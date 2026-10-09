package v1_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/platform/auth"
)

// These tests catch missing account administration, stale status writes,
// privilege confusion and credential revocation through the real HTTP router.
func adminContractToken(t *testing.T) string {
	t.Helper()
	return issueToken(t, auth.Principal{
		UserID: "u_contract", TenantID: "t_contract", Roles: []string{"platform_admin"},
	})
}

func adminContractResponse(t *testing.T, w *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if w.Code != want {
		t.Fatalf("HTTP status = %d, want %d; body: %s", w.Code, want, w.Body.String())
	}
	body := decodeBody(t, w)
	if want >= 400 {
		for _, key := range []string{"code", "message", "request_id"} {
			if value, ok := body[key].(string); !ok || value == "" {
				t.Errorf("error envelope %s is missing: %v", key, body)
			}
		}
	}
	return body
}

func adminContractItems(t *testing.T, body map[string]any) []any {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items must be a non-null array: %v", body)
	}
	return items
}

func adminContractLogin(t *testing.T, r *gin.Engine, email string) (string, string) {
	t.Helper()
	body := adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": email, "password": "password-123456",
	}), http.StatusOK)
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatal("login must return a token pair")
	}
	return access, refresh
}

func adminContractAssertRevoked(t *testing.T, r *gin.Engine, access, refresh string) {
	t.Helper()
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/auth/me", access, nil), http.StatusUnauthorized)
	adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
		"refresh_token": refresh,
	}), http.StatusUnauthorized)
}

func TestContract_adminUsers_listPagesStableMaskedAccounts(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	store := contractFixture(t).store
	ctx := context.Background()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	verified := created.Add(time.Minute)
	for _, id := range []string{"u_page_c", "u_page_a", "u_page_b"} {
		if err := store.CreateUser(ctx, auth.User{
			ID: id, Email: id + "@example.com", Name: "分页用户", PasswordHash: "private-password-hash",
			Status: "active", Phone: "13800000011", CreatedAt: created, EmailVerifiedAt: &verified,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateMember(ctx, auth.Member{TenantID: "t_contract", UserID: id, Role: "analyst"}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	for page, wantID := range []string{"u_page_a", "u_page_b", "u_page_c"} {
		path := "/api/v1/admin/users?q=u_page_&verified=email&page=" + strconv.Itoa(page+1) + "&page_size=1"
		body := adminContractResponse(t, doReq(t, r, http.MethodGet, path, admin, nil), http.StatusOK)
		if body["total"] != float64(3) || body["page"] != float64(page+1) || body["page_size"] != float64(1) {
			t.Errorf("pagination metadata = %v", body)
		}
		items := adminContractItems(t, body)
		if len(items) != 1 {
			t.Fatalf("page has %d items, want 1", len(items))
		}
		row, ok := items[0].(map[string]any)
		if !ok {
			t.Fatal("user item must be an object")
		}
		if row["id"] != wantID || row["phone_masked"] != "138****0011" || row["email_verified"] != true ||
			row["phone_verified"] != false || row["tenant_count"] != float64(1) || row["row_version"] != float64(0) {
			t.Errorf("user row = %v", row)
		}
		created, ok := row["created_at"].(string)
		if !ok {
			t.Fatalf("created_at must be a RFC3339 string: %v", row)
		}
		if _, err := time.Parse(time.RFC3339, created); err != nil {
			t.Errorf("created_at must be RFC3339: %v", row)
		}
		if row["last_login_at"] != nil {
			t.Errorf("never logged-in account last_login_at must be null: %v", row)
		}
		if _, ok := row["platform_roles"].([]any); !ok {
			t.Errorf("platform_roles must be a non-null array: %v", row)
		}
		for _, secret := range []string{"password_hash", "token", "access_token", "refresh_token", "phone"} {
			if _, exists := row[secret]; exists {
				t.Errorf("admin user row exposed %s", secret)
			}
		}
		if strings.Contains(doReq(t, r, http.MethodGet, path, admin, nil).Body.String(), "13800000011") {
			t.Error("admin list exposed complete phone")
		}
		ids = append(ids, row["id"].(string))
	}
	if !reflect.DeepEqual(ids, []string{"u_page_a", "u_page_b", "u_page_c"}) {
		t.Fatalf("equal-time pagination duplicated or reordered accounts: %v", ids)
	}
	empty := adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users?q=no-such-account", admin, nil), http.StatusOK)
	if len(adminContractItems(t, empty)) != 0 || empty["total"] != float64(0) {
		t.Errorf("empty account list = %v", empty)
	}
}

func TestContract_adminUsers_disableEnableCASRevokesBothTokenKinds(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	access, refresh, user := mustRegister(t, r, "status-target@example.com", "状态用户")
	id := user["user_id"].(string)
	path := "/api/v1/admin/users/" + id
	disabled := adminContractResponse(t, doReq(t, r, http.MethodPost, path+"/disable", admin, map[string]any{
		"reason": "客服确认账号停用", "expected_version": 0,
	}), http.StatusOK)
	if disabled["id"] != id || disabled["status"] != "disabled" || disabled["row_version"] != float64(1) {
		t.Errorf("disable response = %v", disabled)
	}
	adminContractAssertRevoked(t, r, access, refresh)
	adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "status-target@example.com", "password": "password-123456",
	}), http.StatusUnauthorized)
	adminContractResponse(t, doReq(t, r, http.MethodPost, path+"/enable", admin, map[string]any{
		"reason": "旧列表恢复请求", "expected_version": 0,
	}), http.StatusConflict)
	enabled := adminContractResponse(t, doReq(t, r, http.MethodPost, path+"/enable", admin, map[string]any{
		"reason": "客服确认账号恢复", "expected_version": 1,
	}), http.StatusOK)
	if enabled["status"] != "active" || enabled["row_version"] != float64(2) {
		t.Errorf("enable response = %v", enabled)
	}
	adminContractAssertRevoked(t, r, access, refresh)
	newAccess, _ := adminContractLogin(t, r, "status-target@example.com")
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/auth/me", newAccess, nil), http.StatusOK)
	detail := adminContractResponse(t, doReq(t, r, http.MethodGet, path, admin, nil), http.StatusOK)
	if detail["status"] != "active" || detail["row_version"] != float64(2) {
		t.Errorf("detail after CAS operations = %v", detail)
	}
	if logs, ok := detail["audit_logs"].([]any); !ok || len(logs) != 2 {
		t.Errorf("only successful status changes must be audited: %v", detail["audit_logs"])
	}
}

func TestContract_adminUsers_platformRoleIsIndependentAndRevocable(t *testing.T) {
	r, deps := newContractEnv(t)
	admin := adminContractToken(t)
	access, refresh, user := mustRegister(t, r, "role-target@example.com", "角色用户")
	id := user["user_id"].(string)
	path := "/api/v1/admin/users/" + id
	grant := adminContractResponse(t, doReq(t, r, http.MethodPut, path+"/platform-role", admin, map[string]any{
		"platform_admin": true, "reason": "平台运营职责交接", "expected_version": 0,
	}), http.StatusOK)
	if !reflect.DeepEqual(grant["platform_roles"], []any{"platform_admin"}) || grant["row_version"] != float64(1) {
		t.Errorf("platform role grant = %v", grant)
	}
	adminContractAssertRevoked(t, r, access, refresh)
	detail := adminContractResponse(t, doReq(t, r, http.MethodGet, path, admin, nil), http.StatusOK)
	memberships, ok := detail["memberships"].([]any)
	if !ok || len(memberships) != 1 {
		t.Fatalf("user detail memberships = %v", detail)
	}
	membership := memberships[0].(map[string]any)
	if membership["tenant_id"] != user["tenant_id"] || membership["role"] != "tenant_admin" || membership["row_version"] != float64(0) {
		t.Errorf("platform grant changed member role: %v", membership)
	}
	freshAccess, freshRefresh := adminContractLogin(t, r, "role-target@example.com")
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users", freshAccess, nil), http.StatusOK)
	revoke := adminContractResponse(t, doReq(t, r, http.MethodPut, path+"/platform-role", admin, map[string]any{
		"platform_admin": false, "reason": "平台职责撤销", "expected_version": 1,
	}), http.StatusOK)
	if roles, ok := revoke["platform_roles"].([]any); !ok || len(roles) != 0 {
		t.Errorf("platform role revoke = %v", revoke)
	}
	adminContractAssertRevoked(t, r, freshAccess, freshRefresh)
	deps.Auth.SetBootstrapAdminEmail("role-target@example.com")
	newAccess, _ := adminContractLogin(t, r, "role-target@example.com")
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users", newAccess, nil), http.StatusForbidden)
}

func TestContract_adminUsers_lastActivePlatformAdminCannotBeRemoved(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	for _, tc := range []struct {
		method string
		path   string
		body   map[string]any
	}{
		{http.MethodPost, "/api/v1/admin/users/u_contract_admin/disable", map[string]any{"reason": "尝试停用最后管理员", "expected_version": 0}},
		{http.MethodPut, "/api/v1/admin/users/u_contract_admin/platform-role", map[string]any{"platform_admin": false, "reason": "尝试撤销最后管理员", "expected_version": 0}},
	} {
		adminContractResponse(t, doReq(t, r, tc.method, tc.path, admin, tc.body), http.StatusConflict)
	}
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users", admin, nil), http.StatusOK)
}

func TestContract_adminUsers_onlyPlatformAdminsCanManageAccounts(t *testing.T) {
	r, deps := newContractEnv(t)
	adminContractToken(t)
	for _, role := range []string{"tenant_admin", "analyst", "viewer"} {
		token := issueToken(t, auth.Principal{UserID: "u_contract", TenantID: "t_contract", Roles: []string{role}})
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/admin/users"},
			{http.MethodGet, "/api/v1/admin/users/u_contract_admin"},
			{http.MethodPost, "/api/v1/admin/users/u_contract_admin/disable"},
			{http.MethodPut, "/api/v1/admin/users/u_contract_admin/platform-role"},
		} {
			adminContractResponse(t, doReq(t, r, tc.method, tc.path, token, map[string]any{
				"reason": "越权操作", "expected_version": 0, "platform_admin": true,
			}), http.StatusForbidden)
		}
	}
	_, key, err := deps.APIKey.CreateKey(context.Background(), "t_contract", "account-admin-boundary", nil)
	if err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users", key, nil), http.StatusForbidden)
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users", "", nil), http.StatusUnauthorized)
}

func TestContract_adminUsers_rejectsInvalidPaginationAndMutationInput(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	for _, query := range []string{"page=0", "page=x", "page_size=0", "page_size=101", "status=invented", "platform_role=tenant_admin", "verified=unknown"} {
		adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users?"+query, admin, nil), http.StatusBadRequest)
	}
	for _, body := range []any{nil, map[string]any{"reason": "缺少版本"}, map[string]any{"expected_version": 0}, map[string]any{"reason": "  ", "expected_version": 0}, map[string]any{"reason": "负版本", "expected_version": -1}} {
		adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/admin/users/u_contract_admin/disable", admin, body), http.StatusBadRequest)
	}
	for _, body := range []any{map[string]any{"reason": "缺少角色意图", "expected_version": 0}, map[string]any{"platform_admin": "yes", "reason": "非法布尔值", "expected_version": 0}} {
		adminContractResponse(t, doReq(t, r, http.MethodPut, "/api/v1/admin/users/u_contract_admin/platform-role", admin, body), http.StatusBadRequest)
	}
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users/not-existing", admin, nil), http.StatusNotFound)
}

func TestContract_adminUsers_enableCannotBypassActivationOrClosure(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	for _, status := range []string{"pending_activation", "closure_pending", "closed"} {
		id := "u_state_" + status
		if err := contractFixture(t).store.CreateUser(context.Background(), auth.User{
			ID: id, Email: id + "@example.com", Name: "状态机账号", PasswordHash: "private-hash", Status: status,
		}); err != nil {
			t.Fatal(err)
		}
		adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/admin/users/"+id+"/enable", admin, map[string]any{
			"reason": "启用不应绕过身份状态机", "expected_version": 0,
		}), http.StatusConflict)
		u, err := contractFixture(t).store.GetByID(context.Background(), id)
		if err != nil || u.Status != status || u.RowVersion != 0 || u.TokenVersion != 0 {
			t.Errorf("invalid enable changed account status/version: %v (%v)", u, err)
		}
	}
}
