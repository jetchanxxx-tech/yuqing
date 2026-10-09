package v1_test

import (
	"context"
	"net/http"
	"testing"
)

func TestBillingEntitlementsPGUsesActualPlanBalanceAndExplicitCycle(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, account := mustRegister(t, e.router, "actual-entitlement@example.invalid", "Actual entitlement")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE report_credits SET balance=0,plan_code='enterprise' WHERE tenant_id=$1`, account["tenant_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, account["user_id"]); err != nil {
		t.Fatal(err)
	}
	me := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/auth/me", token, nil), http.StatusOK)
	if me["plan_code"] != "enterprise" {
		t.Errorf("live session ignored paid plan: %v", me)
	}
	credits := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/credits", token, nil), http.StatusOK)
	if credits["plan_code"] != "enterprise" || credits["effective_plan_source"] != "report_credits" || credits["charging_model"] != "report_credit" || credits["balance"] != float64(0) || credits["billing_exempt"] != false || credits["limit_mode"] != "limited" || credits["effective_remaining_reports"] != float64(0) {
		t.Errorf("ordinary enterprise entitlements=%v", credits)
	}
	usage := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/usage", token, nil), http.StatusOK)
	if usage["plan_code"] != "enterprise" || usage["budget_mode"] != "none" || usage["token_quota"] != float64(0) || usage["billing_exempt"] != false || usage["cycle_status"] != "not_configured" || usage["period_start"] != nil || usage["period_end"] != nil {
		t.Errorf("actual plan/cycle=%v", usage)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "enterprise still requires report credits"}), http.StatusPaymentRequired)
	subscription := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/subscription", token, nil), http.StatusOK)
	if subscription["plan"] != "enterprise" || subscription["status"] != "not_configured" {
		t.Errorf("subscription fabricated=%v", subscription)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/billing/subscribe", token, map[string]any{"plan_code": "pro"}), http.StatusNotImplemented)
	var plan string
	if err := e.pool.QueryRow(ctx, `SELECT plan_code FROM report_credits WHERE tenant_id=$1`, account["tenant_id"]).Scan(&plan); err != nil || plan != "enterprise" {
		t.Fatalf("selection activated unpaid plan: %s/%v", plan, err)
	}
}

func TestBillingEntitlementsPGFixedAccountShowsRealBalanceAndPendingCost(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, account := mustRegister(t, e.router, "admin@pangu.com", "Fixed entitlement")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO billing_exempt_principals(policy_key,user_id,bound_by) VALUES('fixed_admin_v1',$1,'isolated-test')`, account["user_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE report_credits SET balance=0,plan_code='lite' WHERE tenant_id=$1`, account["tenant_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO usage_events(tenant_id,user_id,model,prompt_tokens,completion_tokens,cache_tokens,quota_tokens,billing_exempt,usage_status,cost_status,cost_micro_cny,billed_micro_cny) VALUES($1,$2,'sandbox',400,100,20,0,true,'reported','known',3200,0),($1,$2,'unknown',0,0,0,0,true,'unknown','pending',NULL,0)`, account["tenant_id"], account["user_id"]); err != nil {
		t.Fatal(err)
	}
	e.rebuild()
	budget, err := e.deps.Usage.BudgetStatus(ctx, account["tenant_id"].(string))
	if err != nil || budget.QuotaTokens != 5000000 || budget.SpentTokens != 0 {
		t.Errorf("shared meter lost real plan/exempt quota after restart: %+v/%v", budget, err)
	}
	credits := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/credits", token, nil), http.StatusOK)
	if credits["balance"] != float64(0) || credits["plan_code"] != "lite" || credits["billing_exempt"] != true || credits["limit_mode"] != "unlimited" || credits["effective_remaining_reports"] != nil {
		t.Errorf("fixed balance must remain real: %v", credits)
	}
	usage := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/usage", token, nil), http.StatusOK)
	if usage["actual_tokens"] != float64(500) || usage["quota_tokens_used"] != float64(0) || usage["token_quota"] != float64(5000000) || usage["billing_exempt"] != true || usage["known_cost_micro_cny"] != float64(3200) || usage["pending_cost_events"] != float64(1) || usage["provider_cost_complete"] != false {
		t.Errorf("usage hid actual or pending cost: %v", usage)
	}
}
