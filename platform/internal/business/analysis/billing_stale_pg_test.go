package analysis

import (
	"context"
	"encoding/json"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"testing"
	"time"
)

type k4BlockedFetcher struct {
	entered chan struct{}
	release chan struct{}
}

func (f *k4BlockedFetcher) Fetch(ctx context.Context, req FetchRequest) ([]Document, error) {
	close(f.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.release:
		return []Document{{ID: "stale-doc", Title: "stale output", Content: "old run data", SourceType: "weibo"}}, nil
	}
}

func TestK4PGStaleWorkerCannotWriteIntoNewRerun(t *testing.T) {
	f := newK4BillingPGFixture(t, 2)
	// Exercise real atomic admission with purchased credits.
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "stale worker isolation"))
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT body FROM queue_messages WHERE convert_from(body,'UTF8')::jsonb->>'analysis_id'=$1`, a.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	msg, err := DecodeTaskMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &k4BlockedFetcher{entered: make(chan struct{}), release: make(chan struct{})}
	pipeline := NewPipeline(f.svc, fetcher, time.Minute, nil)
	finished := make(chan error, 1)
	go func() { finished <- pipeline.Handle(f.ctx, msg) }()
	select {
	case <-fetcher.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	if err := f.svc.Cancel(f.ctx, f.tenantID, a.ID); err != nil {
		close(fetcher.release)
		t.Fatal(err)
	}
	if err := f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID}); err != nil {
		close(fetcher.release)
		t.Fatal(err)
	}
	close(fetcher.release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("old worker did not finish")
	}
	current, err := f.svc.Get(f.ctx, f.tenantID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != StateQueued || current.DocCount != 0 || current.Summary != "" || current.ReportContent != "" {
		t.Fatalf("stale worker changed new run: %+v", current)
	}
	var docs int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM raw_documents WHERE analysis_id=$1`, a.ID).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Fatalf("stale worker persisted %d old documents into new run", docs)
	}
}

func TestK4PGTaskMessageContainsPersistedRunIdentity(t *testing.T) {
	f := newK4BillingPGFixture(t, 1)
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "durable run identity"))
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT body FROM queue_messages WHERE convert_from(body,'UTF8')::jsonb->>'analysis_id'=$1`, a.ID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(body, &message); err != nil {
		t.Fatal(err)
	}
	runID, _ := message["run_id"].(string)
	if runID == "" {
		t.Fatal("committed task lacks durable per-execution run_id")
	}
	var persisted string
	if err := f.pool.QueryRow(f.ctx, `SELECT current_run_id FROM analyses WHERE id=$1`, a.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != runID {
		t.Fatalf("message run %s differs from analysis run %s", runID, persisted)
	}
}

func TestK4PGRerunUsesCurrentActorAndPreservesOriginalCreator(t *testing.T) {
	f := newK4BillingPGFixture(t, 2)
	paid, oldConsume := f.seedPaidAnalysis(t, StateCompleted)
	if err := f.svc.Rerun(f.ctx, f.tenantID, paid.ID, billingpolicy.Actor{UserID: f.fixedAdminID}); err != nil {
		t.Fatal(err)
	}
	current, err := f.svc.Get(f.ctx, f.tenantID, paid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CreatedBy != f.ordinaryID || current.CurrentRunID == paid.CurrentRunID {
		t.Fatalf("actor changed creator or reused run: %+v", current)
	}
	if err = f.svc.Cancel(f.ctx, f.tenantID, paid.ID); err != nil {
		t.Fatal(err)
	}
	f.assertBalance(t, 1)
	var refunds int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM credit_transactions WHERE consume_tx_id=$1`, oldConsume).Scan(&refunds); err != nil || refunds != 0 {
		t.Fatalf("old success refunded %d: %v", refunds, err)
	}
	free, err := f.svc.Create(f.ctx, f.request(f.fixedAdminID, "fixed created asset"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.store.mutate(f.ctx, f.tenantID, free.ID, func(a *AnalysisResult) error { a.State = StateCompleted; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Rerun(f.ctx, f.tenantID, free.ID, billingpolicy.Actor{UserID: f.ordinaryID}); err != nil {
		t.Fatal(err)
	}
	charged, err := f.svc.Get(f.ctx, f.tenantID, free.ID)
	if err != nil {
		t.Fatal(err)
	}
	if charged.CreatedBy != f.fixedAdminID {
		t.Fatal("rerun rewrote asset creator")
	}
	f.assertBalance(t, 0)
	var actor, mode string
	if err = f.pool.QueryRow(f.ctx, `SELECT actor_user_id,charge_mode FROM analysis_runs WHERE id=$1`, charged.CurrentRunID).Scan(&actor, &mode); err != nil {
		t.Fatal(err)
	}
	if actor != f.ordinaryID || mode != "normal" {
		t.Fatalf("new run actor/mode=%s/%s", actor, mode)
	}
}

func TestK4PGConcurrentRerunCreatesOnlyOneNewCharge(t *testing.T) {
	f := newK4BillingPGFixture(t, 2)
	a, _ := f.seedPaidAnalysis(t, StateCompleted)
	errors := make(chan error, 20)
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func() {
			<-start
			errors <- f.svc.Rerun(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID})
		}()
	}
	close(start)
	succeeded, conflicts := 0, 0
	for i := 0; i < 20; i++ {
		err := <-errors
		if err == nil {
			succeeded++
		} else if pkgerrors.Is(err, pkgerrors.ErrConflict) {
			conflicts++
		} else {
			t.Errorf("unexpected concurrent rerun: %v", err)
		}
	}
	if succeeded != 1 || conflicts != 19 {
		t.Fatalf("success/conflicts=%d/%d", succeeded, conflicts)
	}
	f.assertBalance(t, 0)
	f.assertEffects(t, 1, 1, 2, 0)
}

func TestK4PGAdmissionRechecksQueuedJWTVersion(t *testing.T) {
	f := newK4BillingPGFixture(t, 1)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(f.ctx, `SELECT pg_advisory_xact_lock(741914)`); err != nil {
		t.Fatal(err)
	}
	version := int64(0)
	req := f.request(f.ordinaryID, "revoked queued credential")
	req.ActorTokenVersion = &version
	result := make(chan error, 1)
	go func() { _, err := f.svc.Create(f.ctx, req); result <- err }()
	// The actor version changes while admission is held behind administration.
	if _, err = blocker.Exec(f.ctx, `UPDATE users SET token_version=1 WHERE id=$1`, f.ordinaryID); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
		t.Fatalf("stale credential admitted: %v", err)
	}
	f.assertBalance(t, 1)
	f.assertEffects(t, 0, 0, 0, 0)
}
