package v1_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClosurePGPasswordUsesDurableSocketAdmission(t *testing.T) {
	e := newBillingActorPGEnv(t)
	access, _, u := mustRegister(t, e.router, "closure-budget@example.invalid", "Owner")
	k9Exec(t, e, `INSERT INTO verification_send_gates(gate_key,window_start,last_sent,sends) VALUES('confirm:ip:192.0.2.201',now(),now(),20)`)
	body, _ := json.Marshal(map[string]any{"password": "password-123456", "confirmed": true, "close_tenant_ids": []string{u["tenant_id"].(string)}})
	r := httptest.NewRequest("POST", "/api/v1/user/account-closure", strings.NewReader(string(body)))
	r.RemoteAddr = "192.0.2.201:43210"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+access)
	r.Header.Set("X-Forwarded-For", "203.0.113.201")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, r)
	adminContractResponse(t, w, 429)
	if k9Count(t, e, `SELECT count(*) FROM users WHERE id=$1 AND status='active' AND token_version=0`, u["user_id"]) != 1 {
		t.Fatal("password admission rejection changed closure identity")
	}
}

func TestClosurePGAdministratorCannotReidentifyClosedTombstone(t *testing.T) {
	e := newBillingActorPGEnv(t)
	admin, _, operator := mustRegister(t, e.router, "closure-operator@example.invalid", "Operator")
	_, _, target := mustRegister(t, e.router, "closure-tombstone@example.invalid", "Tombstone")
	k9Exec(t, e, `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, operator["user_id"])
	k9Exec(t, e, `UPDATE users SET status='closed',name='已注销用户',token_version=token_version+1 WHERE id=$1`, target["user_id"])
	path := "/api/v1/admin/users/" + target["user_id"].(string)
	adminContractResponse(t, doReq(t, e.router, "PATCH", path, admin, map[string]any{"name": "Private identity restored", "reason": "support correction", "expected_version": 0}), 409)
	adminContractResponse(t, doReq(t, e.router, "PUT", path+"/platform-role", admin, map[string]any{"platform_admin": true, "reason": "closed account must stay anonymous", "expected_version": 0}), 409)
	if k9Count(t, e, `SELECT count(*) FROM users WHERE id=$1 AND status='closed' AND name='已注销用户' AND row_version=0`, target["user_id"]) != 1 {
		t.Fatal("administrator reintroduced closed identity data")
	}
}

func TestClosurePGPendingPasswordLoginIsRestricted(t *testing.T) {
	e := newBillingActorPGEnv(t)
	oldAccess, oldRefresh, user := mustRegister(t, e.router, "closure-pending@example.invalid", "Closure owner")
	uid := user["user_id"].(string)
	k9Exec(t, e, `UPDATE users SET status='closure_pending',token_version=token_version+1,row_version=row_version+1 WHERE id=$1`, uid)
	adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/auth/me", oldAccess, nil), 401)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": oldRefresh}), 401)
	login := adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "closure-pending@example.invalid", "password": "password-123456"}), 200)
	access := login["access_token"].(string)
	principal := login["user"].(map[string]any)
	if principal["user_status"] != "closure_pending" || len(principal["roles"].([]any)) != 0 {
		t.Fatalf("pending login must produce role-free restricted principal: %v", principal)
	}
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/billing/credits", nil},
		{"GET", "/api/v1/admin/users", nil},
		{"PUT", "/api/v1/user/profile", map[string]string{"name": "Forbidden"}},
		{"PUT", "/api/v1/auth/password", map[string]string{"old_password": "password-123456", "new_password": "ForbiddenPassword123"}},
	} {
		adminContractResponse(t, doReq(t, e.router, request.method, request.path, access, request.body), 403)
	}
}

func TestClosurePGRequestWithdrawalAndFreshOrderBlockers(t *testing.T) {
	e := newBillingActorPGEnv(t)
	old, oldRefresh, user := mustRegister(t, e.router, "closure-request@example.invalid", "Closure request")
	uid, tid := user["user_id"].(string), user["tenant_id"].(string)
	preview := adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/user/account-closure/preview", old, nil), 200)
	if preview["withdrawal_days"] != float64(7) {
		t.Fatalf("withdrawal policy missing: %v", preview)
	}
	input := map[string]any{"password": "password-123456", "confirmed": true, "close_tenant_ids": []string{}}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", old, input), 409)
	input["close_tenant_ids"] = []string{tid}
	input["password"] = "wrong-password"
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", old, input), 401)
	input["password"] = "password-123456"
	created := adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", old, input), 202)
	if created["state"] != "pending" || k9Count(t, e, `SELECT count(*) FROM users WHERE id=$1 AND status='closure_pending' AND token_version=1`, uid) != 1 {
		t.Fatalf("request did not atomically restrict account: %v", created)
	}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": oldRefresh}), 401)
	login := adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "closure-request@example.invalid", "password": "password-123456"}), 200)
	pending := login["access_token"].(string)
	adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/user/account-closure/status", pending, nil), 200)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure/cancel", pending, map[string]string{"password": "password-123456"}), 200)
	adminContractResponse(t, doReq(t, e.router, "GET", "/api/v1/auth/me", pending, nil), 401)
	if k9Count(t, e, `SELECT count(*) FROM users WHERE id=$1 AND status='active' AND token_version=2`, uid) != 1 {
		t.Fatal("withdrawal did not restore only a fresh active identity")
	}
	login = adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "closure-request@example.invalid", "password": "password-123456"}), 200)
	access := login["access_token"].(string)
	k9Exec(t, e, `INSERT INTO orders(id,tenant_id,sku_code,kind,amount_cents,credits,state,channel,expires_at) VALUES('closure-unsettled',$1,'free','plan',1,1,'pending','test',now()+interval '1 hour')`, tid)
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", access, input), 409)
	if k9Count(t, e, `SELECT count(*) FROM users WHERE id=$1 AND status='active' AND token_version=2`, uid) != 1 {
		t.Fatal("blocked request changed identity")
	}
}

func TestClosurePGSharedAssetsAndLastAdminProtection(t *testing.T) {
	e := newBillingActorPGEnv(t)
	operator, _, admin := mustRegister(t, e.router, "closure-admin@example.invalid", "Sole platform admin")
	_, _, member := mustRegister(t, e.router, "closure-member@example.invalid", "Other member")
	uid, tid := admin["user_id"].(string), admin["tenant_id"].(string)
	k9Exec(t, e, `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, uid)
	input := map[string]any{"password": "password-123456", "confirmed": true, "close_tenant_ids": []string{tid}}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", operator, input), 409)
	k9Exec(t, e, `DELETE FROM platform_user_roles WHERE user_id=$1`, uid)
	k9Exec(t, e, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'analyst')`, tid, member["user_id"])
	input["close_tenant_ids"] = []string{}
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", operator, input), 409)
	k9Exec(t, e, `UPDATE tenant_members SET role='tenant_admin' WHERE tenant_id=$1 AND user_id=$2`, tid, member["user_id"])
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", operator, input), 202)
	var status string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM tenants WHERE id=$1`, tid).Scan(&status); err != nil || status != "active" {
		t.Fatal("shared tenant was changed during member closure")
	}
	if k9Count(t, e, `SELECT count(*) FROM tenant_members WHERE tenant_id=$1 AND user_id=$2 AND role='tenant_admin'`, tid, member["user_id"]) != 1 {
		t.Fatal("other member authority was changed")
	}
	adminContractResponse(t, doReq(t, e.router, http.MethodGet, "/api/v1/auth/me", operator, nil), 401)
}
