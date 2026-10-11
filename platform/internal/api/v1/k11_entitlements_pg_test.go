package v1_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestK11PGManualPlanPreservesFinancialFactsAndFreshSources(t *testing.T) {
	e, _, access, admin := k9Env(t)
	tid := admin["tenant_id"].(string)
	k9Exec(t, e, `INSERT INTO orders(id,tenant_id,sku_code,kind,credits,amount_cents,channel,state,granted,expires_at) VALUES('actual-history',$1,'lite','plan',10,9990,'sandbox','paid',true,now())`, tid)
	version := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
	ledger := k9Count(t, e, `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1`, tid)
	path := "/api/v1/admin/tenants/" + tid + "/plan-change"
	body := adminContractResponse(t, doReq(t, e.router, http.MethodPost, path, access, map[string]any{"plan_code": "pro", "reason": "  approved manual entitlement  ", "expected_version": version}), http.StatusOK)
	if body["plan_code"] != "pro" || body["plan_source"] != "manual" || body["balance"] != float64(1) || body["version"] != float64(version+1) {
		t.Fatalf("manual change fabricated credits/source or lost CAS: %v", body)
	}
	if k9Count(t, e, `SELECT count(*) FROM credit_transactions WHERE tenant_id=$1`, tid) != ledger || k9Count(t, e, `SELECT count(*) FROM orders WHERE tenant_id=$1`, tid) != 1 || k9Count(t, e, `SELECT count(*) FROM orders WHERE id='actual-history' AND state='paid' AND granted AND amount_cents=9990 AND credits=10`) != 1 || k9Count(t, e, `SELECT count(*) FROM subscriptions WHERE tenant_id=$1`, tid) != 0 {
		t.Fatal("manual entitlement rewrote financial/order/subscription facts")
	}
	if k9Count(t, e, `SELECT count(*) FROM audit_logs WHERE action='tenant.plan_change' AND actor_id=$1 AND tenant_id=$2 AND details_json->>'reason'='approved manual entitlement' AND details_json->'before'->>'plan_code'='free' AND details_json->'after'->>'plan_code'='pro' AND details_json->'after'->>'plan_source'='manual'`, admin["user_id"], tid) != 1 {
		t.Fatal("manual plan and original-actor audit were not atomic")
	}
	e.rebuild()
	me := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/auth/me", access, nil), http.StatusOK)
	pool := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/credits", access, nil), http.StatusOK)
	tenant := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/admin/tenants/"+tid, access, nil), http.StatusOK)
	usage := adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/billing/usage", access, nil), http.StatusOK)
	if me["plan_code"] != "pro" || me["plan_source"] != "manual" || pool["effective_plan_source"] != "manual" || pool["balance"] != float64(1) || pool["billing_exempt"] != false || tenant["effective_plan_code"] != "pro" || tenant["plan_source"] != "manual" || usage["cycle_status"] != "not_configured" || usage["period_start"] != nil || usage["period_end"] != nil {
		t.Fatalf("fresh projections invented purchase/cycle/exemption: me=%v credits=%v tenant=%v usage=%v", me, pool, tenant, usage)
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, "/api/v1/admin/plans", access, map[string]any{"code": "custom"}), http.StatusNotImplemented)
}

func TestK11PGManualPlanStrictInputCurrentActorAndCAS(t *testing.T) {
	e, _, access, admin := k9Env(t)
	tid := admin["tenant_id"].(string)
	path := "/api/v1/admin/tenants/" + tid + "/plan-change"
	version := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
	for _, input := range []map[string]any{
		{"plan_code": "pro", "expected_version": version},
		{"plan_code": "pro", "reason": "missing version"},
		{"plan_code": "invented", "reason": "unknown catalog", "expected_version": version},
		{"plan_code": "pro", "reason": "unexpected price", "expected_version": version, "amount_cents": 1},
		{"plan_code": "pro", "reason": "unexpected credit", "expected_version": version, "delta": 10},
	} {
		adminContractResponse(t, doReq(t, e.router, http.MethodPost, path, access, input), http.StatusBadRequest)
	}
	other, _, _ := mustRegister(t, e.router, "k11-nonadmin@example.invalid", "Non admin")
	input := map[string]any{"plan_code": "lite", "reason": "manual", "expected_version": version}
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, path, other, input), http.StatusForbidden)
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, path, access, input), http.StatusOK)
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, path, access, input), http.StatusConflict)
	k9Exec(t, e, `DELETE FROM platform_user_roles WHERE user_id=$1`, admin["user_id"])
	input["expected_version"] = version + 1
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, path, access, input), http.StatusForbidden)
	if k9Count(t, e, `SELECT count(*) FROM audit_logs WHERE action='tenant.plan_change' AND tenant_id=$1`, tid) != 1 || k9Count(t, e, `SELECT balance FROM report_credits WHERE tenant_id=$1`, tid) != 1 {
		t.Fatal("rejected input/actor/replay mutated entitlement facts")
	}
}

func TestK11PGManualPlanConcurrentOneWinnerAndAuditRollback(t *testing.T) {
	e, _, access, admin := k9Env(t)
	tid := admin["tenant_id"].(string)
	path := "/api/v1/admin/tenants/" + tid + "/plan-change"
	version := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, code := range []string{"lite", "pro"} {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			results <- doReq(t, e.router, http.MethodPost, path, access, map[string]any{"plan_code": code, "reason": "concurrent", "expected_version": version}).Code
		}(code)
	}
	wg.Wait()
	close(results)
	winners := 0
	for status := range results {
		if status == http.StatusOK {
			winners++
		} else if status != http.StatusConflict {
			t.Errorf("concurrent manual status=%d", status)
		}
	}
	if winners != 1 {
		t.Fatalf("manual CAS winners=%d", winners)
	}
	var before string
	if err := e.pool.QueryRow(context.Background(), `SELECT plan_code FROM report_credits WHERE tenant_id=$1`, tid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	k9Exec(t, e, `CREATE FUNCTION reject_k11_plan_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='tenant.plan_change' THEN RAISE EXCEPTION 'isolated plan audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_k11_plan_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION reject_k11_plan_audit()`)
	adminContractResponse(t, doReq(t, e.router, http.MethodPost, path, access, map[string]any{"plan_code": "enterprise", "reason": "rollback", "expected_version": version + 1}), http.StatusInternalServerError)
	var after string
	if err := e.pool.QueryRow(context.Background(), `SELECT plan_code FROM report_credits WHERE tenant_id=$1`, tid).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before || k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid) != version+1 || k9Count(t, e, `SELECT count(*) FROM audit_logs WHERE action='tenant.plan_change' AND tenant_id=$1`, tid) != 1 {
		t.Fatal("failed plan audit left partial plan/source/version write")
	}
}

func TestK11PGNotificationPreferencesPersistOnlyConnectedKeys(t *testing.T) {
	e := newBillingActorPGEnv(t)
	access, _, user := mustRegister(t, e.router, "k11-preferences@example.invalid", "Preferences")
	path := "/api/v1/user/notification-preferences"
	adminContractResponse(t, doReq(t, e.router, http.MethodPut, path, access, map[string]any{"task_completed": false}), http.StatusOK)
	e.rebuild()
	body := adminContractResponse(t, doReq(t, e.router, http.MethodGet, path, access, nil), http.StatusOK)
	prefs, ok := body["preferences"].(map[string]any)
	if !ok || prefs["task_completed"] != false || len(prefs) != 1 {
		t.Fatalf("preference was lost or unwired event exposed: %v", body)
	}
	for _, input := range []map[string]any{{"task_completed": "false"}, {"task_completed": nil}, {"product_updates": true}, {"alert_triggered": true}, {"billing_reminder": true}, {"task_completed": true, "user_id": "foreign"}} {
		adminContractResponse(t, doReq(t, e.router, http.MethodPut, path, access, input), http.StatusBadRequest)
	}
	var optedOut bool
	if err := e.pool.QueryRow(context.Background(), `SELECT notification_prefs->>'task_completed'='false' FROM users WHERE id=$1`, user["user_id"]).Scan(&optedOut); err != nil || !optedOut {
		t.Fatalf("rejected preference mutated persisted consent: %v", err)
	}
}

func TestK11PGQueuedManualPlanRetainsOriginalActor(t *testing.T) {
	for _, change := range []string{"version", "role", "disabled"} {
		t.Run(change, func(t *testing.T) {
			e, _, access, admin := k9Env(t)
			uid, tid := admin["user_id"].(string), admin["tenant_id"].(string)
			version := k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			gate, err := e.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback(context.Background())
			var pid int
			if err := gate.QueryRow(ctx, `SELECT pg_backend_pid() FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				result <- doReq(t, e.router, "POST", "/api/v1/admin/tenants/"+tid+"/plan-change", access, map[string]any{"plan_code": "pro", "reason": "queued", "expected_version": version})
			}()
			blocked := false
			for !blocked && ctx.Err() == nil {
				blocked = k9Count(t, e, `SELECT count(*) FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid))`, pid) > 0
				select {
				case w := <-result:
					t.Fatalf("queued plan completed before original actor check: %d", w.Code)
				default:
				}
				if !blocked {
					time.Sleep(5 * time.Millisecond)
				}
			}
			if !blocked {
				t.Fatal("queued plan did not reach actor row")
			}
			statement := `UPDATE users SET token_version=token_version+1 WHERE id=$1`
			if change == "role" {
				statement = `DELETE FROM platform_user_roles WHERE user_id=$1`
			} else if change == "disabled" {
				statement = `UPDATE users SET status='disabled' WHERE id=$1`
			}
			if _, err := gate.Exec(ctx, statement, uid); err != nil {
				t.Fatal(err)
			}
			if err := gate.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case w := <-result:
				if w.Code != 409 {
					t.Fatalf("revoked queued plan status=%d", w.Code)
				}
			case <-ctx.Done():
				t.Fatal("queued plan did not finish")
			}
			if k9Count(t, e, `SELECT version FROM report_credits WHERE tenant_id=$1`, tid) != version || k9Count(t, e, `SELECT count(*) FROM audit_logs WHERE action='tenant.plan_change' AND tenant_id=$1`, tid) != 0 {
				t.Fatal("queued stale actor wrote plan/version/audit")
			}
		})
	}
}
