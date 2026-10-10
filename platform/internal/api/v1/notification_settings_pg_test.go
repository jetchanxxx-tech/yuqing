package v1_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// These requests authenticate before waiting for the shared administration
// boundary. Current role/status/original JWT must still authorize the commit.
func TestNotificationSettingsPGQueuedActorCannotCommit(t *testing.T) {
	for _, kind := range []string{"version", "role", "disabled", "unchanged"} {
		t.Run(kind, func(t *testing.T) {
			e := newBillingActorPGEnv(t)
			token, _, u := mustRegister(t, e.router, "queued-notify-"+kind+"@example.invalid", "queued admin")
			if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, u["user_id"]); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			blocker, err := e.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(741914)`); err != nil {
				t.Fatal(err)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				done <- doReq(t, e.router, http.MethodPut, "/api/v1/admin/settings", token, map[string]any{"resend_api_key": "queued-isolated-key"})
			}()
			for {
				var waiting bool
				if err = e.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock%' AND pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case <-done:
					t.Fatal("configuration committed before administration boundary")
				case <-ctx.Done():
					t.Fatal("configuration never reached actor lock")
				case <-time.After(10 * time.Millisecond):
				}
			}
			switch kind {
			case "version":
				_, err = blocker.Exec(ctx, `UPDATE users SET token_version=token_version+1 WHERE id=$1`, u["user_id"])
			case "role":
				_, err = blocker.Exec(ctx, `DELETE FROM platform_user_roles WHERE user_id=$1`, u["user_id"])
			case "disabled":
				_, err = blocker.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, u["user_id"])
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case w := <-done:
				want := http.StatusConflict
				if kind == "unchanged" {
					want = http.StatusOK
				}
				adminContractResponse(t, w, want)
			case <-ctx.Done():
				t.Fatal("queued settings timed out")
			}
			var count int
			if err = e.pool.QueryRow(ctx, `SELECT count(*) FROM platform_settings WHERE key='resend_api_key' AND value='queued-isolated-key'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "unchanged" {
				want = 1
			}
			if count != want {
				t.Fatal("revoked actor changed configuration")
			}
		})
	}
}
func TestNotificationSettingsPGMaskPreserveAndAuditAtomicity(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, u := mustRegister(t, e.router, "mask-notify@example.invalid", "admin")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/settings"
	secret := "isolated-original-key"
	saved := adminContractResponse(t, doReq(t, e.router, http.MethodPut, path, token, map[string]any{"resend_api_key": secret}), http.StatusOK)
	masked := saved["settings"].(map[string]any)["resend_api_key"]
	if masked == secret {
		t.Fatal("no server-side secret redaction")
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodPut, path, token, map[string]any{"resend_api_key": masked, "email_from_name": "sandbox"}), http.StatusOK)
	raw, _ := e.deps.Settings.Get(context.Background(), "resend_api_key")
	if raw != secret {
		t.Fatal("returned mask overwrote usable secret")
	}
	var details string
	if err := e.pool.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(details_json)::text,'[]') FROM audit_logs WHERE action='settings.update'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if details == "[]" || strings.Contains(details, secret) {
		t.Fatal("configuration audit missing or contains secret")
	}
	if _, err := e.pool.Exec(context.Background(), `CREATE FUNCTION reject_notification_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='settings.update' THEN RAISE EXCEPTION 'isolated audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_notification_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION reject_notification_audit()`); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodPut, path, token, map[string]any{"resend_api_key": "isolated-replacement"}), http.StatusInternalServerError)
	raw, _ = e.deps.Settings.Get(context.Background(), "resend_api_key")
	if raw != secret {
		t.Fatal("audit failure did not roll back configuration")
	}
}

func TestNotificationSettingsPGUnauthorizedReadAndRevokedRole(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, u := mustRegister(t, e.router, "denied-notify@example.invalid", "reader")
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/admin/settings", "", nil), http.StatusUnauthorized)
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/admin/settings", token, nil), http.StatusForbidden)
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodPut, "/api/v1/admin/settings", token, map[string]any{"resend_api_key": "fake-private-config", "smtp_password": "", "sms_access_key_id": "fake-identifier"}), http.StatusOK)
	data := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/admin/settings", token, nil), http.StatusOK)["settings"].(map[string]any)
	if data["resend_api_key"] != "********" || data["sms_access_key_id"] != "********" || data["smtp_password"] != "" {
		t.Fatal("configured state lost or secret exposed")
	}
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM platform_user_roles WHERE user_id=$1`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/admin/settings", token, nil), http.StatusForbidden)
}

func TestNotificationSettingsPGSharedPaymentJSONPreservesTypesAndMasks(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, u := mustRegister(t, e.router, "structured-settings@example.invalid", "admin")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, u["user_id"]); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/settings"
	original := `{"enabled":true,"private_key":"fake-private","api_v3_key":"fake-v3","sign_cert_pfx":"fake-pfx","sign_cert_password":"fake-pass","appid":"sandbox-app","attempts":12,"nested":{"enabled":false,"private_key":"fake-nested","items":[{"secret":"fake-array","weight":2.5}]}}`
	data := adminContractResponse(t, doReq(t, e.router, http.MethodPut, path, token, map[string]any{"payment_wechat": original}), http.StatusOK)["settings"].(map[string]any)
	var masked map[string]any
	if err := json.Unmarshal([]byte(data["payment_wechat"].(string)), &masked); err != nil {
		t.Fatal("redaction destroyed structured configuration")
	}
	if masked["enabled"] != true || masked["appid"] != "sandbox-app" {
		t.Fatal("nonsecret typed configuration changed")
	}
	for _, key := range []string{"private_key", "api_v3_key", "sign_cert_pfx", "sign_cert_password"} {
		if masked[key] != "********" {
			t.Fatalf("unredacted credential field %s", key)
		}
	}
	nested := masked["nested"].(map[string]any)
	if masked["attempts"] != float64(12) || nested["enabled"] != false || nested["private_key"] != "********" {
		t.Fatal("nested typed config lost or secret unmasked")
	}
	item := nested["items"].([]any)[0].(map[string]any)
	if item["secret"] != "********" || item["weight"] != 2.5 {
		t.Fatal("array secret or number changed")
	}
	masked["enabled"] = false
	patch, _ := json.Marshal(masked)
	adminContractResponse(t, doReq(t, e.router, http.MethodPut, path, token, map[string]any{"payment_wechat": string(patch)}), http.StatusOK)
	raw, err := e.deps.Settings.Get(context.Background(), "payment_wechat")
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if json.Unmarshal([]byte(raw), &stored) != nil || stored["enabled"] != false || stored["private_key"] != "fake-private" || stored["api_v3_key"] != "fake-v3" || stored["sign_cert_pfx"] != "fake-pfx" {
		t.Fatal("saving returned masks lost usable structured secrets")
	}
	storedNested := stored["nested"].(map[string]any)
	if storedNested["private_key"] != "fake-nested" || storedNested["items"].([]any)[0].(map[string]any)["secret"] != "fake-array" {
		t.Fatal("nested secret placeholders overwrote credentials")
	}

}
