package v1_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestResilientPythonToolBridgePG(t *testing.T) {
	if os.Getenv("YUQING_TOOL_BRIDGE_TEST") != "1" {
		t.Skip("actual Python bridge contract requires hosted Python dependencies")
	}
	t.Setenv("YUQING_BILLING_SERVICE_TOKEN", "hosted-python-tool-secret")
	t.Setenv("YUQING_TOOL_PRICE_VERSION", "deepseek-tools-cny-2026-10-29")
	t.Setenv("YUQING_TOOL_PRICE_CURRENCY", "CNY")
	e := newBillingActorPGEnv(t)
	token, _, account := mustRegister(t, e.router, "python-tools@example.invalid", "Python tool bridge")
	tid := account["tenant_id"].(string)
	if err := e.deps.Credits.GrantPurchase(context.Background(), tid, "python-tool-seed", 1); err != nil {
		t.Fatal(err)
	}
	a := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "actual bridge"}), http.StatusCreated)
	run := a["current_run_id"].(string)
	var loseACK atomic.Bool
	loseACK.Store(true)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := httptest.NewRecorder()
		e.router.ServeHTTP(response, r)
		if r.URL.Path == "/internal/v1/billing/tool-usage-events" && response.Code == http.StatusCreated && loseACK.CompareAndSwap(true, false) {
			http.Error(w, "isolated ACK loss after PG commit", http.StatusServiceUnavailable)
			return
		}
		for key, values := range response.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	}))
	defer bridge.Close()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	outbox := t.TempDir()
	tokenOutbox := t.TempDir()
	invoke := func(mode string, crashes bool) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "python3", filepath.Join(root, "scripts/ci/verify-tool-usage-bridge.py"), "--mode", mode)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "YUQING_BILLING_URL="+bridge.URL, "YUQING_TOOL_BRIDGE_RUN_ID="+run, "YUQING_TOOL_USAGE_OUTBOX_DIR="+outbox, "YUQING_USAGE_OUTBOX_DIR="+tokenOutbox)
		output, err := cmd.CombinedOutput()
		if crashes {
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 23 {
				t.Fatalf("pending-call crash=%v output=%s", err, output)
			}
		} else if err != nil {
			t.Fatalf("Python bridge %s: %v\n%s", mode, err, output)
		}
	}
	invoke("spend", false)
	files, err := filepath.Glob(filepath.Join(outbox, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("ACK loss discarded durable event: %v/%v", files, err)
	}
	invoke("replay", false)
	invoke("fetch", false)
	invoke("crash", true)
	invoke("replay", false)
	var calls, events, known, pending, tokens int
	var cost int64
	if err = e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM tool_call_authorizations WHERE run_id=$1),count(*),count(*) FILTER(WHERE cost_status='known'),count(*) FILTER(WHERE usage_status='unknown' AND cost_status='pending' AND cost_micro_cny IS NULL),COALESCE(sum(cost_micro_cny),0),(SELECT count(*) FROM usage_events WHERE run_id=$1) FROM tool_usage_events WHERE run_id=$1`, run).Scan(&calls, &events, &known, &pending, &cost, &tokens); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || events != 3 || known != 2 || pending != 1 || cost != 10000 || tokens != 0 {
		t.Fatalf("actual bridge facts calls/events/known/pending/cost/tokens=%d/%d/%d/%d/%d/%d", calls, events, known, pending, cost, tokens)
	}
	e.rebuild()
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses/"+a["id"].(string)+"/cancel", token, nil), http.StatusOK)
	invoke("denied", false)
	var after int
	if err = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM tool_call_authorizations WHERE run_id=$1`, run).Scan(&after); err != nil || after != calls {
		t.Fatalf("canceled run authorized another call: %d/%v", after, err)
	}
}
