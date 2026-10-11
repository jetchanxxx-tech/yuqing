package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/platform/auth"
)

func internalBillingRequest(e *billingActorPGEnv, path string, body any, permit, secret string) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.RemoteAddr = "127.0.0.1:34567"
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Billing-Service-Token", secret)
	}
	if permit != "" {
		req.Header.Set("X-LLM-Call-Permit", permit)
	}
	response := httptest.NewRecorder()
	e.router.ServeHTTP(response, req)
	return response
}

func TestBillingUsagePGActualCostReplayLateSettlementAndQuotaIsolation(t *testing.T) {
	t.Setenv("YUQING_BILLING_SERVICE_TOKEN", "isolated-billing-secret")
	t.Setenv("YUQING_PROVIDER_PRICE_VERSION", "sandbox-price-v1")
	t.Setenv("YUQING_PROVIDER_PRICE_CURRENCY", "CNY")
	e := newBillingActorPGEnv(t)
	// Prices are explicitly sandbox-only:4/16CNY perM yields3200microCNY.
	e.cfg.LLM.Models = []config.ModelConfig{{ID: "sandbox-model", Provider: "sandbox", InputCostPerM: 4, OutputCostPerM: 16}}
	e.rebuild()
	token, _, normal := mustRegister(t, e.router, "normal-usage@example.invalid", "Normal usage")
	fixedToken, _, fixed := mustRegister(t, e.router, "admin@pangu.com", "Fixed usage")
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO billing_exempt_principals(policy_key,user_id,bound_by) VALUES('fixed_admin_v1',$1,'isolated-test')`, fixed["user_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'tenant_admin')`, normal["tenant_id"], fixed["user_id"]); err != nil {
		t.Fatal(err)
	}
	pair, err := auth.GenerateTokenPair(auth.Principal{UserID: fixed["user_id"].(string), TenantID: normal["tenant_id"].(string), TokenVersion: 0}, testJWTSecret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}
	fixedToken = pair.AccessToken
	create := func(token, name string) map[string]any {
		return adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": name}), http.StatusCreated)
	}
	normalRun := create(token, "normal paid provider run")
	freeRun := create(fixedToken, "free provider run")
	authorize := func(call, run, model string) map[string]any {
		return map[string]any{"call_id": call, "run_id": run, "engine": "insight", "phase": "analyze", "attempt": 1, "model": model}
	}
	authBody := authorize("normal-call", normalRun["current_run_id"].(string), "sandbox-model")
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authBody, "", ""), http.StatusForbidden)
	externalBody, _ := json.Marshal(authBody)
	external := httptest.NewRequest(http.MethodPost, "/internal/v1/billing/llm-authorizations", bytes.NewReader(externalBody))
	external.RemoteAddr = "10.0.0.1:1234"
	external.Header.Set("X-Billing-Service-Token", "isolated-billing-secret")
	external.Header.Set("Content-Type", "application/json")
	remote := httptest.NewRecorder()
	e.router.ServeHTTP(remote, external)
	adminContractResponse(t, remote, http.StatusForbidden)
	auth := adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authBody, "", "isolated-billing-secret"), http.StatusCreated)
	permit := auth["permit"].(string)
	replay := adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authBody, "", "isolated-billing-secret"), http.StatusOK)
	if replay["permit"] != permit {
		t.Fatal("authorization ACK retry changed call permit")
	}
	conflict := authorize("normal-call", normalRun["current_run_id"].(string), "changed-model")
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", conflict, "", "isolated-billing-secret"), http.StatusConflict)
	event := map[string]any{"event_id": "normal-event", "call_id": "normal-call", "event_version": 1, "attempt": 1, "actual_model": "sandbox-model", "provider_request_id": "sandbox-request", "usage_status": "reported", "prompt_tokens": 400, "completion_tokens": 100, "cache_tokens": 20, "outcome": "invalid_json"}
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/usage-events", event, "wrong", "isolated-billing-secret"), http.StatusForbidden)
	negative := map[string]any{}
	for k, v := range event {
		negative[k] = v
	}
	negative["prompt_tokens"] = -1
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/usage-events", negative, permit, "isolated-billing-secret"), http.StatusBadRequest)
	// Already accepted provider cost settles after cancellation and actor disabling.
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses/"+normalRun["id"].(string)+"/cancel", token, nil), http.StatusOK)
	if _, err := e.pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, normal["user_id"]); err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/usage-events", event, permit, "isolated-billing-secret"), http.StatusCreated)
	e.rebuild()
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/usage-events", event, permit, "isolated-billing-secret"), http.StatusOK)
	event["prompt_tokens"] = 401
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/usage-events", event, permit, "isolated-billing-secret"), http.StatusConflict)
	var rows, quota, cost, billed int64
	if err := e.pool.QueryRow(ctx, `SELECT count(*),sum(quota_tokens),sum(cost_micro_cny),sum(billed_micro_cny) FROM usage_events WHERE call_id='normal-call'`).Scan(&rows, &quota, &cost, &billed); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || quota != 500 || cost != 3200 || billed != 0 {
		t.Fatalf("actual usage rows/quota/cost/extra charge=%d/%d/%d/%d", rows, quota, cost, billed)
	}
	var balance, consume, refund int
	if err := e.pool.QueryRow(ctx, `SELECT balance,(SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='consume'),(SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='refund') FROM report_credits WHERE tenant_id=$1`, normal["tenant_id"]).Scan(&balance, &consume, &refund); err != nil {
		t.Fatal(err)
	}
	if balance != 1 || consume != 1 || refund != 1 {
		t.Fatalf("report charge/refund detached from provider cost: %d/%d/%d", balance, consume, refund)
	}
	freeAuth := adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authorize("free-call", freeRun["current_run_id"].(string), "sandbox-model"), "", "isolated-billing-secret"), http.StatusCreated)
	event["event_id"] = "free-event"
	event["call_id"] = "free-call"
	event["prompt_tokens"] = 400
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/usage-events", event, freeAuth["permit"].(string), "isolated-billing-secret"), http.StatusCreated)
	var prompt, completion int64
	var exempt bool
	if err := e.pool.QueryRow(ctx, `SELECT prompt_tokens,completion_tokens,quota_tokens,cost_micro_cny,billed_micro_cny,billing_exempt FROM usage_events WHERE call_id='free-call'`).Scan(&prompt, &completion, &quota, &cost, &billed, &exempt); err != nil {
		t.Fatal(err)
	}
	if prompt != 400 || completion != 100 || quota != 0 || cost != 3200 || billed != 0 || !exempt {
		t.Fatalf("free actual cost/quota mismatch: %d/%d/%d/%d/%d/%v", prompt, completion, quota, cost, billed, exempt)
	}
	// An absent price and missing supplier usage remain explicit unknown facts.
	pendingAuth := adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authorize("pending-call", freeRun["current_run_id"].(string), "unpriced-model"), "", "isolated-billing-secret"), http.StatusCreated)
	unknown := map[string]any{"event_id": "pending-event", "call_id": "pending-call", "event_version": 1, "attempt": 1, "actual_model": "unpriced-model", "usage_status": "unknown", "prompt_tokens": 0, "completion_tokens": 0, "cache_tokens": 0, "outcome": "network_error"}
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/usage-events", unknown, pendingAuth["permit"].(string), "isolated-billing-secret"), http.StatusCreated)
	var amount *int64
	var usageStatus, costStatus string
	if err := e.pool.QueryRow(ctx, `SELECT cost_micro_cny,usage_status,cost_status FROM usage_events WHERE call_id='pending-call'`).Scan(&amount, &usageStatus, &costStatus); err != nil {
		t.Fatal(err)
	}
	if amount != nil || usageStatus != "unknown" || costStatus != "pending" {
		t.Fatalf("unknown presented as real zero: %v/%s/%s", amount, usageStatus, costStatus)
	}
	// The fixed identity shares this team's actual token ledger. Its500 actual
	// tokens must not push the ordinary teammate's999999 quota total over1M.
	if _, err := e.pool.Exec(ctx, `INSERT INTO usage_events(tenant_id,user_id,model,prompt_tokens,completion_tokens,cache_tokens,quota_tokens,usage_status,cost_status) VALUES($1,$2,'historical-sandbox',999499,0,0,999499,'reported','pending')`, normal["tenant_id"], normal["user_id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, normal["user_id"]); err != nil {
		t.Fatal(err)
	}
	nextRun := create(token, "normal remaining quota")
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authorize("last-under-cap", nextRun["current_run_id"].(string), "sandbox-model"), "", "isolated-billing-secret"), http.StatusCreated)
	if _, err := e.pool.Exec(ctx, `INSERT INTO usage_events(tenant_id,user_id,model,prompt_tokens,completion_tokens,cache_tokens,quota_tokens,usage_status,cost_status) VALUES($1,$2,'historical-sandbox',1,0,0,1,'reported','pending')`, normal["tenant_id"], normal["user_id"]); err != nil {
		t.Fatal(err)
	}
	e.rebuild()
	capped := adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authorize("over-cap", nextRun["current_run_id"].(string), "sandbox-model"), "", "isolated-billing-secret"), http.StatusPaymentRequired)
	if capped["code"] != "TOKEN_QUOTA_EXCEEDED" {
		t.Fatalf("quota after restart=%v", capped)
	}
	adminContractResponse(t, internalBillingRequest(e, "/internal/v1/billing/llm-authorizations", authorize("free-over-team-cap", freeRun["current_run_id"].(string), "sandbox-model"), "", "isolated-billing-secret"), http.StatusCreated)

}
