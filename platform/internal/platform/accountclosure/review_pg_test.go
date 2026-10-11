package accountclosure

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/storage"
	"github.com/yuqing/platform/internal/platform/payment"
	"image"
	"image/png"
	"testing"
	"time"
)

func closurePNGFixture(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestClosurePGMismatchedStorageCannotReleaseIdentity(t *testing.T) {
	s, root := closureReviewFixture(t)
	ctx := context.Background()
	adapter := storage.NewLocalAvatar(root)
	data := closurePNGFixture(t)
	ref, err := adapter.Put(ctx, "closing", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Put(ctx, "closing", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE users SET avatar_url=$1 WHERE id='closing'`, ref); err != nil {
		t.Fatal(err)
	}
	s.SetAvatarStorage(storage.NewLocalAvatar(t.TempDir()))
	r, err := s.Process(ctx, "closing", 100)
	if err != nil || r.State == "completed" {
		t.Fatalf("different empty root claimed profile PII cleared: %+v %v", r, err)
	}
	var email string
	if err = s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id='closing'`).Scan(&email); err != nil || email != "closing@example.invalid" {
		t.Fatal("wrong root released reusable identity")
	}
	s.SetAvatarStorage(adapter)
	for i := 0; i < 30; i++ {
		r, err = s.Process(ctx, "closing", 100)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "completed" {
			break
		}
	}
	if r.State != "completed" {
		t.Fatalf("verified root could not resume: %+v", r)
	}
	if f, _, _, err := adapter.Open(ctx, ref); err == nil {
		f.Close()
		t.Fatal("correct-root retry left current image")
	}
}

func closureReviewFixture(t *testing.T) (*PGStore, string) {
	t.Helper()
	pool := pgtest.Pool(t, "closure_review")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,name) VALUES('closing','closing@example.invalid','hash','Owner'); INSERT INTO tenants(id,name,slug,db_name,status) VALUES('sole','Sole','sole','sole','active'); INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('sole','closing','tenant_admin'); INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('sole',1,'free')`); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s := NewPGStore(pool)
	s.SetAvatarStorage(storage.NewLocalAvatar(root))
	if _, err := s.Request(ctx, Actor{UserID: "closing", PasswordHash: "hash"}, []string{"sole"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestClosurePGRedactionKeepsTypedAuditTargetLedgerAndCategories(t *testing.T) {
	s, _ := closureReviewFixture(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `INSERT INTO audit_logs(actor_id,tenant_id,action,resource,details_json) VALUES('closing','sole','credit.adjustment','tenant','{"target_type":"tenant","target_id":"sole","ledger_id":"financial-ledger","reason":"closing@example.invalid","request_id":"private request","before":{"balance":1,"plan_code":"free"},"after":{"balance":2,"plan_code":"pro"}}'),('closing',NULL,'member.role','other-user','{"target_type":"user","target_id":"other-user","reason":"Private name","before":{"role":"viewer","status":"active","name":"Private name"},"after":{"role":"analyst","status":"disabled","email":"private@example.invalid"}}')`); err != nil {
		t.Fatal(err)
	}
	var r *Status
	var err error
	for i := 0; i < 50; i++ {
		r, err = s.Process(ctx, "closing", 100)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "completed" {
			break
		}
	}
	if r.State != "completed" {
		t.Fatalf("completion: %+v", r)
	}
	var intact bool
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT details_json->>'ledger_id'='financial-ledger' AND details_json->>'target_id'='sole' AND details_json->'before'->>'plan_code'='free' AND details_json->'after'->>'plan_code'='pro' AND resource='tenant' AND NOT(details_json ? 'reason') FROM audit_logs WHERE action='credit.adjustment') AND (SELECT details_json->>'target_type'='user' AND details_json->>'target_id'='other-user' AND details_json->'before'->>'role'='viewer' AND details_json->'after'->>'role'='analyst' AND details_json->'after'->>'status'='disabled' AND NOT(details_json->'after' ? 'email') AND NOT(details_json->'before' ? 'name') FROM audit_logs WHERE action='member.role')`).Scan(&intact); err != nil || !intact {
		t.Fatalf("anonymization destroyed immutable audit links/categories or retained PII: %v", err)
	}
}

func TestClosurePGRedactionIsBoundedAndResumesBeforeIdentityRelease(t *testing.T) {
	s, root := closureReviewFixture(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `INSERT INTO audit_logs(actor_id,tenant_id,action,resource,details_json) SELECT 'closing','sole','history','closing',jsonb_build_object('target_type','user','target_id','closing','name','private-'||n,'before',jsonb_build_object('balance',n)) FROM generate_series(1,31) n`); err != nil {
		t.Fatal(err)
	}
	var r *Status
	var err error
	for i := 0; i < 50; i++ {
		r, err = s.Process(ctx, "closing", 3)
		if err != nil {
			t.Fatal(err)
		}
		if r.CleanupStatus == "anonymizing" || r.State == "completed" {
			break
		}
	}
	var scrubbed int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='history' AND NOT(details_json ? 'name')`).Scan(&scrubbed); err != nil {
		t.Fatal(err)
	}
	if r.State != "finalizing" || scrubbed == 0 || scrubbed > 3 {
		t.Fatalf("audit batch ignored limit or released identity early: state=%s scrubbed=%d", r.State, scrubbed)
	}
	var email string
	if err = s.pool.QueryRow(ctx, `SELECT email FROM users WHERE id='closing'`).Scan(&email); err != nil || email != "closing@example.invalid" {
		t.Fatal("identity released before all redaction")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _ = s.Process(cancelled, "closing", 3)
	reconstructed := NewPGStore(s.pool)
	reconstructed.SetAvatarStorage(storage.NewLocalAvatar(root))
	for i := 0; i < 80; i++ {
		r, err = reconstructed.Process(ctx, "closing", 3)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "completed" {
			break
		}
	}
	if r.State != "completed" {
		t.Fatalf("restart failed: %+v", r)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='history' AND details_json ? 'name'`).Scan(&scrubbed); err != nil || scrubbed != 0 {
		t.Fatal("redaction restart skipped PII")
	}
}

func TestClosurePGLateRefundDuringRedactionCannotPublishCompletion(t *testing.T) {
	s, _ := closureReviewFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := s.pool.Exec(ctx, `INSERT INTO orders(id,tenant_id,sku_code,kind,credits,amount_cents,channel,state,expires_at) VALUES('late','sole','free','plan',1,1,'test','closed',now()); INSERT INTO audit_logs(actor_id,action,resource,details_json) VALUES('closing','history','closing','{"name":"Private"}'); CREATE FUNCTION pause_closure_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.action='history' THEN PERFORM pg_advisory_xact_lock(741919); END IF; RETURN NEW; END $$; CREATE TRIGGER pause_closure_audit BEFORE UPDATE ON audit_logs FOR EACH ROW EXECUTE FUNCTION pause_closure_audit()`); err != nil {
		t.Fatal(err)
	}
	gate, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT pg_advisory_xact_lock(741919)`); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err = gate.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	result := make(chan *Status, 1)
	failure := make(chan error, 1)
	go func() { r, e := s.Process(ctx, "closing", 100); result <- r; failure <- e }()
	blocked := false
	for i := 0; i < 1000; i++ {
		if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("completion never reached the audit boundary")
	}
	callback := make(chan error, 1)
	go func() { callback <- payment.NewPGStore(s.pool).MarkRefundNeeded(ctx, "late") }()
	select {
	case err = <-callback:
		if err != nil {
			t.Fatal(err)
		}
		callback = nil
	case <-time.After(30 * time.Millisecond):
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-result
	if err = <-failure; err != nil {
		t.Fatal(err)
	}
	if callback != nil {
		if err = <-callback; err != nil {
			t.Fatal(err)
		}
	}
	if r.State == "completed" {
		t.Fatal("completion published after a late outstanding refund became known")
	}
	r, err = s.Process(ctx, "closing", 100)
	if err != nil || r.State == "completed" {
		t.Fatalf("refund blocker did not survive retry: %+v %v", r, err)
	}
}

func TestClosurePGDueDriverAdvancesDeadlinesAndResumesAfterRestart(t *testing.T) {
	s, root := closureReviewFixture(t)
	ctx := context.Background()
	service := NewService(s)
	port, ok := any(service).(interface {
		AdvanceDue(context.Context, int, int) (int, error)
	})
	if !ok {
		t.Fatal("scheduled due and retry discovery is unavailable")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE account_closures SET requested_at=now(),withdraw_until=now()+interval '168 hours'`); err != nil {
		t.Fatal(err)
	}
	if n, err := port.AdvanceDue(ctx, 5, 3); err != nil || n != 0 {
		t.Fatalf("due driver skipped deadline: %d %v", n, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource,details_json) SELECT 'closing','scheduled-history','closing',jsonb_build_object('name','Private '||n) FROM generate_series(1,7) n; INSERT INTO analyses(id,tenant_id,name,state,created_by,report_content) VALUES('retained','sole','Retained','completed','closing','retained content')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		n, err := port.AdvanceDue(ctx, 5, 3)
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Status(ctx, "closing")
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "completed" {
			break
		}
		if n == 0 {
			t.Fatal("due driver lost a resumable step")
		}
		if immediate, err := port.AdvanceDue(ctx, 5, 3); err != nil || immediate != 0 {
			t.Fatalf("due driver ignored persisted retry backoff: %d %v", immediate, err)
		}
		if _, err = s.pool.Exec(ctx, `UPDATE account_closures SET next_attempt_at=now()`); err != nil {
			t.Fatal(err)
		}
		reconstructed := NewPGStore(s.pool)
		reconstructed.SetAvatarStorage(storage.NewLocalAvatar(root))
		port = any(NewService(reconstructed)).(interface {
			AdvanceDue(context.Context, int, int) (int, error)
		})
	}
	r, err := s.Status(ctx, "closing")
	if err != nil || r.State != "completed" {
		t.Fatalf("due discovery did not complete: %+v %v", r, err)
	}
	if r.CleanupStatus != "retention_pending" || r.BusinessRemaining != 1 {
		t.Fatalf("retained data lost from due progress: %+v", r)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE analyses SET created_at=now()-interval '40 days' WHERE id='retained'; UPDATE account_closures SET next_attempt_at=now()`); err != nil {
		t.Fatal(err)
	}
	if n, err := port.AdvanceDue(ctx, 5, 100); err != nil || n != 1 {
		t.Fatalf("retention eligibility was not rediscovered after restart: %d %v", n, err)
	}
	r, err = s.Status(ctx, "closing")
	if err != nil || r.CleanupStatus != "complete" {
		t.Fatalf("retained cleanup did not complete: %+v %v", r, err)
	}
	data, _ := json.Marshal(r)
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	if fields["cleanup_completed_at"] == nil {
		t.Fatal("cleanup completion timestamp is unavailable")
	}
}
