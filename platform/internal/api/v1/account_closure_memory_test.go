package v1_test

import "testing"

func TestClosureMemoryRequestWithdrawalRevokesOldVersions(t *testing.T) {
	router, _ := newContractEnv(t)
	access, refresh, user := mustRegister(t, router, "closure-memory@example.invalid", "Memory owner")
	adminContractResponse(t, doReq(t, router, "GET", "/api/v1/user/account-closure/preview", access, nil), 200)
	input := map[string]any{"password": "password-123456", "confirmed": true, "close_tenant_ids": []string{user["tenant_id"].(string)}}
	adminContractResponse(t, doReq(t, router, "POST", "/api/v1/user/account-closure", access, input), 202)
	adminContractResponse(t, doReq(t, router, "POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refresh}), 401)
	login := adminContractResponse(t, doReq(t, router, "POST", "/api/v1/auth/login", "", map[string]string{"email": "closure-memory@example.invalid", "password": "password-123456"}), 200)
	pending := login["access_token"].(string)
	adminContractResponse(t, doReq(t, router, "GET", "/api/v1/billing/credits", pending, nil), 403)
	adminContractResponse(t, doReq(t, router, "POST", "/api/v1/user/account-closure/cancel", pending, map[string]string{"password": "password-123456"}), 200)
	adminContractResponse(t, doReq(t, router, "GET", "/api/v1/auth/me", pending, nil), 401)
}
