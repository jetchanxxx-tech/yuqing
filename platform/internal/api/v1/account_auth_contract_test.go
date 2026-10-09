package v1_test

import (
	"context"
	"net/http"
	"testing"
)

func TestContract_accountAuth_suspensionRejectsExistingBusinessToken(t *testing.T) {
	r, deps := newContractEnv(t)
	token, _, user := mustRegister(t, r, "suspended-business@example.com", "业务用户")
	tenantID, _ := user["tenant_id"].(string)
	if tenantID == "" {
		t.Fatal("register response has no tenant ID")
	}

	before := doReq(t, r, http.MethodGet, "/api/v1/analyses", token, nil)
	if before.Code != http.StatusOK {
		t.Fatalf("active tenant GET /analyses status = %d, want 200\nbody: %s", before.Code, before.Body.String())
	}
	if err := deps.Tenant.Suspend(context.Background(), tenantID); err != nil {
		t.Fatalf("suspend registered tenant: %v", err)
	}

	w := doReq(t, r, http.MethodGet, "/api/v1/analyses", token, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("existing token after tenant suspension GET /analyses status = %d, want 403\nbody: %s", w.Code, w.Body.String())
	}
}

func TestContract_accountAuth_suspendedTenantCanReadOwnAccount(t *testing.T) {
	r, deps := newContractEnv(t)
	_, _, user := mustRegister(t, r, "suspended-account@example.com", "账户用户")
	tenantID, _ := user["tenant_id"].(string)
	if tenantID == "" {
		t.Fatal("register response has no tenant ID")
	}
	if err := deps.Tenant.Suspend(context.Background(), tenantID); err != nil {
		t.Fatalf("suspend registered tenant: %v", err)
	}

	login := doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "suspended-account@example.com", "password": "password-123456",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("suspended tenant login status = %d, want 200\nbody: %s", login.Code, login.Body.String())
	}
	loginBody := decodeBody(t, login)
	token, _ := loginBody["access_token"].(string)
	if token == "" {
		t.Fatal("login response has no access token")
	}
	loginUser, _ := loginBody["user"].(map[string]any)
	if loginUser["tenant_status"] != "suspended" {
		t.Fatalf("login tenant_status = %v, want suspended", loginUser["tenant_status"])
	}

	for _, tc := range []struct {
		path    string
		idField string
	}{
		{"/api/v1/auth/me", "user_id"},
		{"/api/v1/user/profile", "id"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := doReq(t, r, http.MethodGet, tc.path, token, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("suspended tenant GET %s status = %d, want 200\nbody: %s", tc.path, w.Code, w.Body.String())
			}
			body := decodeBody(t, w)
			if body[tc.idField] != user["user_id"] || body["email"] != "suspended-account@example.com" {
				t.Errorf("GET %s returned another account: %v", tc.path, body)
			}
		})
	}
}

func TestContract_accountAuth_suspendedBootstrapAdminCanResumeTenant(t *testing.T) {
	r, deps := newContractEnv(t)
	deps.Auth.SetBootstrapAdminEmail("admin@example.com")
	token, _, user := mustRegister(t, r, "admin@example.com", "平台管理员")
	tenantID, _ := user["tenant_id"].(string)
	if tenantID == "" {
		t.Fatal("register response has no tenant ID")
	}
	before := doReq(t, r, http.MethodGet, "/api/v1/admin/tenants", token, nil)
	if before.Code != http.StatusOK {
		t.Fatalf("bootstrap admin GET /admin/tenants status = %d, want 200\nbody: %s", before.Code, before.Body.String())
	}
	if err := deps.Tenant.Suspend(context.Background(), tenantID); err != nil {
		t.Fatalf("suspend administrator tenant: %v", err)
	}

	login := doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "admin@example.com", "password": "password-123456",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("suspended administrator login status = %d, want 200\nbody: %s", login.Code, login.Body.String())
	}
	loginBody := decodeBody(t, login)
	token, _ = loginBody["access_token"].(string)
	if token == "" {
		t.Fatal("administrator login response has no access token")
	}
	loginUser, _ := loginBody["user"].(map[string]any)
	if loginUser["tenant_status"] != "suspended" {
		t.Fatalf("administrator login tenant_status = %v, want suspended", loginUser["tenant_status"])
	}

	w := doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/resume", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("suspended administrator POST /admin/tenants/:id/resume status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["id"] != tenantID || body["status"] != "active" {
		t.Errorf("resume response = %v, want registered tenant active", body)
	}
}

func TestContract_accountAuth_APIKeyCannotReadAccountOrAdmin(t *testing.T) {
	r, deps := newContractEnv(t)
	_, _, user := mustRegister(t, r, "key-owner@example.com", "密钥用户")
	tenantID, _ := user["tenant_id"].(string)
	if tenantID == "" {
		t.Fatal("register response has no tenant ID")
	}
	_, rawKey, err := deps.APIKey.CreateKey(context.Background(), tenantID, "account-boundary", nil)
	if err != nil {
		t.Fatalf("create tenant API key: %v", err)
	}
	before := doReq(t, r, http.MethodGet, "/api/v1/analyses", rawKey, nil)
	if before.Code != http.StatusOK {
		t.Fatalf("active API key GET /analyses status = %d, want 200\nbody: %s", before.Code, before.Body.String())
	}

	for _, path := range []string{
		"/api/v1/auth/me",
		"/api/v1/user/profile",
		"/api/v1/admin/tenants",
	} {
		t.Run(path, func(t *testing.T) {
			w := doReq(t, r, http.MethodGet, path, rawKey, nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("API key GET %s status = %d, want 403\nbody: %s", path, w.Code, w.Body.String())
			}
		})
	}
}

func TestContract_accountAuth_paymentCallbackIgnoresUserCredentials(t *testing.T) {
	r, deps := newContractEnv(t)
	_, _, user := mustRegister(t, r, "callback-account@example.com", "回调用户")
	tenantID, _ := user["tenant_id"].(string)
	if tenantID == "" {
		t.Fatal("register response has no tenant ID")
	}
	if err := deps.Tenant.Suspend(context.Background(), tenantID); err != nil {
		t.Fatalf("suspend callback tenant: %v", err)
	}
	p, pair, err := deps.Auth.Login(context.Background(), "callback-account@example.com", "password-123456")
	if err != nil {
		t.Fatalf("login callback account: %v", err)
	}
	if p.TenantStatus != "suspended" {
		t.Fatalf("callback principal tenant status = %q, want suspended", p.TenantStatus)
	}

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"no authorization", ""},
		{"invalid authorization", "not-a-valid-access-token"},
		{"suspended authorization", pair.AccessToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, r, http.MethodPost, "/api/v1/callbacks/payment/alipay", tc.token, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("unsigned Alipay callback status = %d, want 400\nbody: %s", w.Code, w.Body.String())
			}
		})
	}
}
