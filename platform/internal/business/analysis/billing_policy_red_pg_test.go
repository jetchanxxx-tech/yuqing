package analysis

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/queue"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"github.com/yuqing/platform/internal/platform/credit"
)

// These tests describe the confirmed K4 billing behavior using existing Go APIs.
// The real PostgreSQL contracts must run in the GitHub-hosted PG workflow.
// No proposed run/actor fields or not-yet-created policy tables are referenced.
type k4BillingPGFixture struct {
	ctx          context.Context
	pool         *pgxpool.Pool
	svc          *Service
	credits      *credit.Service
	tenantID     string
	ordinaryID   string
	fixedAdminID string
	otherAdminID string
}

func newK4BillingPGFixture(t *testing.T, balance int) *k4BillingPGFixture {
	t.Helper()
	if os.Getenv(pgtest.EnvURL) == "" {
		t.Skip("K4 billing contracts require the disposable GitHub-hosted PostgreSQL service")
	}
	parsed, err := pgxpool.ParseConfig(os.Getenv(pgtest.EnvURL))
	if err != nil || !strings.Contains(strings.ToLower(parsed.ConnConfig.Database), "test") {
		t.Fatal("K4 billing contracts require a disposable test database")
	}

	pool := pgTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	q := queue.NewPGQueue(pool, queue.PGQueueOptions{})
	t.Cleanup(func() { _ = q.Close() })
	f := &k4BillingPGFixture{
		ctx:          ctx,
		pool:         pool,
		svc:          NewPGService(pool, q, 4),
		credits:      credit.NewService(credit.NewPGStore(pool)),
		tenantID:     newTestTenant(),
		ordinaryID:   "user-1", // Created by the existing isolated pgTestPool fixture.
		fixedAdminID: "k4-fixed-admin",
		otherAdminID: "k4-other-platform-admin",
	}
	f.exec(t, `INSERT INTO tenants (id, name, slug, db_name, status, plan_code)
		VALUES ($1, 'K4 billing team', $2, $3, 'active', 'lite')`,
		f.tenantID, "k4-"+f.tenantID, "k4_db_"+f.tenantID)
	f.exec(t, `INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, 'admin@pangu.com', 'test-only', 'Fixed administrator', 'active'),
		       ($2, 'other-admin@example.com', 'test-only', 'Other administrator', 'active')`,
		f.fixedAdminID, f.otherAdminID)
	f.exec(t, `INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1, $2, 'tenant_admin'), ($1, $3, 'tenant_admin'), ($1, $4, 'analyst')`,
		f.tenantID, f.ordinaryID, f.fixedAdminID, f.otherAdminID)
	f.exec(t, `INSERT INTO platform_user_roles (user_id, role, granted_by)
		VALUES ($1, 'platform_admin', $1), ($2, 'platform_admin', $1)`,
		f.fixedAdminID, f.otherAdminID)
	if err := f.credits.SetPlanCode(ctx, f.tenantID, "lite"); err != nil {
		t.Fatalf("seed the real plan: %v", err)
	}
	if balance > 0 {
		if err := f.credits.GrantPurchase(ctx, f.tenantID, "k4-seed-"+id.New(), balance); err != nil {
			t.Fatalf("seed purchased report credits: %v", err)
		}
	}
	f.svc.SetCreditReserver(f.credits)
	if _, err := billingpolicy.NewService(pool).Bind(ctx, f.fixedAdminID, true); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *k4BillingPGFixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, statement, args...); err != nil {
		t.Fatalf("prepare isolated billing fixture: %v", err)
	}
}

func (f *k4BillingPGFixture) request(userID, name string) CreateAnalysisRequest {
	return CreateAnalysisRequest{
		TenantID: f.tenantID, UserID: userID, Name: name,
		AnalysisType: "brand", Keywords: []string{"billing fixture"}, Sources: []string{"weibo"},
	}
}

func (f *k4BillingPGFixture) assertBalance(t *testing.T, want int) {
	t.Helper()
	got, err := f.credits.Balance(f.ctx, f.tenantID)
	if err != nil {
		t.Fatalf("read actual balance: %v", err)
	}
	if got != want {
		t.Errorf("balance = %d, want %d", got, want)
	}
}

func (f *k4BillingPGFixture) assertEffects(t *testing.T, analyses, messages, consumes, refunds int) {
	t.Helper()
	checks := []struct {
		name  string
		query string
		want  int
	}{
		{"analyses", `SELECT count(*) FROM analyses WHERE tenant_id=$1`, analyses},
		{"messages", `SELECT count(*) FROM queue_messages
			WHERE topic='analysis.tasks' AND convert_from(body, 'UTF8')::jsonb->>'tenant_id'=$1`, messages},
		{"consumes", `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='consume'`, consumes},
		{"refunds", `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='refund'`, refunds},
	}
	for _, check := range checks {
		var got int
		if err := f.pool.QueryRow(f.ctx, check.query, f.tenantID).Scan(&got); err != nil {
			t.Fatalf("read %s side effects: %v", check.name, err)
		}
		if got != check.want {
			t.Errorf("%s = %d, want %d", check.name, got, check.want)
		}
	}
}

// Seed a completed/queued paid run through the real atomic admission path.
// Remove its historical queue delivery only; financial assertions remain real.
func (f *k4BillingPGFixture) seedPaidAnalysis(t *testing.T, state State) (*AnalysisResult, string) {
	t.Helper()
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "previous paid analysis"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.store.mutate(f.ctx, f.tenantID, a.ID, func(a *AnalysisResult) error {
		a.State = state
		a.Summary = "preserve the completed result"
		a.ReportContent = "<p>previous report</p>"
		if state == StateCompleted {
			a.FinishedAt = time.Now().UTC()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `DELETE FROM queue_messages WHERE convert_from(body,'UTF8')::jsonb->>'analysis_id'=$1`, a.ID)
	a, err = f.svc.Get(f.ctx, f.tenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var consume string
	if err = f.pool.QueryRow(f.ctx, `SELECT consume_tx_id FROM analysis_runs WHERE id=$1`, a.CurrentRunID).Scan(&consume); err != nil {
		t.Fatal(err)
	}
	return a, consume
}

func (f *k4BillingPGFixture) rejectQueueWrites(t *testing.T) {
	t.Helper()
	f.exec(t, `CREATE FUNCTION k4_reject_queue_write() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'k4_queue_write_rejected'; END; $$`)
	f.exec(t, `CREATE TRIGGER k4_reject_queue_write BEFORE INSERT ON queue_messages
		FOR EACH ROW EXECUTE FUNCTION k4_reject_queue_write()`)
}

func (f *k4BillingPGFixture) rejectRefundWrites(t *testing.T) {
	t.Helper()
	f.exec(t, `CREATE FUNCTION k4_reject_refund_write() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.reason='refund' THEN RAISE EXCEPTION 'k4_refund_write_rejected'; END IF;
			RETURN NEW;
		END; $$`)
	f.exec(t, `CREATE TRIGGER k4_reject_refund_write BEFORE INSERT ON credit_transactions
		FOR EACH ROW EXECUTE FUNCTION k4_reject_refund_write()`)
}

func TestK4PGCreateConsumesOneCreditAndPublishesOneTask(t *testing.T) {
	f := newK4BillingPGFixture(t, 1)
	created, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "ordinary paid create"))
	if err != nil {
		t.Fatalf("ordinary PG Create with purchased credit must succeed without beta bypass: %v", err)
	}
	if created.CreatedBy != f.ordinaryID || created.State != StateQueued {
		t.Errorf("created analysis = %+v, want the real ordinary creator and queued state", created)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 1, 1, 1, 0)
	var body []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT body FROM queue_messages
		WHERE topic='analysis.tasks' AND convert_from(body, 'UTF8')::jsonb->>'analysis_id'=$1`, created.ID).Scan(&body); err != nil {
		t.Fatalf("read committed task: %v", err)
	}
	message, err := DecodeTaskMessage(body)
	if err != nil || message.AnalysisID != created.ID || message.TenantID != f.tenantID {
		t.Errorf("committed queue identity = %+v, err=%v", message, err)
	}
}

func TestK4PGZeroCreditsRejectsWithoutTaskOrDebit(t *testing.T) {
	f := newK4BillingPGFixture(t, 0)
	_, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "ordinary zero balance"))
	if !pkgerrors.Is(err, pkgerrors.ErrNoCredits) {
		t.Errorf("zero balance Create error = %v, want NO_CREDITS", err)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 0, 0, 0, 0)
}

func TestK4PGRerunConsumesAnotherCreditAndPublishesOneTask(t *testing.T) {
	f := newK4BillingPGFixture(t, 2)
	a, _ := f.seedPaidAnalysis(t, StateCompleted)
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID}); err != nil {
		t.Fatalf("ordinary paid PG Rerun must succeed without beta bypass: %v", err)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 1, 1, 2, 0)
	got, err := f.svc.Get(f.ctx, f.tenantID, a.ID)
	if err != nil {
		t.Fatalf("read rerun analysis: %v", err)
	}
	if got.State != StateQueued || got.CreatedBy != f.ordinaryID || got.Summary != "" || got.ReportContent != "" {
		t.Errorf("rerun must preserve original creator and clear previous output: %+v", got)
	}
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID}); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("rerunning an active task error = %v, want CONFLICT before attempting another debit", err)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 1, 1, 2, 0)
}

func TestK4PGRerunWithoutCreditsPreservesCompletedResult(t *testing.T) {
	f := newK4BillingPGFixture(t, 1)
	a, _ := f.seedPaidAnalysis(t, StateCompleted)
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID}); !pkgerrors.Is(err, pkgerrors.ErrNoCredits) {
		t.Errorf("zero balance Rerun error = %v, want NO_CREDITS", err)
	}
	got, err := f.svc.Get(f.ctx, f.tenantID, a.ID)
	if err != nil {
		t.Fatalf("read refused rerun: %v", err)
	}
	if got.State != StateCompleted || got.Summary != a.Summary || got.ReportContent != a.ReportContent {
		t.Errorf("refused rerun modified the completed analysis: %+v", got)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 1, 0, 1, 0)
}

func TestK4PGCreateQueueFailureRollsBackCreditAndAnalysis(t *testing.T) {
	f := newK4BillingPGFixture(t, 1)
	f.rejectQueueWrites(t)
	_, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "queue rejected create"))
	if err == nil || !strings.Contains(err.Error(), "k4_queue_write_rejected") {
		t.Errorf("Create must reach the injected queue failure, got %v", err)
	}
	f.assertBalance(t, 1)
	// A compensating refund after a separate debit is not an atomic rollback.
	f.assertEffects(t, 0, 0, 0, 0)
}

func TestK4PGRerunQueueFailurePreservesCreditAndPreviousOutput(t *testing.T) {
	f := newK4BillingPGFixture(t, 2)
	a, _ := f.seedPaidAnalysis(t, StateCompleted)
	f.rejectQueueWrites(t)
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID}); err == nil || !strings.Contains(err.Error(), "k4_queue_write_rejected") {
		t.Errorf("Rerun must reach the injected queue failure, got %v", err)
	}
	got, err := f.svc.Get(f.ctx, f.tenantID, a.ID)
	if err != nil {
		t.Fatalf("read rolled back rerun: %v", err)
	}
	if got.State != StateCompleted || got.Summary != a.Summary || got.ReportContent != a.ReportContent {
		t.Errorf("queue failure destroyed previous completed output: %+v", got)
	}
	f.assertBalance(t, 1)
	f.assertEffects(t, 1, 0, 1, 0)
}

func TestK4PGConcurrentCreateCannotOversellPurchasedCredits(t *testing.T) {
	f := newK4BillingPGFixture(t, 5)
	const attempts = 20
	start := make(chan struct{})
	results := make(chan error, attempts)
	var workers sync.WaitGroup
	for index := 0; index < attempts; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, fmt.Sprintf("concurrent paid create %d", index)))
			results <- err
		}(index)
	}
	close(start)
	workers.Wait()
	close(results)
	succeeded, insufficient := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case pkgerrors.Is(err, pkgerrors.ErrNoCredits):
			insufficient++
		default:
			t.Errorf("ordinary concurrent Create returned an unexpected error: %v", err)
		}
	}
	if succeeded != 5 || insufficient != 15 {
		t.Errorf("concurrent admission successes/no-credits = %d/%d, want 5/15", succeeded, insufficient)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 5, 5, 5, 0)
}

// This is a regression against the existing global beta setting, not proof
// that immutable UID provisioning already exists. It must stay red while that
// setting grants another member, another admin role, or a replacement email
// holder free use. The fixed binding fixture is added with migration 0015.
func TestK4PGLegacyBetaFlagCannotTransferFixedAdminExemption(t *testing.T) {
	cases := []struct {
		name        string
		useAdmin    bool
		moveEmail   bool
		otherAdmin  bool
		wantSuccess bool
	}{
		{"fixed administrator", true, false, false, true},
		{"ordinary member of administrator tenant", false, false, false, false},
		{"another platform administrator", false, false, true, false},
		{"fixed UID after its email changes", true, true, false, true},
		{"different UID receiving the original email", false, true, false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newK4BillingPGFixture(t, 0)
			// Only the verified immutable UID binding can bypass report limits.
			if testCase.moveEmail {
				f.exec(t, `UPDATE users SET email='fixed-admin-renamed@example.com' WHERE id=$1`, f.fixedAdminID)
				f.exec(t, `UPDATE users SET email='admin@pangu.com' WHERE id=$1`, f.ordinaryID)
			}
			userID := f.ordinaryID
			if testCase.useAdmin {
				userID = f.fixedAdminID
			} else if testCase.otherAdmin {
				userID = f.otherAdminID
			}
			_, err := f.svc.Create(f.ctx, f.request(userID, "identity exemption boundary"))
			wantAnalyses := 0
			if testCase.wantSuccess {
				wantAnalyses = 1
				if err != nil {
					t.Errorf("fixed verified administrator UID must remain exempt, got %v", err)
				}
			} else if !pkgerrors.Is(err, pkgerrors.ErrNoCredits) {
				t.Errorf("non-exempt UID must have NO_CREDITS despite legacy beta flag/tenant/role/email, got %v", err)
			}
			f.assertBalance(t, 0)
			f.assertEffects(t, wantAnalyses, wantAnalyses, 0, 0)
		})
	}
}

func TestK4PGFailureOfUnchargedRerunCannotRefundPriorSuccessfulConsume(t *testing.T) {
	f := newK4BillingPGFixture(t, 1)
	a, oldConsumeID := f.seedPaidAnalysis(t, StateCompleted)
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.fixedAdminID}); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `DELETE FROM queue_messages WHERE convert_from(body,'UTF8')::jsonb->>'analysis_id'=$1`, a.ID)

	if err := f.svc.markFailed(f.ctx, f.tenantID, a.ID, "current_unpaid_run_failed"); err != nil {
		t.Fatalf("mark the uncharged current execution failed: %v", err)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 1, 0, 1, 0)
	var oldConsumeRefunds int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM credit_transactions
		WHERE tenant_id=$1 AND reason='refund' AND consume_tx_id=$2`, f.tenantID, oldConsumeID).Scan(&oldConsumeRefunds); err != nil {
		t.Fatalf("read old successful consumption refunds: %v", err)
	}
	if oldConsumeRefunds != 0 {
		t.Errorf("an uncharged current execution refunded prior successful consume %s", oldConsumeID)
	}
}

func TestK4PGFailureRefundsOnlyLatestPaidExecutionOnce(t *testing.T) {
	f := newK4BillingPGFixture(t, 2)
	a, oldConsumeID := f.seedPaidAnalysis(t, StateCompleted)
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID}); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `DELETE FROM queue_messages WHERE convert_from(body,'UTF8')::jsonb->>'analysis_id'=$1`, a.ID)

	if err := f.svc.markFailed(f.ctx, f.tenantID, a.ID, "current_paid_run_failed"); err != nil {
		t.Fatalf("fail the current paid execution: %v", err)
	}
	if err := f.svc.markFailed(f.ctx, f.tenantID, a.ID, "duplicate_failure"); err != nil {
		t.Fatalf("repeat the terminal failure: %v", err)
	}
	f.assertBalance(t, 1)
	f.assertEffects(t, 1, 0, 2, 1)
	var refundedConsumeID string
	if err := f.pool.QueryRow(f.ctx, `SELECT consume_tx_id FROM credit_transactions
		WHERE tenant_id=$1 AND reason='refund'`, f.tenantID).Scan(&refundedConsumeID); err != nil {
		t.Fatalf("read the exact paired refund: %v", err)
	}
	if refundedConsumeID == oldConsumeID || refundedConsumeID == "" {
		t.Errorf("refund paired to %q, want the second consume, not old successful consume %q", refundedConsumeID, oldConsumeID)
	}
}

func TestK4PGRefundFailureCannotCommitTerminalState(t *testing.T) {
	operations := []struct {
		name string
		run  func(*k4BillingPGFixture, string) error
	}{
		{"pipeline failure", func(f *k4BillingPGFixture, analysisID string) error {
			return f.svc.markFailed(f.ctx, f.tenantID, analysisID, "pipeline_failed")
		}},
		{"user cancellation", func(f *k4BillingPGFixture, analysisID string) error {
			return f.svc.Cancel(f.ctx, f.tenantID, analysisID)
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			f := newK4BillingPGFixture(t, 1)
			a, _ := f.seedPaidAnalysis(t, StateQueued)
			f.rejectRefundWrites(t)
			if err := operation.run(f, a.ID); err == nil {
				t.Error("terminal operation reported success after its paired refund was rejected")
			}
			got, err := f.svc.Get(f.ctx, f.tenantID, a.ID)
			if err != nil {
				t.Fatalf("read state after rejected refund: %v", err)
			}
			if got.State != StateQueued || got.ErrorCode != "" || !got.FinishedAt.IsZero() {
				t.Errorf("terminal state committed without its refund: %+v", got)
			}
			f.assertBalance(t, 0)
			f.assertEffects(t, 1, 0, 1, 0)
		})
	}
}
