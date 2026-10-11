package v1_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// This test-only malformed hash enters real VerifyPassword/Argon2, then panics
// on time=0 before allocation. Router Recovery returns500. The positive control
// distinguishes rejection before the KDF from merely swallowing its failure.
const issuanceEntryHash = "$argon2id$v=19$m=8,t=0,p=1$c2FsdA$AQ"

func issuanceBudgetRequest(ctx context.Context, e *billingActorPGEnv, token, path, password, target, peer string, forged int) *httptest.ResponseRecorder {
	body := map[string]string{"password": password, "ip": fmt.Sprintf("203.0.113.%d", forged)}
	if path == "/user/email-change/request" {
		body["new_email"] = target
	} else {
		body["phone"] = target
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1"+path, strings.NewReader(string(raw))).WithContext(ctx)
	req.RemoteAddr = peer
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", forged))
	req.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", forged))
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}
func issuanceEntryFixture(t *testing.T) (*billingActorPGEnv, *identitySandbox, string, any) {
	t.Helper()
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	token, _, user := mustRegister(t, e.router, "issuance-budget@example.invalid", "Issuance budget")
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET password_hash=$2 WHERE id=$1`, user["user_id"], issuanceEntryHash); err != nil {
		t.Fatal(err)
	}
	return e, box, token, user["user_id"]
}
func issuanceRoute(i int) (string, string) {
	if i%2 == 0 {
		return "/user/email-change/request", fmt.Sprintf("issuance-%d@example.invalid", i)
	}
	return "/user/phone/send-code", fmt.Sprintf("1390000%04d", i)
}
func assertIssuanceIdentityUnchanged(t *testing.T, e *billingActorPGEnv, uid any) {
	t.Helper()
	var unchanged bool
	if err := e.pool.QueryRow(context.Background(), `SELECT token_version=0 AND row_version=0 AND email='issuance-budget@example.invalid' AND phone='' AND NOT EXISTS(SELECT 1 FROM verification_tokens WHERE user_id=users.id) AND NOT EXISTS(SELECT 1 FROM sms_verification_codes WHERE user_id=users.id) FROM users WHERE id=$1`, uid).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Fatal("rejected issuance changed identity or created a credential")
	}
}
func TestIdentityIssuancePGSharedSourceAccountBudgetPersistsAcrossRestartAndWindow(t *testing.T) {
	e, box, token, uid := issuanceEntryFixture(t)
	// Prepare a second genuine principal before installing any entropy observer.
	other, _, user := mustRegister(t, e.router, "issuance-other@example.invalid", "Other issuer")
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET password_hash=$2 WHERE id=$1`, user["user_id"], issuanceEntryHash); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 22; i++ {
		if i == 10 {
			e.rebuild()
			configureIdentity(e, box)
		}
		path, target := issuanceRoute(i)
		w := issuanceBudgetRequest(context.Background(), e, token, path, "wrong-current-password", target, "[::ffff:192.0.2.77]:10000", i+1)
		want := 500
		if i >= 20 {
			want = 429
		}
		if w.Code != want {
			t.Errorf("real issuance KDF entry request%d status=%d want=%d", i+1, w.Code, want)
		}
		if i >= 20 {
			retry, _ := strconv.Atoi(w.Header().Get("Retry-After"))
			if retry < 1 || retry > 60 {
				t.Error("bounded Retry-After missing")
			}
		}
	}
	// Rotate both targets and forged headers: only immutable account and actual
	// canonical socket identity may select a KDF budget.
	for _, tc := range []struct{ token, peer string }{{other, "192.0.2.77:10000"}, {token, "192.0.2.88:10000"}} {
		if w := issuanceBudgetRequest(context.Background(), e, tc.token, "/user/email-change/request", "wrong", "fresh@example.invalid", tc.peer, 99); w.Code != 429 {
			t.Errorf("exhausted source/account bypass status=%d", w.Code)
		}
	}
	if w := issuanceBudgetRequest(context.Background(), e, other, "/user/phone/send-code", "wrong", "13900000888", "192.0.2.89:10000", 99); w.Code != 500 {
		t.Error("unexhausted positive KDF-entry control did not reach real verifier")
	}
	var source, account int
	if err := e.pool.QueryRow(context.Background(), `SELECT COALESCE((SELECT sends FROM verification_send_gates WHERE gate_key='confirm:ip:192.0.2.77'),0),COALESCE((SELECT sends FROM verification_send_gates WHERE gate_key=$1),0)`, "confirm:uid:"+uid.(string)).Scan(&source, &account); err != nil {
		t.Fatal(err)
	}
	if source != 20 || account != 20 {
		t.Errorf("durable shared attempts source=%d account=%d want20/20", source, account)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE verification_send_gates SET window_start=now()-interval '61 seconds' WHERE gate_key IN ('confirm:ip:192.0.2.77',$1)`, "confirm:uid:"+uid.(string)); err != nil {
		t.Fatal(err)
	}
	if w := issuanceBudgetRequest(context.Background(), e, token, "/user/email-change/request", "wrong", "window@example.invalid", "192.0.2.77:10000", 99); w.Code != 500 {
		t.Error("new window cannot admit positive KDF-entry control")
	}
	if box.count() != 0 {
		t.Fatal("failed KDF sent a message")
	}
	assertIssuanceIdentityUnchanged(t, e, uid)
}
func TestIdentityIssuancePGRejectsOversizedInvalidPeerAndUnavailableBeforeKDF(t *testing.T) {
	e, box, token, uid := issuanceEntryFixture(t)
	for i := 0; i < 2; i++ {
		path, target := issuanceRoute(i)
		for _, tc := range []struct {
			name, password, peer string
			want                 int
		}{{"oversized", strings.Repeat("x", 1025), "192.0.2.80:10000", 400}, {"missing peer", "wrong", "untrusted", 400}} {
			w := issuanceBudgetRequest(context.Background(), e, token, path, tc.password, target, tc.peer, 1)
			if w.Code != tc.want {
				t.Errorf("%s %s status=%d want=%d", path, tc.name, w.Code, tc.want)
			}
		}
		box.unavailable = true
		if w := issuanceBudgetRequest(context.Background(), e, token, path, "wrong", target, "192.0.2.80:10000", 1); w.Code != 503 {
			t.Errorf("unavailable %s entered verifier: %d", path, w.Code)
		}
		box.unavailable = false
	}
	if box.count() != 0 {
		t.Fatal("cheap rejected request sent a message")
	}
	assertIssuanceIdentityUnchanged(t, e, uid)
}
func TestIdentityIssuancePGSharesLiveConfirmationSlotsThroughCancellation(t *testing.T) {
	e, uid, valid := seedHashBudgetCredential(t)
	token, _, user := mustRegister(t, e.router, "issuance-budget@example.invalid", "Issuance budget")
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET password_hash=$2 WHERE id=$1`, user["user_id"], issuanceEntryHash); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	observer := &passwordHashObserver{base: rand.Reader, entered: make(chan int64, 8), release: release}
	rand.Reader = observer
	var workers sync.WaitGroup
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); workers.Wait(); rand.Reader = observer.base }()
	call := func(ctx context.Context, peer string) <-chan *httptest.ResponseRecorder {
		out := make(chan *httptest.ResponseRecorder, 1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			out <- hashBudgetRequest(ctx, e, map[string]string{"token": valid, "new_password": "Example12345"}, peer, 1)
		}()
		return out
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := call(ctx, "192.0.2.91:1234")
	second := call(context.Background(), "192.0.2.92:1234")
	for i := 0; i < 2; i++ {
		select {
		case <-observer.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("positive reset controls did not occupy live KDF slots")
		}
	}
	cancel()
	e.rebuild()
	configureIdentity(e, &identitySandbox{})
	for i := 0; i < 2; i++ {
		path, target := issuanceRoute(i)
		w := issuanceBudgetRequest(context.Background(), e, token, path, "wrong", target, "192.0.2.93:1234", 1)
		if w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Errorf("live KDF ceiling escaped by %s: %d", path, w.Code)
		}
	}
	if observer.calls.Load() != 2 {
		t.Error("concurrent ceiling admitted extra password hashing")
	}
	once.Do(func() { close(release) })
	<-first
	<-second
	workers.Wait()
	if w := issuanceBudgetRequest(context.Background(), e, token, "/user/email-change/request", "wrong", "after-release@example.invalid", "192.0.2.94:1234", 1); w.Code != 500 {
		t.Error("completed KDF leaked process capacity")
	}
	assertHashBudgetStateUnchanged(t, e, uid)
}
func TestIdentityIssuancePGValidCurrentPasswordStillIssuesSandboxCredentials(t *testing.T) {
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	token, _, user := mustRegister(t, e.router, "issuance-budget@example.invalid", "Issuance budget")
	for i := 0; i < 2; i++ {
		path, target := issuanceRoute(i)
		want := 202
		if i == 1 {
			want = 200
		}
		if w := issuanceBudgetRequest(context.Background(), e, token, path, "password-123456", target, "192.0.2.95:1234", 1); w.Code != want {
			t.Fatalf("valid %s no longer issues: %d %s", path, w.Code, w.Body.String())
		}
	}
	if box.count() != 2 {
		t.Fatal("valid issuance did not reach both isolated suppliers")
	}
	var accepted int
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM verification_tokens WHERE user_id=$1 AND purpose='email_change' AND delivery_status='accepted')+(SELECT count(*) FROM sms_verification_codes WHERE user_id=$1 AND purpose='phone_bind' AND delivery_status='accepted')`, user["user_id"]).Scan(&accepted); err != nil {
		t.Fatal(err)
	}
	if accepted != 2 {
		t.Fatal("bounded issuance lost accepted durable credentials")
	}
}
