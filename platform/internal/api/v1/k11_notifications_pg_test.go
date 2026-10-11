package v1_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/storage"
	"github.com/yuqing/platform/internal/platform/accountclosure"
	"github.com/yuqing/platform/internal/platform/auth"
)

type k11TaskDispatcher interface {
	SetSender(auth.MailSender)
	Dispatch(context.Context, int) error
}

func k11Dispatcher(t *testing.T, e *billingActorPGEnv, box auth.MailSender) k11TaskDispatcher {
	t.Helper()
	// Test-only interface lookup lets the initial missing-component RED compile
	// against the preceding accepted source, rather than fail on a new type name.
	field := reflect.ValueOf(e.deps).Elem().FieldByName("TaskNotifications")
	if !field.IsValid() || field.IsNil() {
		t.Fatal("committed task notification dispatcher is not wired")
	}
	port, ok := field.Interface().(k11TaskDispatcher)
	if !ok {
		t.Fatal("task notification dispatcher does not expose its sender/dispatch ports")
	}
	port.SetSender(box)
	return port
}

func k11CompleteTask(t *testing.T, e *billingActorPGEnv, access, tid string) string {
	t.Helper()
	created := adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/analyses", access, map[string]any{"name": "Private task title must never enter notice receipts"}), 201)
	aid := created["id"].(string)
	a, err := e.deps.Analysis.Get(context.Background(), tid, aid)
	if err != nil || a.CurrentRunID == "" {
		t.Fatalf("actual charged task run missing: %+v %v", a, err)
	}
	for _, state := range []string{"acquiring_budget", "fetching", "analyzing", "generating_report", "completed"} {
		if err := e.deps.Analysis.TransitionRun(context.Background(), tid, aid, a.CurrentRunID, state); err != nil {
			t.Fatal(err)
		}
	}
	return aid
}

func TestK11PGTaskCompletionDurableIntentPreferenceAndAcceptedReplay(t *testing.T) {
	for _, optedIn := range []bool{false, true} {
		t.Run(map[bool]string{false: "opted_out", true: "opted_in"}[optedIn], func(t *testing.T) {
			e := newBillingActorPGEnv(t)
			access, _, user := mustRegister(t, e.router, "k11-completion@example.invalid", "Completion")
			tid := user["tenant_id"].(string)
			k9Exec(t, e, `UPDATE users SET email_verified_at=now(),notification_prefs=jsonb_build_object('task_completed',$2::bool) WHERE id=$1`, user["user_id"], optedIn)
			aid := k11CompleteTask(t, e, access, tid)
			if k9Count(t, e, `SELECT count(*) FROM account_notification_events WHERE analysis_id=$1 AND category='task_completed'`, aid) != 1 {
				t.Fatal("committed actual run has no unique durable completion intent")
			}
			// Reconstruct the graph: an in-process callback cannot satisfy this gate.
			e.rebuild()
			box := &identitySandbox{}
			port := k11Dispatcher(t, e, box)
			if err := port.Dispatch(context.Background(), 25); err != nil {
				t.Fatal(err)
			}
			if err := port.Dispatch(context.Background(), 25); err != nil {
				t.Fatal(err)
			}
			state := "cancelled"
			count := 0
			if optedIn {
				state, count = "accepted", 1
			}
			if box.count() != count || k9Count(t, e, `SELECT count(*) FROM account_notification_events WHERE analysis_id=$1 AND state=$2`, aid, state) != 1 {
				t.Fatalf("preference or terminal receipt replay failed: sends=%d expected=%d", box.count(), count)
			}
			if optedIn {
				payload := box.delivered(t)
				if !strings.Contains(payload, "完成") || strings.Contains(payload, "Private task") || strings.Contains(payload, "token=") {
					t.Fatal("task notice exposes private title/credential or claims an unwired event")
				}
			}
			var receipt string
			if err := e.pool.QueryRow(context.Background(), `SELECT receipt::text FROM account_notification_events WHERE analysis_id=$1`, aid).Scan(&receipt); err != nil || strings.Contains(receipt, "example.invalid") || strings.Contains(receipt, "Private task") {
				t.Fatalf("receipt contains recipient/body PII: %v", err)
			}
			adminContractResponse(t, doReq(t, e.router, "PUT", "/api/v1/user/notification-preferences", access, map[string]any{"task_completed": true}), 200)
			if err := port.Dispatch(context.Background(), 25); err != nil || box.count() != count {
				t.Fatal("preference retry reactivated a terminal intent")
			}
		})
	}
}

func TestK11PGTaskNotificationFailureRetriesWithoutUndoingCompletion(t *testing.T) {
	e := newBillingActorPGEnv(t)
	access, _, user := mustRegister(t, e.router, "k11-retry@example.invalid", "Retry")
	tid := user["tenant_id"].(string)
	k9Exec(t, e, `UPDATE users SET email_verified_at=now(),notification_prefs='{"task_completed":true}' WHERE id=$1`, user["user_id"])
	aid := k11CompleteTask(t, e, access, tid)
	box := &identitySandbox{rejected: true}
	port := k11Dispatcher(t, e, box)
	if err := port.Dispatch(context.Background(), 25); err == nil {
		t.Fatal("failed delivery was reported successful")
	}
	if k9Count(t, e, `SELECT count(*) FROM analyses WHERE id=$1 AND state='completed'`, aid) != 1 || k9Count(t, e, `SELECT count(*) FROM account_notification_events WHERE analysis_id=$1 AND state='failed' AND attempts=1`, aid) != 1 || k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, tid) != 0 {
		t.Fatal("delivery failure rolled back business or lost durable retry")
	}
	e.rebuild()
	box.rejected = false
	port = k11Dispatcher(t, e, box)
	k9Exec(t, e, `UPDATE account_notification_events SET next_attempt_at=now() WHERE analysis_id=$1`, aid)
	if err := port.Dispatch(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	if err := port.Dispatch(context.Background(), 25); err != nil || box.count() != 1 || k9Count(t, e, `SELECT count(*) FROM account_notification_events WHERE analysis_id=$1 AND state='accepted' AND attempts=2 AND receipt->>'state'='accepted'`, aid) != 1 {
		t.Fatal("reconstructed retry did not persist one terminal sandbox acceptance")
	}
}

type k11BlockedTaskSender struct {
	*identitySandbox
	entered chan struct{}
	release chan struct{}
}

func (s *k11BlockedTaskSender) Send(ctx context.Context, to, subject, body string) error {
	close(s.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return s.identitySandbox.Send(ctx, to, subject, body)
	}
}

func TestK11PGClosurePreservesTaskNoticeInFlightAndItsReceipt(t *testing.T) {
	e := newBillingActorPGEnv(t)
	access, _, user := mustRegister(t, e.router, "k11-closure@example.invalid", "Closing")
	uid, tid := user["user_id"].(string), user["tenant_id"].(string)
	k9Exec(t, e, `UPDATE users SET email_verified_at=now(),notification_prefs='{"task_completed":true}' WHERE id=$1`, uid)
	aid := k11CompleteTask(t, e, access, tid)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	box := &k11BlockedTaskSender{identitySandbox: &identitySandbox{}, entered: make(chan struct{}), release: make(chan struct{})}
	port := k11Dispatcher(t, e, box)
	done := make(chan error, 1)
	go func() { done <- port.Dispatch(ctx, 25) }()
	select {
	case <-box.entered:
	case err := <-done:
		t.Fatalf("dispatch did not reach the supplier boundary: %v", err)
	case <-ctx.Done():
		t.Fatal("dispatch did not reach the supplier boundary before deadline")
	}
	closing := accountclosure.NewPGStore(e.pool)
	closing.SetAvatarStorage(storage.NewLocalAvatar(t.TempDir()))
	var hash string
	if err := e.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, uid).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if _, err := closing.Request(ctx, accountclosure.Actor{UserID: uid, PasswordHash: hash}, []string{tid}); err != nil {
		t.Fatal(err)
	}
	k9Exec(t, e, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second' WHERE user_id=$1`, uid)
	r, err := closing.Process(ctx, uid, 100)
	if err != nil || r.State == "completed" || k9Count(t, e, `SELECT count(*) FROM account_notification_events WHERE analysis_id=$1 AND state='processing'`, aid) != 1 {
		t.Fatalf("closure erased task attempt or published before receipt: %+v %v", r, err)
	}
	close(box.release)
	if err := <-done; err != nil {
		t.Fatalf("request made original task receipt inadmissible: %v", err)
	}
	r, err = closing.Process(ctx, uid, 100)
	if err != nil || r.State != "completed" || box.count() != 1 || k9Count(t, e, `SELECT count(*) FROM account_notification_events WHERE analysis_id=$1 AND state='accepted' AND receipt->>'state'='accepted'`, aid) != 1 {
		t.Fatalf("settled task receipt did not allow resumed closure: %+v %v", r, err)
	}
}
