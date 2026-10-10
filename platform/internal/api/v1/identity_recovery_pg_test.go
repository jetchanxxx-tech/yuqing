package v1_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/testsupport/notificationsandbox"
)

type identitySandbox struct {
	inbox                 notificationsandbox.Inbox
	mu                    sync.Mutex
	indices               []int
	rejected, unavailable bool
}

func (s *identitySandbox) CheckVerification(context.Context, string) error {
	if s.unavailable {
		return errors.New("sandbox unavailable")
	}
	return nil
}
func (s *identitySandbox) accept(to, purpose, payload string) error {
	if !strings.HasSuffix(to, "@example.invalid") && !strings.HasPrefix(to, "1390000") {
		return errors.New("non-sandbox recipient")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejected {
		return errors.New("sandbox rejection")
	}
	n := s.inbox.Accept(to, purpose, payload)
	s.indices = append(s.indices, n)
	return nil
}
func (s *identitySandbox) Send(_ context.Context, to, subject, body string) error {
	return s.accept(to, subject, body)
}

type identitySMS struct{ box *identitySandbox }

func (s identitySMS) CheckVerification(ctx context.Context, purpose string) error {
	return s.box.CheckVerification(ctx, purpose)
}
func (s identitySMS) Send(_ context.Context, to, purpose string, params map[string]string) error {
	return s.box.accept(to, purpose, params["code"])
}
func (s *identitySandbox) delivered(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.indices) == 0 {
		t.Fatal("sandbox has no accepted message")
	}
	i := s.indices[len(s.indices)-1]
	if err := s.inbox.Deliver(i); err != nil {
		t.Fatal(err)
	}
	m, err := s.inbox.Delivered(i)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Payload, "token=") {
		return strings.Split(strings.Split(m.Payload, "token=")[1], "\"")[0]
	}
	return m.Payload
}
func (s *identitySandbox) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.indices) }
func configureIdentity(e *billingActorPGEnv, box *identitySandbox) {
	users := auth.NewPGStore(e.pool)
	e.deps.Auth.EnableUserCenter(users, auth.NewPGVerificationStore(e.pool), identitySMS{box}, box, "https://example.invalid")
}
func identityRequest(t *testing.T, e *billingActorPGEnv, path, body string, forged int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.77:10000"
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", forged))
	req.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", forged))
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func TestIdentityPGHTTPUniformRequestsAndSocketIPGate(t *testing.T) {
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	_, _, u := mustRegister(t, e.router, "gate-known@example.invalid", "Identity gate")
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET phone='13900000701',phone_verified_at=now() WHERE id=$1`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	var accepted string
	for i := 0; i < 20; i++ {
		path := "/auth/phone/send-code"
		body := fmt.Sprintf(`{"phone":"1390000%04d","ip":"198.51.100.%d"}`, 700+i, i+1)
		if i%2 == 0 {
			path = "/auth/password-reset/request"
			body = fmt.Sprintf(`{"email":"gate-unknown-%d@example.invalid","ip":"198.51.100.%d"}`, i, i+1)
		}
		// One known phone among unknown phones/emails; all share the socket gate.
		w := identityRequest(t, e, path, body, i+1)
		if w.Code != 202 {
			t.Fatalf("request %d status=%d", i, w.Code)
		}
		if accepted == "" {
			accepted = w.Body.String()
		}
		if accepted != w.Body.String() {
			t.Fatal("known/unknown response enumerates identity")
		}
	}
	if box.count() != 1 {
		t.Fatalf("expected only the verified known phone to issue, accepted=%d", box.count())
	}
	// A known unused email and unknown phone have new target keys. Their shared
	// socket is exhausted despite distinct body/XFF/X-Real-IP values.
	for i, body := range []string{`{"email":"gate-known@example.invalid","ip":"198.51.100.201"}`, `{"phone":"13900000999","ip":"198.51.100.202"}`} {
		path := "/auth/password-reset/request"
		if i == 1 {
			path = "/auth/phone/send-code"
		}
		w := identityRequest(t, e, path, body, 201+i)
		if w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Fatalf("spoofed IP escaped aggregate gate: %d", w.Code)
		}
	}
	if box.count() != 1 {
		t.Fatal("rate limited target reached supplier")
	}
	var sends, targets, users, tenants int
	for query, out := range map[string]*int{`SELECT sends FROM verification_send_gates WHERE gate_key='ip:192.0.2.77'`: &sends, `SELECT count(*) FROM verification_send_gates WHERE gate_key IN ('target:password_reset:gate-known@example.invalid','target:phone_login:13900000999')`: &targets, `SELECT count(*) FROM users`: &users, `SELECT count(*) FROM tenants`: &tenants} {
		if err := e.pool.QueryRow(context.Background(), query).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	if sends != 20 || targets != 0 || users != 1 || tenants != 1 {
		t.Fatalf("gate/register state incorrect: sends=%d newtargets=%d users=%d tenants=%d", sends, targets, users, tenants)
	}
	e.rebuild()
	configureIdentity(e, box)
	if w := identityRequest(t, e, "/auth/phone/send-code", `{"phone":"13900000998"}`, 250); w.Code != 429 {
		t.Fatal("IP gate lost on service reconstruction")
	}
}

func TestIdentityPGHTTPPublicUnavailableRejectedAndUnknownRemainUniform(t *testing.T) {
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	_, _, u := mustRegister(t, e.router, "uniform-known@example.invalid", "Uniform")
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET phone='13900000731',phone_verified_at=now() WHERE id=$1`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	box.unavailable = true
	for _, body := range []string{`{"email":"uniform-known@example.invalid"}`, `{"email":"uniform-unknown@example.invalid"}`, `{"phone":"13900000731"}`, `{"phone":"13900000732"}`} {
		if w := identityRequest(t, e, "/auth/password-reset/request", body, 1); w.Code != 503 {
			t.Fatalf("global unavailable leaks existence: %d", w.Code)
		}
	}
	box.unavailable = false
	box.rejected = true
	var accepted string
	for _, body := range []string{`{"email":"uniform-known@example.invalid"}`, `{"email":"uniform-unknown@example.invalid"}`, `{"phone":"13900000731"}`, `{"phone":"13900000732"}`} {
		w := identityRequest(t, e, "/auth/password-reset/request", body, 1)
		if w.Code != 202 {
			t.Fatalf("isolated rejection leaks existence: %d", w.Code)
		}
		if accepted == "" {
			accepted = w.Body.String()
		}
		if w.Body.String() != accepted {
			t.Fatal("public acceptance differs")
		}
	}
	var acceptedCredentials int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM verification_tokens WHERE delivery_status='accepted')+(SELECT count(*) FROM sms_verification_codes WHERE delivery_status='accepted')`).Scan(&acceptedCredentials); err != nil {
		t.Fatal(err)
	}
	if acceptedCredentials != 0 || box.count() != 0 {
		t.Fatal("supplier rejection created consumable credential")
	}
}

func TestIdentityPGHTTPRecoveryAndPhoneLoginRevokeAndReconstruct(t *testing.T) {
	for _, mode := range []string{"email", "phone"} {
		t.Run(mode, func(t *testing.T) {
			e := newBillingActorPGEnv(t)
			box := &identitySandbox{}
			configureIdentity(e, box)
			access, refresh, u := mustRegister(t, e.router, "reset-"+mode+"@example.invalid", "Recover")
			if _, err := e.pool.Exec(context.Background(), `UPDATE users SET phone='13900000741',phone_verified_at=now() WHERE id=$1`, u["user_id"]); err != nil {
				t.Fatal(err)
			}
			req := `{"email":"reset-email@example.invalid"}`
			if mode == "phone" {
				req = `{"phone":"13900000741"}`
			}
			if w := identityRequest(t, e, "/auth/password-reset/request", req, 1); w.Code != 202 {
				t.Fatalf("request=%d", w.Code)
			}
			value := box.delivered(t)
			confirm := map[string]string{"token": value, "new_password": "Recovered12345"}
			if mode == "phone" {
				confirm = map[string]string{"phone": "13900000741", "code": value, "new_password": "Recovered12345"}
			}
			confirm["new_password"] = "weak"
			if w := doReq(t, e.router, "POST", "/api/v1/auth/password-reset/confirm", "", confirm); w.Code != 409 {
				t.Fatalf("weak password=%d", w.Code)
			}
			confirm["new_password"] = "Recovered12345"
			adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/password-reset/confirm", "", confirm), 200)
			if w := doReq(t, e.router, "POST", "/api/v1/auth/password-reset/confirm", "", confirm); w.Code == 200 {
				t.Fatal("replay reset succeeded")
			}
			adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/auth/me", access, nil), 401)
			adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh}), 401)
			emailLogin := adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "reset-" + mode + "@example.invalid", "password": "Recovered12345"}), 200)
			if w := identityRequest(t, e, "/auth/phone/send-code", `{"phone":"13900000741"}`, 1); w.Code != 202 {
				t.Fatalf("login request=%d", w.Code)
			}
			loginCode := box.delivered(t)
			phoneLogin := adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/phone/login", "", map[string]string{"phone": "13900000741", "code": loginCode}), 200)
			eUser := emailLogin["user"].(map[string]any)
			pUser := phoneLogin["user"].(map[string]any)
			for _, key := range []string{"user_id", "tenant_id", "email", "tenant_status", "plan_code"} {
				if eUser[key] != pUser[key] {
					t.Fatalf("phone principal differs: %s", key)
				}
			}
			adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/phone/login", "", map[string]string{"phone": "13900000741", "code": loginCode}), 401)
		})
	}
}

func TestIdentityPGHTTPInactiveRecoveryCannotActivate(t *testing.T) {
	for _, status := range []string{"pending_activation", "disabled", "closed"} {
		t.Run(status, func(t *testing.T) {
			e := newBillingActorPGEnv(t)
			box := &identitySandbox{}
			configureIdentity(e, box)
			_, _, u := mustRegister(t, e.router, "inactive@example.invalid", "Inactive")
			if w := identityRequest(t, e, "/auth/password-reset/request", `{"email":"inactive@example.invalid"}`, 1); w.Code != 202 {
				t.Fatal(w.Code)
			}
			value := box.delivered(t)
			if _, err := e.pool.Exec(context.Background(), `UPDATE users SET status=$2 WHERE id=$1`, u["user_id"], status); err != nil {
				t.Fatal(err)
			}
			w := doReq(t, e.router, "POST", "/api/v1/auth/password-reset/confirm", "", map[string]string{"token": value, "new_password": "CannotActivate123"})
			if w.Code == 200 {
				t.Fatal("inactive recovery activated account")
			}
			var actual string
			var version int
			if err := e.pool.QueryRow(context.Background(), `SELECT status,token_version FROM users WHERE id=$1`, u["user_id"]).Scan(&actual, &version); err != nil {
				t.Fatal(err)
			}
			if actual != status || version != 0 {
				t.Fatal("recovery mutated inactive identity")
			}
		})
	}
}

func TestIdentityPGHTTPEmailChangeSuspendedTenantUniqueRollback(t *testing.T) {
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	token, _, u := mustRegister(t, e.router, "old-identity@example.invalid", "Change")
	if _, err := e.pool.Exec(context.Background(), `UPDATE tenants SET status='suspended' WHERE id=$1`, u["tenant_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE report_credits SET plan_code='invalid-k7-plan' WHERE tenant_id=$1`, u["tenant_id"]); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/email-change/request", token, map[string]string{"password": "password-123456", "new_email": "claimed@example.invalid"}), 202)
	value := box.delivered(t)
	// Old email still authenticates before confirmation; target is taken later.
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "old-identity@example.invalid", "password": "password-123456"}), 200)
	_, _, other := mustRegister(t, e.router, "claimed@example.invalid", "Other")
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/email-change/confirm", token, map[string]string{"token": value}), 409)
	var old string
	var used bool
	var version int
	if err := e.pool.QueryRow(context.Background(), `SELECT u.email,u.token_version,v.used_at IS NOT NULL FROM users u JOIN verification_tokens v ON v.user_id=u.id WHERE u.id=$1 AND v.purpose='email_change'`, u["user_id"]).Scan(&old, &version, &used); err != nil {
		t.Fatal(err)
	}
	if old != "old-identity@example.invalid" || version != 0 || used {
		t.Fatal("target collision consumed credential or mutated identity")
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET email='moved@example.invalid' WHERE id=$1`, other["user_id"]); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/email-change/confirm", token, map[string]string{"token": value}), 200)
	adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/auth/me", token, nil), 401)
	if box.count() != 1 {
		t.Fatal("notice sent inside identity transaction/request")
	}
	if err := e.deps.Auth.DispatchIdentityNotices(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	if box.count() != 2 {
		t.Fatal("old address notice not sent after commit")
	}
}

func TestIdentityPGHTTPQueuedEmailRequestRetainsOriginalActorVersion(t *testing.T) {
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	token, _, u := mustRegister(t, e.router, "queued-identity@example.invalid", "Queued")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doReq(t, e.router, "POST", "/api/v1/user/email-change/request", token, map[string]string{"password": "password-123456", "new_email": "queued-target@example.invalid"})
	}()
	for {
		var blocked bool
		if err = e.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users WHERE id=$1 FOR UPDATE%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-done:
			t.Fatal("request did not wait for user lock")
		case <-ctx.Done():
			t.Fatal("request never reached lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET token_version=token_version+1 WHERE id=$1`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code == 202 {
			t.Fatal("queued request adopted fresh actor version")
		}
	case <-ctx.Done():
		t.Fatal("queued request timed out")
	}
	if box.count() != 0 {
		t.Fatal("revoked actor issued a credential")
	}
}
