package v1_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestNotificationSettingsPGSecretsStayServerSide(t *testing.T) {
	e := newBillingActorPGEnv(t)
	_, _, u := mustRegister(t, e.router, "notification-admin@example.invalid", "notification admin")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	token, _ := adminContractLogin(t, e.router, "notification-admin@example.invalid")
	secret := "isolated-resend-credential"
	w := doReq(t, e.router, http.MethodPut, "/api/v1/admin/settings", token, map[string]any{"resend_api_key": secret, "sms_access_key_secret": "isolated-sms-secret", "email_provider": "resend"})
	adminContractResponse(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "isolated-sms-secret") {
		t.Error("settings save returned complete supplier credentials")
	}
	w = doReq(t, e.router, http.MethodGet, "/api/v1/admin/settings", token, nil)
	adminContractResponse(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), secret) {
		t.Error("settings GET returned complete supplier credential")
	}
	raw, err := e.deps.Settings.Get(context.Background(), "resend_api_key")
	if err != nil || raw != secret {
		t.Fatal("internal adapter lost credential")
	}
}
