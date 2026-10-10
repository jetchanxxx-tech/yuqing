package v1_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/platform/auth"
)

func TestIdentityHTTPFirstBindRequiresCurrentPassword(t *testing.T) {
	r, s := newContractEnv(t)
	token := issueToken(t, principal("tenant_admin"))
	sms := &verificationSandboxSMS{}
	s.Auth.EnableUserCenter(contractFixture(t).store, auth.NewMemoryVerificationStore(), sms, nil, "https://example.invalid")
	for _, password := range []string{"", "wrong-password"} {
		w := doReq(t, r, "POST", "/api/v1/user/phone/send-code", token, map[string]string{"phone": "13900000701", "password": password})
		if w.Code != 401 || sms.code != "" {
			t.Fatalf("first binding accepted without current password: status=%d issued=%v", w.Code, sms.code != "")
		}
	}
	good := doReq(t, r, "POST", "/api/v1/user/phone/send-code", token, map[string]string{"phone": "13900000701", "password": "password-123456"})
	if good.Code != 200 || sms.code == "" { t.Fatalf("correct current password must issue: %d", good.Code) }
}

func TestIdentityHTTPForgedIPCannotBypassAggregateSendGate(t *testing.T) {
	r, s := newContractEnv(t)
	_ = issueToken(t, principal("tenant_admin"))
	s.Auth.EnableUserCenter(contractFixture(t).store, auth.NewMemoryVerificationStore(), &verificationSandboxSMS{}, &verificationSandboxMail{}, "https://example.invalid")
	for i := 0; i < 22; i++ {
		path := "/api/v1/auth/phone/send-code"
		body := fmt.Sprintf(`{"phone":"1390000%04d","ip":"198.51.100.%d"}`, i, i+1)
		if i%2 == 1 { path = "/api/v1/auth/password-reset/request"; body = fmt.Sprintf(`{"email":"unknown-%d@example.invalid","ip":"198.51.100.%d"}`, i, i+1) }
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		req.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i+1))
		req.RemoteAddr = "192.0.2.7:43210"
		w := httptest.NewRecorder(); r.ServeHTTP(w, req)
		want := 202; if i >= 20 { want = 429 }
		if w.Code != want || (want == 429 && w.Header().Get("Retry-After") == "") {
			t.Fatalf("socket-IP aggregate across purposes request %d: status=%d want=%d retry=%q", i+1, w.Code, want, w.Header().Get("Retry-After"))
		}
	}
}
