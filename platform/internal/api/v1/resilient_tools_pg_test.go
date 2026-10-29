package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func resilientToolRequest(e *billingActorPGEnv, path string, body any, permit string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.RemoteAddr = "127.0.0.1:30123"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Billing-Service-Token", "isolated-tool-secret")
	if permit != "" {
		req.Header.Set("X-Tool-Call-Permit", permit)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func TestResilientToolPGAuthorizationAndUsageAreSeparateFromTokens(t *testing.T) {
	t.Setenv("YUQING_BILLING_SERVICE_TOKEN", "isolated-tool-secret")
	t.Setenv("YUQING_TOOL_PRICE_VERSION", "deepseek-tools-cny-2026-10-29")
	t.Setenv("YUQING_TOOL_PRICE_CURRENCY", "CNY")
	e := newBillingActorPGEnv(t)
	token, _, account := mustRegister(t, e.router, "tools@example.invalid", "Tool billing")
	tid := account["tenant_id"].(string)
	if err := e.deps.Credits.GrantPurchase(context.Background(), tid, "tool-seed", 1); err != nil {
		t.Fatal(err)
	}
	run := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "tool accounting"}), http.StatusCreated)
	body := map[string]any{"call_id": "tool-search-1", "run_id": run["current_run_id"], "provider": "deepseek", "operation": "search", "attempt": 1, "request_hash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	auth := adminContractResponse(t, resilientToolRequest(e, "/internal/v1/billing/tool-authorizations", body, ""), http.StatusCreated)
	permit := auth["permit"].(string)
	adminContractResponse(t, resilientToolRequest(e, "/internal/v1/billing/tool-authorizations", body, ""), http.StatusOK)
	event := map[string]any{"event_id": "tool-event-1", "call_id": "tool-search-1", "event_version": 1, "attempt": 1, "provider": "deepseek", "operation": "search", "outcome": "success", "http_status": 200, "result_count": 2, "usage_status": "reported", "provider_request_id": "provider-search-1"}
	adminContractResponse(t, resilientToolRequest(e, "/internal/v1/billing/tool-usage-events", event, "wrong"), http.StatusForbidden)
	adminContractResponse(t, resilientToolRequest(e, "/internal/v1/billing/tool-usage-events", event, permit), http.StatusCreated)
	adminContractResponse(t, resilientToolRequest(e, "/internal/v1/billing/tool-usage-events", event, permit), http.StatusOK)
	var cost int64
	var status string
	var tokens int
	if err := e.pool.QueryRow(context.Background(), `SELECT cost_micro_cny,cost_status FROM tool_usage_events WHERE event_id='tool-event-1'`).Scan(&cost, &status); err != nil {
		t.Fatal(err)
	}
	if cost != 5000 || status != "known" {
		t.Fatalf("verified tool price = %d/%s", cost, status)
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM usage_events WHERE call_id='tool-search-1'`).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("tool call contaminated token ledger: %d/%v", tokens, err)
	}
	e.rebuild()
	adminContractResponse(t, resilientToolRequest(e, "/internal/v1/billing/tool-usage-events", event, permit), http.StatusOK)
}

func TestResilientPGResumeAcceptsNewChargedRunAndPreservesOldSnapshot(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	e := newBillingActorPGEnv(t)
	token, _, account := mustRegister(t, e.router, "resume@example.invalid", "Resume")
	tid := account["tenant_id"].(string)
	if err := e.deps.Credits.GrantPurchase(context.Background(), tid, "resume-seed", 2); err != nil {
		t.Fatal(err)
	}
	a := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "resume task", "delivery_policy": "best_available"}), http.StatusCreated)
	aid, old := a["id"].(string), a["current_run_id"].(string)
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses/"+aid+"/cancel", token, nil), http.StatusOK)
	resumed := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses/"+aid+"/resume", token, map[string]any{}), http.StatusOK)
	if resumed["current_run_id"] == old || resumed["resume_from_run_id"] != old {
		t.Fatalf("resume revived or lost original run: %+v", resumed)
	}
	var oldState string
	var consumes, refunds, balance int
	if err := e.pool.QueryRow(context.Background(), `SELECT state FROM analysis_runs WHERE id=$1`, old).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if oldState != "canceled" {
		t.Fatalf("resume rewrote settled old run: %s", oldState)
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT balance,(SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='consume'),(SELECT count(*) FROM credit_transactions WHERE tenant_id=$1 AND reason='refund') FROM report_credits WHERE tenant_id=$1`, tid).Scan(&balance, &consumes, &refunds); err != nil {
		t.Fatal(err)
	}
	if balance != 1 || consumes != 2 || refunds != 1 {
		t.Fatalf("resume policy: balance=%d consumes=%d refunds=%d", balance, consumes, refunds)
	}
}
