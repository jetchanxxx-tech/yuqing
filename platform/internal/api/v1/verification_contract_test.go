package v1_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/platform/auth"
)

type verificationSandboxMail struct {
	body  string
	calls int
}

func (m *verificationSandboxMail) Send(_ context.Context, _, _, body string) error {
	m.body = body
	m.calls++
	return nil
}

type verificationSandboxSMS struct{ code, template string }

func (m *verificationSandboxSMS) Send(_ context.Context, _, template string, params map[string]string) error {
	m.code = params["code"]
	m.template = template
	return nil
}

func TestVerificationHTTPConfiguredOriginAndCredentialPrivacy(t *testing.T) {
	r, s := newContractEnv(t)
	token := issueToken(t, principal("tenant_admin"))
	fixture := contractFixture(t)
	mail := &verificationSandboxMail{}
	s.Auth.EnableUserCenter(fixture.store, auth.NewMemoryVerificationStore(), nil, mail, "")
	req := httptest.NewRequest(http.MethodPost, "http://attacker.invalid/api/v1/auth/send-verification-email", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 503 || mail.calls != 0 {
		t.Fatalf("missing configured origin accepted forged Host: %d", w.Code)
	}
	s.Auth.EnableUserCenter(fixture.store, auth.NewMemoryVerificationStore(), nil, mail, "https://trusted.example.com")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || mail.calls != 1 || !strings.Contains(mail.body, "https://trusted.example.com/verify-email?token=") || strings.Contains(mail.body, "attacker.invalid") {
		t.Fatalf("configured origin not authoritative: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "token=") {
		t.Fatal("response leaked bearer credential")
	}
	w = doReq(t, r, "POST", "/api/v1/auth/send-verification-email", token, nil)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("send gate omitted retry advice")
	}
}
func TestVerificationHTTPPhoneBindingRevokesOriginalSession(t *testing.T) {
	r, s := newContractEnv(t)
	token := issueToken(t, principal("tenant_admin"))
	fixture := contractFixture(t)
	sms := &verificationSandboxSMS{}
	s.Auth.EnableUserCenter(fixture.store, auth.NewMemoryVerificationStore(), sms, nil, "https://trusted.example.com")
	sent := doReq(t, r, "POST", "/api/v1/user/phone/send-code", token, map[string]string{"phone": "13800138000"})
	if sent.Code != 200 || sms.template != "SMS_BIND_PHONE" {
		t.Fatalf("purpose-specific sandbox send failed: %d", sent.Code)
	}
	bound := doReq(t, r, "POST", "/api/v1/user/phone/bind", token, map[string]string{"phone": "13800138000", "code": sms.code})
	if bound.Code != 200 || !strings.Contains(bound.Body.String(), `"requires_relogin":true`) {
		t.Fatalf("bind did not require relogin: %d", bound.Code)
	}
	if after := doReq(t, r, "GET", "/api/v1/user/profile", token, nil); after.Code != 401 {
		t.Fatalf("old JWT survived identity change: %d", after.Code)
	}
}
