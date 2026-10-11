package v1_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func finalQueuedMutation(t *testing.T, kind, change string) {
	t.Helper()
	e := newBillingActorPGEnv(t)
	token, _, owner := mustRegister(t, e.router, "final-"+kind+"-"+change+"@example.invalid", "Queued actor")
	var target, table, path, method string
	var initialBalance int
	var initialVersion int64
	if kind == "cancel" {
		result := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/analyses", token, map[string]any{"name": "queued cancellation", "analysis_type": "sentiment", "keywords": []string{"isolated"}, "sources": []string{"news"}}), 201)
		target, table = result["id"].(string), "analyses"
		path, method = "/api/v1/analyses/"+target+"/cancel", http.MethodPost
		if err := e.pool.QueryRow(context.Background(), `SELECT balance,version FROM report_credits WHERE tenant_id=$1`, owner["tenant_id"]).Scan(&initialBalance, &initialVersion); err != nil {
			t.Fatal(err)
		}
		if change == "unchanged_actor" {
			if _, err := e.pool.Exec(context.Background(), `UPDATE tenant_members SET role='analyst' WHERE tenant_id=$1 AND user_id=$2`, owner["tenant_id"], owner["user_id"]); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		result := adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/apikeys", token, map[string]any{"name": "queued revocation"}), 201)
		target, table = result["id"].(string), "api_keys"
		path, method = "/api/v1/apikeys/"+target, http.MethodDelete
	}
	dsn, err := url.Parse(e.cfg.DB.Primary)
	if err != nil {
		t.Fatal(err)
	}
	application := "final-" + kind + "-" + change
	values := dsn.Query()
	values.Set("application_name", application)
	dsn.RawQuery = values.Encode()
	e.cfg.DB.Primary = dsn.String()
	e.rebuild()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	// Both implementations reach a real DB barrier: vulnerable code waits on the
	// target row; repaired code first waits on the shared authority boundary.
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(741914)`); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `SELECT id FROM `+table+` WHERE id=$1 FOR UPDATE`, target); err != nil {
		t.Fatal(err)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- doReq(t, e.router, method, path, token, nil) }()
	for {
		var waiting bool
		if err = e.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, application).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case w := <-result:
			t.Fatalf("request never reached queued DB boundary: %d %s", w.Code, w.Body.String())
		case <-ctx.Done():
			t.Fatal("mutation did not reach barrier")
		case <-time.After(10 * time.Millisecond):
		}
	}
	switch change {
	case "credential_version":
		_, err = blocker.Exec(ctx, `UPDATE users SET token_version=token_version+1 WHERE id=$1`, owner["user_id"])
	case "disabled_user":
		_, err = blocker.Exec(ctx, `UPDATE users SET status='disabled',token_version=token_version+1 WHERE id=$1`, owner["user_id"])
	case "membership_permission":
		_, err = blocker.Exec(ctx, `UPDATE tenant_members SET role='viewer' WHERE tenant_id=$1 AND user_id=$2`, owner["tenant_id"], owner["user_id"])
	case "suspended_tenant":
		_, err = blocker.Exec(ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, owner["tenant_id"])
	case "unchanged_actor":
	default:
		t.Fatal("unknown fixture change")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	want := http.StatusForbidden
	if change == "unchanged_actor" {
		want = 200
		if kind == "revoke" {
			want = 204
		}
	}
	var response *httptest.ResponseRecorder
	select {
	case response = <-result:
	case <-ctx.Done():
		t.Fatal("queued request did not finish")
	}
	if response.Code != want {
		t.Errorf("queued %s after %s status=%d want=%d body=%s", kind, change, response.Code, want, response.Body.String())
	}
	if kind == "cancel" {
		var state, runState string
		var balance, refunds int
		var version int64
		if err = e.pool.QueryRow(ctx, `SELECT a.state,r.state,c.balance,c.version,(SELECT count(*) FROM credit_transactions WHERE run_id=a.current_run_id AND reason='refund') FROM analyses a JOIN analysis_runs r ON r.id=a.current_run_id JOIN report_credits c ON c.tenant_id=a.tenant_id WHERE a.id=$1`, target).Scan(&state, &runState, &balance, &version, &refunds); err != nil {
			t.Fatal(err)
		}
		expectedState := "queued"
		expectedRefunds := 0
		expectedBalance := initialBalance
		expectedVersion := initialVersion
		if change == "unchanged_actor" {
			expectedState = "canceled"
			expectedRefunds = 1
			expectedBalance++
			expectedVersion++
		}
		if state != expectedState || runState != expectedState || balance != expectedBalance || version != expectedVersion || refunds != expectedRefunds {
			t.Errorf("queued cancellation durable facts state=%s run=%s balance=%d version=%d refunds=%d want=%s/%s/%d/%d/%d", state, runState, balance, version, refunds, expectedState, expectedState, expectedBalance, expectedVersion, expectedRefunds)
		}
		if change == "unchanged_actor" {
			if w := doReq(t, e.router, method, path, token, nil); w.Code != 409 {
				t.Errorf("terminal cancellation replay=%d", w.Code)
			}
			var count int
			if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM credit_transactions WHERE run_id=(SELECT current_run_id FROM analyses WHERE id=$1) AND reason='refund'`, target).Scan(&count); err != nil || count != 1 {
				t.Fatal("replay changed paired refund count")
			}
		}
	} else {
		var revoked bool
		if err = e.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM api_keys WHERE id=$1`, target).Scan(&revoked); err != nil {
			t.Fatal(err)
		}
		if revoked != (change == "unchanged_actor") {
			t.Errorf("queued revocation changed key=%t after %s", revoked, change)
		}
		if change == "unchanged_actor" {
			if w := doReq(t, e.router, method, path, token, nil); w.Code != 204 {
				t.Errorf("authorized key revoke replay=%d", w.Code)
			}
		}
	}
	if strings.Contains(response.Body.String(), `"api_key":`) {
		t.Fatal("mutation returned a raw key")
	}
	t.Logf("actual queued %s original-actor control=%s status=%d", kind, change, response.Code)
}
func TestFinalQueuedCancellationRechecksOriginalActor(t *testing.T) {
	for _, change := range []string{"credential_version", "disabled_user", "membership_permission", "suspended_tenant", "unchanged_actor"} {
		t.Run(change, func(t *testing.T) { finalQueuedMutation(t, "cancel", change) })
	}
}
func TestFinalQueuedAPIKeyRevocationRechecksOriginalActor(t *testing.T) {
	for _, change := range []string{"credential_version", "disabled_user", "membership_permission", "suspended_tenant", "unchanged_actor"} {
		t.Run(change, func(t *testing.T) { finalQueuedMutation(t, "revoke", change) })
	}
}
