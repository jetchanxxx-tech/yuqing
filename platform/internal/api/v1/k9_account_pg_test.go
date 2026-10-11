package v1_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/credit"
)

func k9Env(t *testing.T) (*billingActorPGEnv, *identitySandbox, string, map[string]any) {
	t.Helper()
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	access, _, user := mustRegister(t, e.router, "k9-admin@example.invalid", "K9 Admin")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, user["user_id"]); err != nil {
		t.Fatal(err)
	}
	return e, box, access, user
}
func k9Exec(t *testing.T, e *billingActorPGEnv, q string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func k9Count(t *testing.T, e *billingActorPGEnv, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func k9Create(t *testing.T, e *billingActorPGEnv, access, email string) map[string]any {
	t.Helper()
	return adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users", access, map[string]any{"email": email, "name": "K9 Pending", "tenant_name": "K9 Initial Team"}), 201)
}
func k9SeedPending(t *testing.T, e *billingActorPGEnv, id, email string) {
	t.Helper()
	k9Exec(t, e, `INSERT INTO users(id,email,password_hash,name,status) VALUES($1,$2,'unusable','K9 Pending','pending_activation')`, id, email)
}
func TestK9PGCreateActivationAndTrialReplay(t *testing.T) {
	e, box, access, _ := k9Env(t)
	body := k9Create(t, e, access, "k9-created@example.invalid")
	uid, ok := body["user_id"].(string)
	if !ok || uid == "" {
		t.Fatalf("creation must return committed user_id: %v", body)
	}
	tid, ok := body["tenant_id"].(string)
	if !ok || tid == "" {
		t.Fatalf("creation must return committed tenant_id: %v", body)
	}
	if body["status"] != "pending_activation" || body["activation"].(map[string]any)["state"] != "accepted" {
		t.Fatalf("unexpected creation state: %v", body)
	}
	if k9Count(t, e, `SELECT count(*) FROM tenant_members WHERE tenant_id=$1 AND user_id=$2 AND role='tenant_admin'`, tid, uid) != 1 || k9Count(t, e, `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='trial' AND delta=1`, tid) != 1 || k9Count(t, e, `SELECT count(*) FROM platform_user_roles WHERE user_id=$1`, uid) != 0 {
		t.Fatal("creation lost owner/trial or granted platform role")
	}
	if k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, tid) != 1 {
		t.Fatal("wrong current trial grant")
	}
	for _, key := range []string{"password", "password_hash", "token", "access_token", "refresh_token"} {
		if _, exists := body[key]; exists {
			t.Fatalf("creation exposed %s", key)
		}
	}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "k9-created@example.invalid", "password": "StrongPassword123"}), 401)
	value := box.delivered(t)
	confirmation := map[string]string{"token": value, "new_password": "StrongPassword123"}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/password-reset/confirm", "", confirmation), 400)
	confirmed := adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", confirmation), 200)
	if confirmed["requires_relogin"] != true || len(confirmed) != 2 {
		t.Fatalf("activation must return no identity or credential: %v", confirmed)
	}
	replay := doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", confirmation)
	if replay.Code < 400 {
		t.Fatal("activation replay succeeded")
	}
	if k9Count(t, e, `SELECT count(*) FROM users WHERE id=$1 AND status='active' AND token_version=1 AND row_version=1 AND password_changed_at IS NOT NULL`, uid) != 1 {
		t.Fatal("activation/password/version not atomic")
	}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "k9-created@example.invalid", "password": "StrongPassword123"}), 200)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users", access, map[string]string{"email": "K9-CREATED@example.invalid", "name": "Duplicate"}), 409)
	if k9Count(t, e, `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='trial'`, tid) != 1 {
		t.Fatal("creation replay granted another trial")
	}
	e.rebuild()
	detail := adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/admin/tenants/"+tid, access, nil), 200)
	if len(detail["credit_transactions"].([]any)) != 1 {
		t.Fatal("admin detail lost actual trial ledger")
	}
}
func TestK9PGCreateFailedDispatchRemainsDiscoverableAndRetryable(t *testing.T) {
	e, box, access, _ := k9Env(t)
	box.unavailable = true
	body := k9Create(t, e, access, "k9-delivery@example.invalid")
	uid := body["user_id"].(string)
	state := body["activation"].(map[string]any)
	if state["state"] != "failed" || state["error_code"] != "SERVICE_UNAVAILABLE" {
		t.Fatalf("created account needs explicit failed delivery state: %v", state)
	}
	detail := adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/admin/users/"+uid, access, nil), 200)
	if len(detail["notifications"].([]any)) != 1 {
		t.Fatal("failed dispatch was not durable")
	}
	box.unavailable = false
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users/"+uid+"/activation-resend", access, nil), 202)
	first := box.delivered(t)
	k9Exec(t, e, `DELETE FROM verification_send_gates WHERE gate_key LIKE 'target:set_password:%'`)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users/"+uid+"/activation-resend", access, nil), 202)
	second := box.delivered(t)
	if first == second {
		t.Fatal("resend reused plaintext credential")
	}
	if doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", map[string]string{"token": first, "new_password": "StrongPassword123"}).Code < 400 {
		t.Fatal("resend left old activation usable")
	}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", map[string]string{"token": second, "new_password": "StrongPassword123"}), 200)
	box.rejected = true
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users/"+uid+"/password-reset", access, nil), 503)
	if k9Count(t, e, `SELECT count(*) FROM verification_tokens WHERE user_id=$1 AND purpose='password_reset' AND delivery_status='rejected' AND used_at IS NOT NULL`, uid) != 1 {
		t.Fatal("failed reset lacks rejected durable credential")
	}
	box.rejected = false
	k9Exec(t, e, `DELETE FROM verification_send_gates WHERE gate_key LIKE 'target:password_reset:%'`)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users/"+uid+"/password-reset", access, nil), 202)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/password-reset/confirm", "", map[string]string{"token": box.delivered(t), "new_password": "ResetPassword456"}), 200)
	if k9Count(t, e, `SELECT count(*) FROM credit_transactions WHERE reason='trial' AND tenant_id=$1`, body["tenant_id"]) != 1 {
		t.Fatal("retry/reset changed trial")
	}
}
func TestK9PGCreateValidationAndAtomicRollback(t *testing.T) {
	e, _, access, _ := k9Env(t)
	for _, body := range []map[string]any{{"email": "invalid", "name": "N"}, {"email": "ok@example.invalid", "name": "N", "platform_admin": true}, {"email": "ok@example.invalid", "name": "N", "password": "AdminChosen123"}, {"email": "ok@example.invalid", "name": " "}} {
		adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users", access, body), 400)
	}
	for _, table := range []string{"tenants", "tenant_members", "credit_transactions", "audit_logs"} {
		t.Run(table, func(t *testing.T) {
			before := []int{}
			for _, name := range []string{"users", "tenants", "tenant_members", "report_credits", "credit_transactions", "audit_logs"} {
				before = append(before, k9Count(t, e, "SELECT count(*) FROM "+name))
			}
			k9Exec(t, e, `CREATE FUNCTION reject_k9_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'isolated k9 rejection'; END $$`)
			k9Exec(t, e, `CREATE TRIGGER reject_k9 BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION reject_k9_insert()`)
			w := doReq(t, e.router, "POST", "/api/v1/admin/users", access, map[string]string{"email": "rollback@example.invalid", "name": "Rollback"})
			if w.Code != 500 {
				t.Errorf("injected %s failure status=%d", table, w.Code)
			}
			k9Exec(t, e, `DROP TRIGGER reject_k9 ON `+table)
			k9Exec(t, e, `DROP FUNCTION reject_k9_insert()`)
			after := []int{}
			for _, name := range []string{"users", "tenants", "tenant_members", "report_credits", "credit_transactions", "audit_logs"} {
				after = append(after, k9Count(t, e, "SELECT count(*) FROM "+name))
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%s failure left partial provisioning: %v -> %v", table, before, after)
			}
		})
	}
}
func TestK9PGActivationStatePurposeVersionAndAtomicFailure(t *testing.T) {
	for _, state := range []string{"pending_activation", "active", "disabled", "closed", "stale", "rollback"} {
		t.Run(state, func(t *testing.T) {
			e, box, access, _ := k9Env(t)
			k9SeedPending(t, e, "pending", "activation@example.invalid")
			adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users/pending/activation-resend", access, nil), 202)
			value := box.delivered(t)
			if state == "stale" {
				k9Exec(t, e, `UPDATE users SET token_version=1 WHERE id='pending'`)
			} else if state != "pending_activation" && state != "rollback" {
				k9Exec(t, e, `UPDATE users SET status=$1 WHERE id='pending'`, state)
			}
			body := map[string]string{"token": value, "new_password": "StrongPassword123"}
			if state == "rollback" {
				k9Exec(t, e, `ALTER TABLE users ADD CONSTRAINT reject_k9_password CHECK(password_hash='unusable' OR id<>'pending')`)
			}
			w := doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", body)
			if state == "pending_activation" {
				adminContractResponse(t, w, 200)
			} else if w.Code < 400 {
				t.Fatalf("%s activation succeeded", state)
			}
			if state == "rollback" {
				k9Exec(t, e, `ALTER TABLE users DROP CONSTRAINT reject_k9_password`)
				adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", body), 200)
			}
			want := 0
			if state == "pending_activation" || state == "rollback" {
				want = 1
			}
			if k9Count(t, e, `SELECT count(*) FROM users WHERE id='pending' AND password_changed_at IS NOT NULL`) != want {
				t.Fatal("activation failure partially changed password")
			}
		})
	}
}
func TestK9PGCreditAdjustmentIntentCASLedgerAndAudit(t *testing.T) {
	e, _, access, admin := k9Env(t)
	tid := admin["tenant_id"].(string)
	path := "/api/v1/admin/tenants/" + tid + "/credit-adjustments"
	version := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
	input := map[string]any{"delta": 3, "reason": "  K9 compensation  ", "idempotency_key": "k9-key", "expected_version": version}
	first := adminContractResponse(t, doReq(t, e.router, "POST", path, access, input), 200)
	second := adminContractResponse(t, doReq(t, e.router, "POST", path, access, input), 200)
	if !reflect.DeepEqual(first, second) || first["balance_after"] != float64(4) || first["reason_detail"] != "K9 compensation" || first["actor_id"] != admin["user_id"] {
		t.Fatalf("idempotency/result mismatch: %v / %v", first, second)
	}
	if k9Count(t, e, `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND idempotency_key='k9-key'`, tid) != 1 || k9Count(t, e, `SELECT count(*) FROM audit_logs WHERE action='credit.adjustment' AND tenant_id=$1`, tid) != 1 {
		t.Fatal("duplicate ledger/audit")
	}
	input["delta"] = 2
	adminContractResponse(t, doReq(t, e.router, "POST", path, access, input), 409)
	input["delta"] = 3
	input["expected_version"] = version + 1
	adminContractResponse(t, doReq(t, e.router, "POST", path, access, input), 409)
	input["idempotency_key"] = "negative"
	input["delta"] = -5
	adminContractResponse(t, doReq(t, e.router, "POST", path, access, input), 402)
	if k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, tid) != 4 {
		t.Fatal("rejected adjustment changed balance")
	}
}
func TestK9PGCreditDirectRejectsRevokedActor(t *testing.T) {
	e, _, _, admin := k9Env(t)
	k9Exec(t, e, `DELETE FROM platform_user_roles WHERE user_id=$1`, admin["user_id"])
	_, err := e.deps.Credits.Adjust(context.Background(), credit.Adjustment{TenantID: admin["tenant_id"].(string), ActorID: admin["user_id"].(string), Delta: 1, ReasonDetail: "revoked", IdempotencyKey: "revoked", ExpectedVersion: 1})
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Fatalf("credit storage must reject revoked actor with CONFLICT, got %v", err)
	}
	if k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, admin["tenant_id"]) != 1 {
		t.Fatal("revoked actor mutated balance")
	}
}
func TestK9PGCreditConcurrencyAndRollback(t *testing.T) {
	e, _, access, admin := k9Env(t)
	tid := admin["tenant_id"].(string)
	path := "/api/v1/admin/tenants/" + tid + "/credit-adjustments"
	version := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
	var wg sync.WaitGroup
	out := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out <- doReq(t, e.router, "POST", path, access, map[string]any{"delta": -1, "reason": "concurrent", "idempotency_key": fmt.Sprintf("concurrent-%d", i), "expected_version": version}).Code
		}(i)
	}
	wg.Wait()
	close(out)
	ok := 0
	for status := range out {
		if status == 200 {
			ok++
		} else if status != 409 {
			t.Errorf("unexpected concurrent status %d", status)
		}
	}
	if ok != 1 || k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, tid) != 0 {
		t.Fatalf("concurrent decrements successes=%d", ok)
	}
	for _, table := range []string{"credit_transactions", "audit_logs"} {
		before := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
		k9Exec(t, e, `CREATE FUNCTION reject_k9_credit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'isolated k9 rejection'; END $$`)
		k9Exec(t, e, `CREATE TRIGGER reject_k9 BEFORE INSERT ON `+table+` FOR EACH ROW EXECUTE FUNCTION reject_k9_credit()`)
		adminContractResponse(t, doReq(t, e.router, "POST", path, access, map[string]any{"delta": 2, "reason": "must rollback", "idempotency_key": "rollback-" + table, "expected_version": before}), 500)
		k9Exec(t, e, `DROP TRIGGER reject_k9 ON `+table)
		k9Exec(t, e, `DROP FUNCTION reject_k9_credit()`)
		if k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, tid) != 0 || k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid) != before {
			t.Fatal("failed ledger/audit changed balance or version")
		}
	}
}
func TestK9PGQueuedOriginalAdminCannotCreateSendOrAdjust(t *testing.T) {
	for _, operation := range []string{"create", "activation", "reset", "nickname", "credit"} {
		t.Run(operation, func(t *testing.T) {
			e, box, access, admin := k9Env(t)
			uid := admin["user_id"].(string)
			k9SeedPending(t, e, "queued-pending", "queued-pending@example.invalid")
			ctx := context.Background()
			tx, err := e.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			var pid int
			if err = tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			method, path, body := "POST", "/api/v1/admin/users", map[string]any{"email": "queued-create@example.invalid", "name": "Queued"}
			switch operation {
			case "activation":
				path = "/api/v1/admin/users/queued-pending/activation-resend"
				body = nil
			case "reset":
				path = "/api/v1/admin/users/" + uid + "/password-reset"
				body = nil
			case "nickname":
				method = "PATCH"
				path = "/api/v1/admin/users/queued-pending"
				body = map[string]any{"name": "Changed", "reason": "queued", "expected_version": 0}
			case "credit":
				path = "/api/v1/admin/tenants/" + admin["tenant_id"].(string) + "/credit-adjustments"
				body = map[string]any{"delta": 1, "reason": "queued", "expected_version": 1, "idempotency_key": "queued"}
			}
			out := make(chan *httptest.ResponseRecorder, 1)
			go func() { out <- doReq(t, e.router, method, path, access, body) }()
			deadline := time.Now().Add(5 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				if k9Count(t, e, `SELECT count(*) FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid))`, pid) > 0 {
					blocked = true
					break
				}
				select {
				case w := <-out:
					t.Fatalf("%s completed before locked actor validation, status=%d", operation, w.Code)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("request did not reach actor lock")
			}
			if _, err = tx.Exec(ctx, `UPDATE users SET token_version=token_version+1 WHERE id=$1`, uid); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case w := <-out:
				if w.Code != 409 {
					t.Fatalf("queued stale %s status=%d", operation, w.Code)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("queued request did not complete")
			}
			if box.count() != 0 || k9Count(t, e, `SELECT count(*) FROM users WHERE email='queued-create@example.invalid'`) > 0 || k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, admin["tenant_id"]) != 1 {
				t.Fatal("revoked queued actor changed state or dispatched")
			}
		})
	}
}
func TestK9PGActivationSocketBudgetAndStrictInput(t *testing.T) {
	e, _, _, _ := k9Env(t)
	for i := 0; i < 22; i++ {
		body, _ := json.Marshal(map[string]string{"token": fmt.Sprintf("unknown-%d", i), "new_password": "StrongPassword123"})
		w := identityRequest(t, e, "/auth/activation/confirm", string(body), i)
		if i < 20 {
			if w.Code != 401 {
				t.Fatalf("invalid activation %d status=%d", i, w.Code)
			}
		} else if w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Fatalf("activation bypassed shared source budget: %d", w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/activation/confirm", strings.NewReader(`{"token":"unknown","new_password":"StrongPassword123"}`))
	req.RemoteAddr = "not-a-socket"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("invalid socket source accepted")
	}
}

func TestK9PGNicknameOnlyAndCreditAuthorization(t *testing.T) {
	e, _, access, admin := k9Env(t)
	ordinary, _, target := mustRegister(t, e.router, "k9-ordinary@example.invalid", "Original")
	uid, tid := target["user_id"].(string), target["tenant_id"].(string)
	path := "/api/v1/admin/users/" + uid
	for _, extra := range []string{"email", "status", "phone", "platform_admin", "password"} {
		body := map[string]any{"name": "Changed", "reason": "profile", "expected_version": 0, extra: "forbidden"}
		adminContractResponse(t, doReq(t, e.router, "PATCH", path, access, body), 400)
	}
	adminContractResponse(t, doReq(t, e.router, "PATCH", path, access, map[string]any{"name": "Updated nickname", "reason": "support correction", "expected_version": 0}), 200)
	adminContractResponse(t, doReq(t, e.router, "PATCH", path, access, map[string]any{"name": "Stale", "reason": "stale", "expected_version": 0}), 409)
	if k9Count(t, e, `SELECT count(*) FROM users WHERE id=$1 AND name='Updated nickname' AND row_version=1 AND token_version=0 AND status='active' AND email='k9-ordinary@example.invalid'`, uid) != 1 {
		t.Fatal("nickname changed identity or failed CAS")
	}
	adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/auth/me", ordinary, nil), 200)
	body := map[string]any{"delta": 1, "reason": "unauthorized", "idempotency_key": "no-permission", "expected_version": 1}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/tenants/"+tid+"/credit-adjustments", ordinary, body), 403)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users", ordinary, map[string]string{"email": "illegal@example.invalid", "name": "Illegal"}), 403)
	for _, delta := range []int{0, 2147483648, -2147483648} {
		body["delta"] = delta
		adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/tenants/"+tid+"/credit-adjustments", access, body), 400)
	}
	_ = admin
}
func TestK9PGSameKeyConcurrentReplayAndNonAdminBalanceVersion(t *testing.T) {
	e, _, access, admin := k9Env(t)
	tid := admin["tenant_id"].(string)
	path := "/api/v1/admin/tenants/" + tid + "/credit-adjustments"
	// A non-admin balance mutation must advance the exact version used by the
	// adjustment UI, including K4's direct transactional balance writes.
	before := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
	k9Exec(t, e, `UPDATE report_credits SET balance=balance+2 WHERE tenant_id=$1`, tid)
	if k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid) != before+1 {
		t.Fatal("nonadmin writer left stale CAS version")
	}
	body := map[string]any{"delta": 2, "reason": "same intent", "idempotency_key": "concurrent-same", "expected_version": before + 1}
	out := make(chan *httptest.ResponseRecorder, 4)
	for i := 0; i < 4; i++ {
		go func() { out <- doReq(t, e.router, "POST", path, access, body) }()
	}
	var first map[string]any
	for i := 0; i < 4; i++ {
		r := adminContractResponse(t, <-out, 200)
		if first == nil {
			first = r
		} else if !reflect.DeepEqual(first, r) {
			t.Fatal("concurrent replay changed original result")
		}
	}
	e.rebuild()
	r := adminContractResponse(t, doReq(t, e.router, "POST", path, access, body), 200)
	if !reflect.DeepEqual(first, r) {
		t.Fatal("reconstructed retry changed original result")
	}
	if k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, tid) != 5 || k9Count(t, e, `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND idempotency_key='concurrent-same'`, tid) != 1 || k9Count(t, e, `SELECT count(*) FROM audit_logs WHERE tenant_id=$1 AND action='credit.adjustment'`, tid) != 1 {
		t.Fatal("duplicate replay changed ledger/audit/balance")
	}
}

func TestK9PGActivationRejectsInvalidBeforeKDFAndSharesWorkCeiling(t *testing.T) {
	e, box, access, _ := k9Env(t)
	k9SeedPending(t, e, "kdf-pending", "kdf-pending@example.invalid")
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/admin/users/kdf-pending/activation-resend", access, nil), 202)
	value := box.delivered(t)
	observer := &passwordHashObserver{base: rand.Reader}
	rand.Reader = observer
	defer func() { rand.Reader = observer.base }()
	for i := 0; i < 20; i++ {
		body, _ := json.Marshal(map[string]string{"token": fmt.Sprintf("invalid-%d", i), "new_password": "StrongPassword123"})
		w := identityRequest(t, e, "/auth/activation/confirm", string(body), i)
		if w.Code != 401 {
			t.Fatalf("invalid status=%d", w.Code)
		}
	}
	if observer.calls.Load() != 0 {
		t.Fatal("invalid activation entered Argon2")
	}
	body, _ := json.Marshal(map[string]string{"token": value, "new_password": "StrongPassword123"})
	if w := identityRequest(t, e, "/auth/activation/confirm", string(body), 99); w.Code != 429 {
		t.Fatal("known token bypassed exhausted socket")
	}
	// A fresh socket and valid pending target is a positive KDF control.
	w := doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", map[string]string{"token": value, "new_password": "StrongPassword123"})
	if w.Code != 500 || observer.calls.Load() != 1 {
		t.Fatal("valid activation did not enter observed KDF")
	}
	assertHashBudgetStateUnchanged(t, e, "kdf-pending")
	release := make(chan struct{})
	observer.release = release
	observer.entered = make(chan int64, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	released := false
	defer func() {
		if !released {
			close(release)
		}
		wg.Wait()
	}()
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", map[string]string{"token": value, "new_password": "StrongPassword123"})
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-observer.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("valid activation did not occupy hash slot")
		}
	}
	w = doReq(t, e.router, "POST", "/api/v1/auth/activation/confirm", "", map[string]string{"token": value, "new_password": "StrongPassword123"})
	if w.Code != 429 || observer.calls.Load() != 3 {
		t.Error("third activation bypassed two-slot KDF guard")
	}
	close(release)
	released = true
	wg.Wait()
	assertHashBudgetStateUnchanged(t, e, "kdf-pending")
}
