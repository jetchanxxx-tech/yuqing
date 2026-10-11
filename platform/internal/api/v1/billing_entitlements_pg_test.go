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
	if _, err := e.pool.Exec(ctx, `INSERT INTO usage_events(tenant_id,user_id,model,prompt_tokens,completion_tokens,cache_tokens,quota_tokens,billing_exempt,usage_status,cost_status,cost_micro_cny,billed_micro_cny,event_version) VALUES($1,$2,'sandbox',400,100,20,0,true,'reported','known',3200,0,1),($1,$2,'unknown',0,0,0,0,true,'unknown','pending',NULL,0,1)`, account["tenant_id"], account["user_id"]); err != nil {
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

func TestBillingEntitlementsPGLegacyCacheAndVersionedProviderUsageStayDistinct(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, account := mustRegister(t, e.router, "usage-version@example.invalid", "Usage version facts")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO usage_events(tenant_id,user_id,model,prompt_tokens,completion_tokens,cache_tokens,quota_tokens,usage_status,cost_status,event_version) VALUES($1,$2,'legacy',100,50,20,170,'reported','pending',NULL),($1,$2,'versioned',100,50,20,150,'reported','pending',1)`, account["tenant_id"], account["user_id"]); err != nil {
		t.Fatal(err)
	}
	usage := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/usage", token, nil), http.StatusOK)
	if usage["actual_tokens"] != float64(320) || usage["quota_tokens_used"] != float64(320) {
		t.Fatalf("legacy additive cache and new subset cache mixed: %v", usage)
	}
	if actual := e.deps.Usage.Aggregate()[account["tenant_id"].(string)]; actual != 320 {
		t.Fatalf("public and aggregate facts disagree: %d", actual)
	}
}

func TestBillingEntitlementsPGAbsentInvalidAndUnknownPlansDoNotInventEntitlements(t *testing.T) {
	for _, kind := range []string{"absent", "empty", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("YUQING_BILLING_SERVICE_TOKEN", "isolated-plan-boundary-secret")
			e := newBillingActorPGEnv(t)
			token, _, account := mustRegister(t, e.router, "plan-"+kind+"@example.invalid", "Plan source boundary")
			ctx := context.Background()
			if kind == "absent" {
				if _, err := e.pool.Exec(ctx, `DELETE FROM report_credits WHERE tenant_id=$1`, account["tenant_id"]); err != nil {
					t.Fatal(err)
				}
				me := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/auth/me", token, nil), http.StatusOK)
				credits := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/credits", token, nil), http.StatusOK)
				if me["plan_code"] != "free" || me["plan_status"] != "valid" || me["plan_source"] != "default_free" || credits["plan_code"] != "free" || credits["effective_plan_source"] != "default_free" || credits["balance"] != float64(0) {
					t.Errorf("absent row fabricated paid source: %v/%v", me, credits)
				}
				var rows int
				if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM report_credits WHERE tenant_id=$1`, account["tenant_id"]).Scan(&rows); err != nil || rows != 0 {
					t.Fatalf("read created missing credit row: %d/%v", rows, err)
				}
			} else {
				accepted := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "accepted before plan corruption"}), http.StatusCreated)
				if _, err := e.pool.Exec(ctx, `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, account["user_id"]); err != nil {
					t.Fatal(err)
				}
				code := ""
				if kind == "unknown" {
					code = "not-a-catalog-plan"
				}
				if _, err := e.pool.Exec(ctx, `UPDATE report_credits SET plan_code=$2 WHERE tenant_id=$1`, account["tenant_id"], code); err != nil {
					t.Fatal(err)
				}
				me := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/auth/me", token, nil), http.StatusOK)
				if me["plan_code"] != "unavailable" || me["plan_status"] != "invalid" || me["plan_source"] != "report_credits" {
					t.Errorf("invalid persisted plan advertised as catalog entitlement: %v", me)
				}
				adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/user/profile", token, nil), http.StatusOK)
				adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/admin/users", token, nil), http.StatusOK)
				if _, err := e.pool.Exec(ctx, `INSERT INTO reports(id,tenant_id,analysis_id,format,status,file_key,created_by,report_version) VALUES('plan-boundary-report',$1,$2,'html','completed','sandbox.html',$3,1)`, account["tenant_id"], accepted["id"], account["user_id"]); err != nil {
					t.Fatal(err)
				}
				if _, err := e.pool.Exec(ctx, `UPDATE analyses SET report_content='<p>sandbox report</p>' WHERE id=$1`, accepted["id"]); err != nil {
					t.Fatal(err)
				}
				adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/reports/plan-boundary-report/download", token, nil), http.StatusForbidden)
				for _, path := range []string{"/api/v1/billing/credits", "/api/v1/billing/usage"} {
					adminContractResponse(t, doReq(t, e.router, http.MethodGet, path, token, nil), http.StatusServiceUnavailable)
				}
				adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "invalid plan must not admit"}), http.StatusServiceUnavailable)
				auth := map[string]any{"call_id": "invalid-plan-call", "run_id": accepted["current_run_id"], "engine": "insight", "phase": "analyze", "attempt": 1, "model": "sandbox"}
				adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", auth, "", "isolated-plan-boundary-secret"), http.StatusServiceUnavailable)
				var analyses, consumes, authorizations int
				if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM analyses),(SELECT count(*) FROM credit_transactions WHERE reason='consume'),(SELECT count(*) FROM llm_call_authorizations)`).Scan(&analyses, &consumes, &authorizations); err != nil {
					t.Fatal(err)
				}
				if analyses != 1 || consumes != 1 || authorizations != 0 {
					t.Fatalf("invalid plan admission wrote effects: %d/%d/%d", analyses, consumes, authorizations)
				}
			}
		})
	}
}
