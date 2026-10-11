package v1_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/platform/auth"
)

func TestContract_adminTenants_listsCountsCreationAndCreditPlan(t *testing.T) {
	r, deps := newContractEnv(t)
	admin := adminContractToken(t)
	_, _, user := mustRegister(t, r, "tenant-member@example.com", "团队成员")
	store := contractFixture(t).store
	if err := store.CreateMember(context.Background(), auth.Member{
		TenantID: "t_contract", UserID: user["user_id"].(string), Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	if err := deps.Credits.SetPlanCode(context.Background(), "t_contract", "pro"); err != nil {
		t.Fatal(err)
	}
	body := adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/tenants?page=1&page_size=1", admin, nil), http.StatusOK)
	items := adminContractItems(t, body)
	if len(items) != 1 || body["total"] != float64(2) || body["page"] != float64(1) || body["page_size"] != float64(1) {
		t.Fatalf("tenant pagination = %v", body)
	}
	if !reflect.DeepEqual(body["tenants"], items) {
		t.Errorf("legacy tenants must contain current page items: %v", body)
	}
	row := items[0].(map[string]any)
	if row["id"] != "t_contract" || row["user_count"] != float64(2) || row["effective_plan_code"] != "pro" || row["plan_source"] != "report_credits" || row["row_version"] != float64(0) {
		t.Errorf("tenant must show actual membership count and credit plan: %v", row)
	}
	created, ok := row["created_at"].(string)
	if !ok {
		t.Fatalf("tenant created_at missing: %v", row)
	}
	if _, err := time.Parse(time.RFC3339, created); err != nil {
		t.Errorf("tenant created_at = %q: %v", created, err)
	}
	detail := adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/tenants/t_contract", admin, nil), http.StatusOK)
	if members, ok := detail["members"].([]any); !ok || len(members) != 2 {
		t.Errorf("tenant detail must list existing members: %v", detail)
	}
	if orders, ok := detail["orders"].([]any); !ok || len(orders) != 0 {
		t.Errorf("credit plan must not fabricate paid orders: %v", detail)
	}
}

func TestContract_adminTenants_statusRequiresReasonAndCAS(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	for _, body := range []any{nil, map[string]any{"reason": "缺少版本"}, map[string]any{"expected_version": 0}, map[string]any{"reason": "  ", "expected_version": 0}} {
		adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/t_contract/suspend", admin, body), http.StatusBadRequest)
	}
	suspended := adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/t_contract/suspend", admin, map[string]any{
		"reason": "风控挂起团队", "expected_version": 0,
	}), http.StatusOK)
	if suspended["status"] != "suspended" || suspended["row_version"] != float64(1) {
		t.Errorf("suspend response = %v", suspended)
	}
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/auth/me", admin, nil), http.StatusOK)
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/analyses", admin, nil), http.StatusForbidden)
	adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/t_contract/resume", admin, map[string]any{
		"reason": "旧列表恢复请求", "expected_version": 0,
	}), http.StatusConflict)
	resumed := adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/t_contract/resume", admin, map[string]any{
		"reason": "风险复核恢复", "expected_version": 1,
	}), http.StatusOK)
	if resumed["status"] != "active" || resumed["row_version"] != float64(2) {
		t.Errorf("resume response = %v", resumed)
	}
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/analyses", admin, nil), http.StatusOK)
}

func TestContract_adminMembers_changesOnlyExistingExplicitMembership(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	access, refresh, user := mustRegister(t, r, "member-role@example.com", "成员角色")
	id := user["user_id"].(string)
	store := contractFixture(t).store
	if err := store.CreateMember(context.Background(), auth.Member{TenantID: "t_contract", UserID: id, Role: "analyst"}); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/tenants/t_contract/members/" + id + "/role"
	body := adminContractResponse(t, doReq(t, r, http.MethodPut, path, admin, map[string]any{
		"role": "viewer", "reason": "只读职责调整", "expected_version": 0,
	}), http.StatusOK)
	if body["tenant_id"] != "t_contract" || body["user_id"] != id || body["role"] != "viewer" || body["row_version"] != float64(1) {
		t.Errorf("membership role response = %v", body)
	}
	adminContractAssertRevoked(t, r, access, refresh)
	adminContractResponse(t, doReq(t, r, http.MethodPut, path, admin, map[string]any{
		"role": "analyst", "reason": "重放旧成员版本", "expected_version": 0,
	}), http.StatusConflict)
	if role, err := store.GetUserRole(context.Background(), user["tenant_id"].(string), id); err != nil || role != "tenant_admin" {
		t.Errorf("other tenant member changed: role=%q err=%v", role, err)
	}
	u, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := auth.GenerateTokenPair(auth.Principal{UserID: id, TenantID: "t_contract", TokenVersion: u.TokenVersion}, testJWTSecret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/analyses", pair.AccessToken, nil), http.StatusOK)
	adminContractResponse(t, doReq(t, r, http.MethodPost, "/api/v1/analyses", pair.AccessToken, map[string]any{"topic": "只读成员不能创建"}), http.StatusForbidden)
	adminContractResponse(t, doReq(t, r, http.MethodGet, "/api/v1/admin/users", pair.AccessToken, nil), http.StatusForbidden)
}

func TestContract_adminMembers_rejectsRoleEscalationMissingMemberAndLastAdmin(t *testing.T) {
	r, _ := newContractEnv(t)
	admin := adminContractToken(t)
	_, _, user := mustRegister(t, r, "non-member@example.com", "外部成员")
	for _, role := range []string{"platform_admin", "api_service", "owner", ""} {
		adminContractResponse(t, doReq(t, r, http.MethodPut, "/api/v1/admin/tenants/t_contract/members/u_contract_admin/role", admin, map[string]any{
			"role": role, "reason": "非法成员角色", "expected_version": 0,
		}), http.StatusBadRequest)
	}
	adminContractResponse(t, doReq(t, r, http.MethodPut, "/api/v1/admin/tenants/t_contract/members/"+user["user_id"].(string)+"/role", admin, map[string]any{
		"role": "viewer", "reason": "不得隐式创建成员", "expected_version": 0,
	}), http.StatusNotFound)
	adminContractResponse(t, doReq(t, r, http.MethodPut, "/api/v1/admin/tenants/t_contract/members/u_contract_admin/role", admin, map[string]any{
		"role": "viewer", "reason": "不得降级最后团队管理员", "expected_version": 0,
	}), http.StatusConflict)
	if _, err := contractFixture(t).store.GetUserRole(context.Background(), "t_contract", user["user_id"].(string)); err == nil {
		t.Error("missing member role mutation silently created a membership")
	}
	for _, role := range []string{"tenant_admin", "analyst", "viewer"} {
		token := issueToken(t, auth.Principal{UserID: "u_contract", TenantID: "t_contract", Roles: []string{role}})
		adminContractResponse(t, doReq(t, r, http.MethodPut, "/api/v1/admin/tenants/t_contract/members/u_contract_admin/role", token, map[string]any{
			"role": "viewer", "reason": "团队角色越权管理平台", "expected_version": 0,
		}), http.StatusForbidden)
	}
}
