// Package v1_test holds API contract tests for the v1 HTTP handlers.
//
// These tests lock the HTTP-level response STRUCTURE the frontend depends on
// (see web/src/api/*.ts and web/src/stores/auth.tsx). Every endpoint is
// exercised through the real router with the real in-memory services wired by
// internal/app/container.go — no handler-level mocks. Endpoints that remain
// intentionally unimplemented are asserted for the consistent error envelope
// and tracked in the contractGapRegistry at the bottom of this file.
package v1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/api"
	"github.com/yuging/platform/internal/api/v1"
	"github.com/yuging/platform/internal/app"
	"github.com/yuging/platform/internal/business/analysis"
	"github.com/yuging/platform/internal/config"
	"github.com/yuging/platform/internal/pkg/llm"
	"github.com/yuging/platform/internal/platform/auth"
)

const testJWTSecret = "contract-test-secret-key-min-32-chars!!"

func init() { gin.SetMode(gin.TestMode) }

// newContractEnv builds the real router with real in-memory services and a
// discard logger (AuditLog stays silent). The returned Services handle lets
// tests seed state the same way services would (e.g. run analyses).
func newContractEnv(t *testing.T) (*gin.Engine, *v1.Services) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = testJWTSecret
	cfg.Auth.AccessTTL = "15m"
	cfg.Auth.RefreshTTL = "720h"
	cfg.RateLimit.Enabled = false
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := app.Build(cfg)
	return api.NewRouter(cfg, logger, deps), deps
}

func newContractRouter(t *testing.T) *gin.Engine {
	t.Helper()
	r, _ := newContractEnv(t)
	return r
}

// issueToken mints a real access token for the given principal.
func issueToken(t *testing.T, p auth.Principal) string {
	t.Helper()
	pair, err := auth.GenerateTokenPair(p, testJWTSecret, "15m", "720h")
	if err != nil {
		t.Fatalf("GenerateTokenPair failed: %v", err)
	}
	return pair.AccessToken
}

// doReq performs an HTTP request against the router and returns the recorder.
func doReq(t *testing.T, r *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// decodeBody parses the response body into a generic map.
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Body.Len() == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not a JSON object (status %d): %v\nbody: %s",
			w.Code, err, w.Body.String())
	}
	return m
}

// envelopeCode returns the "code" field of an error-envelope response.
func envelopeCode(m map[string]any) string {
	if s, ok := m["code"].(string); ok {
		return s
	}
	return ""
}

func principal(roles ...string) auth.Principal {
	return auth.Principal{
		UserID:       "u_contract",
		TenantID:     "t_contract",
		Email:        "contract@example.com",
		Roles:        roles,
		PlanCode:     "free",
		TenantStatus: "active",
	}
}

// mustRegister registers a fresh user through the HTTP layer and returns the
// token pair plus the decoded user object.
func mustRegister(t *testing.T, r *gin.Engine, email, name string) (access, refresh string, user map[string]any) {
	t.Helper()
	w := doReq(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"email": email, "password": "password-123456", "name": name,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("register %s status = %d, want 201\nbody: %s", email, w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	access, _ = body["access_token"].(string)
	refresh, _ = body["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatal("register response has no token pair")
	}
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatal("register response has no user object")
	}
	return access, refresh, user
}

// --- 1. GET /api/v1/health -------------------------------------------------

func TestContract_health_returnsOKStatus(t *testing.T) {
	r := newContractRouter(t)
	w := doReq(t, r, http.MethodGet, "/api/v1/health", "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/health status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	if body["status"] != "ok" {
		t.Errorf(`body["status"] = %v, want "ok"`, body["status"])
	}
}

// --- 2. Auth endpoints (register/login/refresh/me/logout) --------------------

// TestContract_authRegister_roundTrip drives a full self-registration through
// the HTTP layer and asserts the shape web/src/stores/auth.tsx consumes:
// {access_token, refresh_token, user:{user_id, tenant_id, email, roles,
// plan_code, tenant_status}}.
func TestContract_authRegister_roundTrip(t *testing.T) {
	r := newContractRouter(t)

	w := doReq(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"email": "register-flow@example.com", "password": "password-123456", "name": "注册用户",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201\nbody: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	for _, f := range []string{"access_token", "refresh_token"} {
		if s, _ := body[f].(string); s == "" {
			t.Errorf(`register response missing non-empty %q`, f)
		}
	}
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatal("register response missing user object")
	}
	for _, f := range []string{"user_id", "tenant_id", "email", "plan_code", "tenant_status"} {
		if v, ok := user[f]; !ok || v == "" {
			t.Errorf("user missing non-empty %q", f)
		}
	}
	roles, ok := user["roles"].([]any)
	if !ok || len(roles) != 1 || roles[0] != "tenant_admin" {
		t.Errorf("user roles = %v, want [tenant_admin]", user["roles"])
	}
	if user["email"] != "register-flow@example.com" {
		t.Errorf("user.email = %v", user["email"])
	}
}

// TestContract_auth_registerValidation: malformed input never reaches the
// service — thin DTO validation answers 400 BAD_REQUEST.
func TestContract_auth_registerValidation(t *testing.T) {
	r := newContractRouter(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"bad email", map[string]any{"email": "not-an-email", "password": "password-123456", "name": "n"}},
		{"short password", map[string]any{"email": "a@b.com", "password": "short", "name": "n"}},
		{"empty name", map[string]any{"email": "a@b.com", "password": "password-123456", "name": " "}},
		{"no body", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doReq(t, r, http.MethodPost, "/api/v1/auth/register", "", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("register status = %d, want 400\nbody: %s", w.Code, w.Body.String())
			}
			if code := envelopeCode(decodeBody(t, w)); code != "BAD_REQUEST" {
				t.Errorf("envelope code = %q, want BAD_REQUEST", code)
			}
		})
	}
}

// TestContract_auth_duplicateEmail_conflict: the same email cannot register
// twice (store-level ErrConflict → 409 CONFLICT).
func TestContract_auth_duplicateEmail_conflict(t *testing.T) {
	r := newContractRouter(t)
	_, _, _ = mustRegister(t, r, "dup@example.com", "第一个")
	w := doReq(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"email": "dup@example.com", "password": "password-123456", "name": "第二个",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate register status = %d, want 409\nbody: %s", w.Code, w.Body.String())
	}
	if code := envelopeCode(decodeBody(t, w)); code != "CONFLICT" {
		t.Errorf("envelope code = %q, want CONFLICT", code)
	}
}

// TestContract_auth_login_roundTrip: credentials registered over HTTP log in
// over HTTP with the same token/user shape.
func TestContract_auth_login_roundTrip(t *testing.T) {
	r := newContractRouter(t)
	_, _, user := mustRegister(t, r, "login-flow@example.com", "登录用户")

	w := doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email":    "LOGIN-FLOW@example.com", // uppercased: normalization must apply
		"password": "password-123456",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	for _, f := range []string{"access_token", "refresh_token"} {
		if s, _ := body[f].(string); s == "" {
			t.Errorf("login response missing non-empty %q", f)
		}
	}
	loginUser := body["user"].(map[string]any)
	if loginUser["user_id"] != user["user_id"] || loginUser["tenant_id"] != user["tenant_id"] {
		t.Errorf("login user %v does not match register user %v", loginUser, user)
	}
}

// TestContract_auth_login_failures: wrong credentials → 401 UNAUTHORIZED,
// malformed body → 400, unknown email → 401 (no user enumeration).
func TestContract_auth_login_failures(t *testing.T) {
	r := newContractRouter(t)
	_, _, _ = mustRegister(t, r, "login-fail@example.com", "失败用户")

	t.Run("wrong password is 401", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
			"email": "login-fail@example.com", "password": "wrong-password",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
		if code := envelopeCode(decodeBody(t, w)); code != "UNAUTHORIZED" {
			t.Errorf("code = %q, want UNAUTHORIZED", code)
		}
	})
	t.Run("unknown email is 401", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
			"email": "ghost@example.com", "password": "password-123456",
		})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
	t.Run("missing body is 400", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/auth/login", "", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})
}

// TestContract_auth_refresh_roundTrip: the refresh token (body field
// refresh_token, per the axios retry in web/src/api/client.ts) yields a fresh
// token pair.
func TestContract_auth_refresh_roundTrip(t *testing.T) {
	r := newContractRouter(t)
	_, refreshToken, _ := mustRegister(t, r, "refresh-flow@example.com", "刷新用户")

	rw := doReq(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
		"refresh_token": refreshToken,
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200\nbody: %s", rw.Code, rw.Body.String())
	}
	body := decodeBody(t, rw)
	if at, _ := body["access_token"].(string); at == "" {
		t.Error("refresh response missing access_token")
	}
	if rt, _ := body["refresh_token"].(string); rt == "" {
		t.Error("refresh response missing refresh_token")
	}

	t.Run("garbage refresh token is 401", func(t *testing.T) {
		bw := doReq(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
			"refresh_token": "not-a-real-token",
		})
		if bw.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", bw.Code)
		}
	})
	t.Run("missing refresh token is 400", func(t *testing.T) {
		bw := doReq(t, r, http.MethodPost, "/api/v1/auth/refresh", "", nil)
		if bw.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", bw.Code)
		}
	})
}

// TestContract_auth_me: GET /auth/me returns the current principal from the
// access token — the endpoint frontends use to re-validate a session.
func TestContract_auth_me(t *testing.T) {
	r := newContractRouter(t)
	tok, _, user := mustRegister(t, r, "me-flow@example.com", "我")

	w := doReq(t, r, http.MethodGet, "/api/v1/auth/me", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	if body["user_id"] != user["user_id"] || body["tenant_id"] != user["tenant_id"] {
		t.Errorf("me user_id/tenant_id mismatch: %v vs %v", body, user)
	}
	if body["email"] != "me-flow@example.com" {
		t.Errorf("me email = %v", body["email"])
	}

	t.Run("unauthenticated /me is 401", func(t *testing.T) {
		uw := doReq(t, r, http.MethodGet, "/api/v1/auth/me", "", nil)
		if uw.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", uw.Code)
		}
	})
}

// TestContract_auth_logout: logout is an authenticated no-op (stateless JWT
// MVP — client discards local tokens). 204 without body.
func TestContract_auth_logout(t *testing.T) {
	r := newContractRouter(t)
	tok, _, _ := mustRegister(t, r, "logout-flow@example.com", "登出用户")

	w := doReq(t, r, http.MethodPost, "/api/v1/auth/logout", tok, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", w.Code)
	}
	t.Run("unauthenticated logout is 401", func(t *testing.T) {
		uw := doReq(t, r, http.MethodPost, "/api/v1/auth/logout", "", nil)
		if uw.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", uw.Code)
		}
	})
}

// --- 2b. API Keys (machine credentials) --------------------------------------

// TestContract_apikeys_crudLifecycle drives create → list → use → revoke over
// HTTP: the raw pangu_ key is returned exactly once, listings carry no secret,
// the key authenticates machine calls while live, and revocation kills it.
func TestContract_apikeys_crudLifecycle(t *testing.T) {
	r := newContractRouter(t)
	// A registered tenant (not a synthetic principal): API-key auth resolves
	// the key's tenant row and fails closed when it does not exist.
	tok, _, user := mustRegister(t, r, "apikey-owner@example.com", "密钥主")
	ownerTenantID, _ := user["tenant_id"].(string)

	var keyID, rawKey string

	t.Run("create returns raw key exactly once", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/apikeys", tok, map[string]any{
			"name":   "ci-bot",
			"scopes": []string{"analyses:create", "analyses:list"},
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("POST /apikeys status = %d, want 201\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		keyID, _ = body["id"].(string)
		rawKey, _ = body["api_key"].(string)
		if keyID == "" {
			t.Error("create response missing non-empty id")
		}
		if !strings.HasPrefix(rawKey, "pangu_") {
			t.Errorf("api_key = %q, want pangu_ prefix", rawKey)
		}
		for _, f := range []string{"tenant_id", "name", "prefix", "created_at"} {
			if v, ok := body[f]; !ok || v == nil || v == "" {
				t.Errorf(`create response missing non-empty %q`, f)
			}
		}
		if body["tenant_id"] != ownerTenantID {
			t.Errorf("tenant_id = %v, want %s", body["tenant_id"], ownerTenantID)
		}
		if scopes, ok := body["scopes"].([]any); !ok || len(scopes) != 2 {
			t.Errorf(`scopes = %v, want the two requested`, body["scopes"])
		}
	})

	t.Run("list never leaks secrets", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/apikeys", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /apikeys status = %d, want 200\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		keys, _ := body["keys"].([]any)
		if len(keys) != 1 {
			t.Fatalf("keys count = %d, want 1", len(keys))
		}
		row := keys[0].(map[string]any)
		if row["id"] != keyID || row["prefix"] == "" {
			t.Errorf("listed row = %v, want id=%s with display prefix", row, keyID)
		}
		if strings.Contains(w.Body.String(), rawKey) {
			t.Error("list body contains the raw key")
		}
		if strings.Contains(w.Body.String(), "key_hash") {
			t.Error("list body exposes the key hash field")
		}
	})

	t.Run("live key authenticates machine calls", func(t *testing.T) {
		lw := doReq(t, r, http.MethodGet, "/api/v1/analyses", rawKey, nil)
		if lw.Code != http.StatusOK {
			t.Fatalf("GET /analyses with api key status = %d, want 200", lw.Code)
		}
		cw := doReq(t, r, http.MethodPost, "/api/v1/analyses", rawKey, analysisRequest())
		if cw.Code != http.StatusCreated {
			t.Fatalf("POST /analyses with api key status = %d, want 201\nbody: %s", cw.Code, cw.Body.String())
		}
	})

	t.Run("revoke kills the key", func(t *testing.T) {
		w := doReq(t, r, http.MethodDelete, "/api/v1/apikeys/"+keyID, tok, nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("DELETE /apikeys/:id status = %d, want 204", w.Code)
		}
		gw := doReq(t, r, http.MethodGet, "/api/v1/analyses", rawKey, nil)
		if gw.Code != http.StatusUnauthorized {
			t.Fatalf("revoked key status = %d, want 401", gw.Code)
		}
		if code := envelopeCode(decodeBody(t, gw)); code != "UNAUTHORIZED" {
			t.Errorf("code = %q, want UNAUTHORIZED", code)
		}
		lw := doReq(t, r, http.MethodGet, "/api/v1/apikeys", tok, nil)
		lrow := decodeBody(t, lw)["keys"].([]any)[0].(map[string]any)
		if lrow["revoked_at"] == nil || lrow["revoked_at"] == "" {
			t.Error("revoked key missing revoked_at in listing")
		}
	})

	t.Run("validation and auth failures", func(t *testing.T) {
		nw := doReq(t, r, http.MethodPost, "/api/v1/apikeys", tok, map[string]any{"scopes": []string{"x"}})
		if nw.Code != http.StatusBadRequest {
			t.Fatalf("create without name status = %d, want 400", nw.Code)
		}
		uw := doReq(t, r, http.MethodDelete, "/api/v1/apikeys/does-not-exist", tok, nil)
		if uw.Code != http.StatusNotFound {
			t.Fatalf("revoke unknown id status = %d, want 404", uw.Code)
		}
	})
}

// TestContract_apikeys_rbac: key management needs apikeys:manage; viewers are
// rejected and unauthenticated requests never reach the handler.
func TestContract_apikeys_rbac(t *testing.T) {
	r := newContractRouter(t)
	viewer := issueToken(t, principal("viewer"))

	for _, m := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/apikeys"},
		{http.MethodGet, "/api/v1/apikeys"},
		{http.MethodDelete, "/api/v1/apikeys/k1"},
	} {
		w := doReq(t, r, m.method, m.path, viewer, nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s status = %d, want 403", m.method, m.path, w.Code)
		}
	}
	aw := doReq(t, r, http.MethodGet, "/api/v1/apikeys", "", nil)
	if aw.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET /apikeys status = %d, want 401", aw.Code)
	}
}

// TestContract_apikeys_crossTenantIsolation: tenant B can neither see nor
// revoke tenant A's keys — foreign ids answer 404, not 403.
func TestContract_apikeys_crossTenantIsolation(t *testing.T) {
	r := newContractRouter(t)
	tokA := issueToken(t, principal("tenant_admin"))
	tokB := issueToken(t, auth.Principal{
		UserID: "u_other", TenantID: "t_other", Email: "other@example.com",
		Roles: []string{"tenant_admin"}, PlanCode: "free", TenantStatus: "active",
	})

	cw := doReq(t, r, http.MethodPost, "/api/v1/apikeys", tokA, map[string]any{"name": "owned-by-a"})
	if cw.Code != http.StatusCreated {
		t.Fatalf("A create status = %d, want 201", cw.Code)
	}
	keyID := decodeBody(t, cw)["id"].(string)

	lw := doReq(t, r, http.MethodGet, "/api/v1/apikeys", tokB, nil)
	if keys := decodeBody(t, lw)["keys"].([]any); len(keys) != 0 {
		t.Errorf("B sees %d keys, want 0 (tenant isolation)", len(keys))
	}
	dw := doReq(t, r, http.MethodDelete, "/api/v1/apikeys/"+keyID, tokB, nil)
	if dw.Code != http.StatusNotFound {
		t.Errorf("B revoking A's key status = %d, want 404", dw.Code)
	}
}

// --- 3. Analyses -------------------------------------------------------------

// analysisRequest is a valid create payload the frontend sends.
func analysisRequest() map[string]any {
	return map[string]any{
		"name":          "雅阁后排舆情",
		"analysis_type": "sentiment",
		"keywords":      []string{"雅阁后排"},
		"sources":       []string{"weibo", "wechat"},
	}
}

// seedAnalysis creates one analysis for tenant t_contract via the service
// (as a worker/queue would) and returns its ID.
func seedAnalysis(t *testing.T, deps *v1.Services, name string) string {
	t.Helper()
	a, err := deps.Analysis.Create(context.Background(), analysis.CreateAnalysisRequest{
		TenantID:     "t_contract",
		UserID:       "u_contract",
		Name:         name,
		AnalysisType: "sentiment",
		Keywords:     []string{"雅阁后排"},
		Sources:      []string{"weibo"},
	})
	if err != nil {
		t.Fatalf("seed analysis failed: %v", err)
	}
	return a.ID
}

// TestContract_analyses_createGetListCancelRerun drives one analysis through
// its whole lifecycle over HTTP with the real analysis service.
func TestContract_analyses_createGetListCancelRerun(t *testing.T) {
	r, deps := newContractEnv(t)
	tok := issueToken(t, principal("analyst")) // tenant t_contract

	createdID := seedAnalysis(t, deps, "种子分析")

	t.Run("create returns a full AnalysisSummary with progress", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/analyses", tok, analysisRequest())
		if w.Code != http.StatusCreated {
			t.Fatalf("POST /analyses status = %d, want 201\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		for _, f := range []string{"id", "name", "analysis_type", "state", "progress", "created_at"} {
			if v, ok := body[f]; !ok || v == nil {
				t.Errorf(`create response missing field %q — AnalysisSummary requires it`, f)
			}
		}
		if body["state"] != "queued" {
			t.Errorf("state = %v, want queued", body["state"])
		}
		if body["progress"] != float64(0) {
			t.Errorf("progress = %v, want 0", body["progress"])
		}
	})

	t.Run("list returns created analyses", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/analyses", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /analyses status = %d, want 200", w.Code)
		}
		body := decodeBody(t, w)
		analyses, _ := body["analyses"].([]any)
		if len(analyses) < 2 {
			t.Errorf("analyses count = %d, want ≥ 2 (seeded + created)", len(analyses))
		}
		if total, _ := body["total"].(float64); total != float64(len(analyses)) {
			t.Errorf("total = %v, want %d", total, len(analyses))
		}
		if body["tenant_id"] != "t_contract" {
			t.Errorf("tenant_id = %v, want t_contract", body["tenant_id"])
		}
	})

	t.Run("detail returns the seeded analysis state", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/analyses/"+createdID, tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /analyses/:id status = %d, want 200", w.Code)
		}
		body := decodeBody(t, w)
		if body["id"] != createdID {
			t.Errorf("id = %v, want %s", body["id"], createdID)
		}
		for _, f := range []string{"name", "state", "progress"} {
			if v, ok := body[f]; !ok || v == nil {
				t.Errorf("detail missing %q — AnalysisStateResponse requires it", f)
			}
		}
		if body["state"] != "queued" {
			t.Errorf("state = %v, want queued (no hardcoded draft)", body["state"])
		}
	})

	t.Run("unknown analysis id is 404", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/analyses/does-not-exist", tok, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
		if code := envelopeCode(decodeBody(t, w)); code != "NOT_FOUND" {
			t.Errorf("code = %q, want NOT_FOUND", code)
		}
	})

	t.Run("cancel transitions queued → canceled", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/analyses/"+createdID+"/cancel", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("cancel status = %d, want 200\nbody: %s", w.Code, w.Body.String())
		}
		if body := decodeBody(t, w); body["state"] != "canceled" {
			t.Errorf("state after cancel = %v, want canceled", body["state"])
		}
	})

	t.Run("rerun requeues a canceled analysis", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/analyses/"+createdID+"/rerun", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("rerun status = %d, want 200\nbody: %s", w.Code, w.Body.String())
		}
		if body := decodeBody(t, w); body["state"] != "queued" {
			t.Errorf("state after rerun = %v, want queued", body["state"])
		}
	})

	t.Run("cancel of unknown id is 404", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/analyses/nope/cancel", tok, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
}

// TestContract_analyses_resultShape: the result endpoint answers with the
// AnalysisResult envelope incl. topics ([] until the documents store lands).
func TestContract_analyses_resultShape(t *testing.T) {
	r, deps := newContractEnv(t)
	tok := issueToken(t, principal("analyst"))
	id := seedAnalysis(t, deps, "结果分析")

	w := doReq(t, r, http.MethodGet, "/api/v1/analyses/"+id+"/result", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /analyses/:id/result status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	if _, ok := body["documents"].([]any); !ok {
		t.Error(`"documents" is not an array — AnalysisResult.documents`)
	}
	if s, ok := body["sentiments"].(map[string]any); !ok {
		t.Error(`"sentiments" missing — AnalysisResult.sentiments`)
	} else {
		for _, f := range []string{"positive", "negative", "neutral"} {
			if _, ok := s[f]; !ok {
				t.Errorf("sentiments.%s missing", f)
			}
		}
	}
	if topics, ok := body["topics"].([]any); !ok {
		t.Error(`"topics" missing — AnalysisResult.topics is required`)
	} else if len(topics) != 0 {
		t.Errorf("topics = %v, want [] until the documents store lands", topics)
	}
}

// --- SSE (GET /analyses/:id/events) -------------------------------------------

// sseRecorder is a flush-capable recorder whose body is safe to read from
// another goroutine while the handler is streaming (disconnect test).
type sseRecorder struct {
	mu     sync.Mutex
	code   int
	header http.Header
	body   bytes.Buffer
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{code: http.StatusOK, header: http.Header{}}
}

func (rec *sseRecorder) Header() http.Header { return rec.header }

func (rec *sseRecorder) Write(b []byte) (int, error) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.body.Write(b)
}

func (rec *sseRecorder) WriteHeader(status int) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.code = status
}

func (rec *sseRecorder) Flush() {}

func (rec *sseRecorder) snapshot() (int, string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.code, rec.body.String()
}

// waitFor polls the recorder body until it contains want or the deadline hits.
func waitFor(t *testing.T, rec *sseRecorder, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, body := rec.snapshot()
		if strings.Contains(body, want) {
			return body
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, body := rec.snapshot()
	t.Fatalf("timed out waiting for %q in SSE stream\ngot: %q", want, body)
	return body
}

// advanceToCompleted walks a queued analysis to the completed terminal state
// through the real state machine (the worker's Transition write path).
func advanceToCompleted(t *testing.T, deps *v1.Services, tenantID, id string) {
	t.Helper()
	ctx := context.Background()
	for _, st := range []string{"acquiring_budget", "fetching", "analyzing", "generating_report", "completed"} {
		if err := deps.Analysis.Transition(ctx, tenantID, id, st); err != nil {
			t.Fatalf("transition %s: %v", st, err)
		}
	}
}

// TestContract_analyses_events_completedStream: a terminal analysis gets one
// final event over the text/event-stream channel and the connection closes
// (handler returns — the recorder completes without any cancellation).
func TestContract_analyses_events_completedStream(t *testing.T) {
	r, deps := newContractEnv(t)
	tok := issueToken(t, principal("analyst"))
	id := seedAnalysis(t, deps, "已完成流")
	advanceToCompleted(t, deps, "t_contract", id)

	w := doReq(t, r, http.MethodGet, "/api/v1/analyses/"+id+"/events", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("events status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: final") {
		t.Errorf("missing final event:\n%s", body)
	}
	if !strings.Contains(body, `"state":"completed"`) {
		t.Errorf("final event missing completed state:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("SSE frames must end with a blank line:\n%q", body)
	}
}

// TestContract_analyses_events_progressStream: an in-flight analysis streams
// progress frames and closes with a final frame when the pipeline completes.
func TestContract_analyses_events_progressStream(t *testing.T) {
	r, deps := newContractEnv(t)
	deps.SSEPollInterval = 10 * time.Millisecond
	tok := issueToken(t, principal("analyst"))
	id := seedAnalysis(t, deps, "进行中流")

	rec := newSSERecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analyses/"+id+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(rec, req)
	}()

	body := waitFor(t, rec, "event: progress")
	if !strings.Contains(body, `"state":"queued"`) {
		t.Errorf("initial frame should carry the current state:\n%s", body)
	}

	advanceToCompleted(t, deps, "t_contract", id)
	body = waitFor(t, rec, "event: final")
	if !strings.Contains(body, `"state":"completed"`) {
		t.Errorf("final frame missing completed state:\n%s", body)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not close the stream after the terminal event")
	}
}

// TestContract_analyses_events_clientDisconnect: canceling the request
// context (client hung up) must stop the polling loop promptly.
func TestContract_analyses_events_clientDisconnect(t *testing.T) {
	r, deps := newContractEnv(t)
	deps.SSEPollInterval = 10 * time.Millisecond
	tok := issueToken(t, principal("analyst"))
	id := seedAnalysis(t, deps, "断开流")

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analyses/"+id+"/events", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := newSSERecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(rec, req)
	}()

	waitFor(t, rec, "event: progress") // stream is live
	cancel()                            // client disconnects

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler kept polling after the client disconnected")
	}
}

// TestContract_analyses_events_guards: unknown ids answer the 404 JSON
// envelope (no SSE headers), and a role without analyses:list is refused
// before the handler runs.
func TestContract_analyses_events_guards(t *testing.T) {
	r, deps := newContractEnv(t)
	analyst := issueToken(t, principal("analyst"))

	w := doReq(t, r, http.MethodGet, "/api/v1/analyses/no-such-id/events", analyst, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		t.Error("404 must not open an SSE stream")
	}

	guest := issueToken(t, principal("guest")) // role with no permissions at all
	gw := doReq(t, r, http.MethodGet, "/api/v1/analyses/whatever/events", guest, nil)
	if gw.Code != http.StatusForbidden {
		t.Fatalf("permissionless role status = %d, want 403", gw.Code)
	}
	_ = deps
}

// --- 4. Dashboard (wired to the live dashboard service) ----------------------

// TestContract_dashboard_endpoints_real: every dashboard endpoint answers 200
// with the documented frontend field shapes (web/src/api/dashboard.ts).
func TestContract_dashboard_endpoints_real(t *testing.T) {
	r, deps := newContractEnv(t)
	tok := issueToken(t, principal("analyst"))
	seedAnalysis(t, deps, "看板分析")

	t.Run("overview", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/dashboard/overview", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		for _, f := range []string{"total_analyses", "total_docs", "sentiment_pos", "sentiment_neg", "sentiment_neu", "success_rate", "active_tasks"} {
			if _, ok := body[f]; !ok {
				t.Errorf("overview missing %q — DashboardOverview requires it", f)
			}
		}
		if body["total_analyses"] != float64(1) {
			t.Errorf("total_analyses = %v, want 1", body["total_analyses"])
		}
	})
	t.Run("trend", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/dashboard/trend", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		body := decodeBody(t, w)
		if _, ok := body["dates"].([]any); !ok {
			t.Error(`"dates" is not an array`)
		}
		if _, ok := body["counts"].([]any); !ok {
			t.Error(`"counts" is not an array`)
		}
		if scores, ok := body["scores"].([]any); !ok {
			t.Error(`"scores" is not an array`)
		} else if len(scores) != len(body["dates"].([]any)) {
			t.Errorf("len(scores)=%d, len(dates)=%d — must be aligned", len(scores), len(body["dates"].([]any)))
		}
	})
	t.Run("sources", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/dashboard/sources", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		rows, _ := decodeBody(t, w)["sources"].([]any)
		if len(rows) == 0 {
			t.Fatal("sources empty")
		}
		for _, row := range rows {
			m := row.(map[string]any)
			for _, f := range []string{"name", "count", "pct"} {
				if _, ok := m[f]; !ok {
					t.Errorf("source row missing %q", f)
				}
			}
		}
	})
	t.Run("topics", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/dashboard/topics", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		topics, _ := decodeBody(t, w)["topics"].([]any)
		if len(topics) == 0 {
			t.Fatal("topics empty")
		}
		for _, tp := range topics {
			m := tp.(map[string]any)
			for _, f := range []string{"name", "doc_count", "trend"} {
				if _, ok := m[f]; !ok {
					t.Errorf("topic row missing %q", f)
				}
			}
		}
	})
	t.Run("alerts", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/dashboard/alerts", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if alerts, _ := decodeBody(t, w)["alerts"].([]any); alerts == nil {
			t.Error(`"alerts" is not an array`)
		}
	})
}

// --- 5. Billing --------------------------------------------------------------

// TestContract_billingPlans_frontendFields: plan rows carry the frontend
// contract {code, name, price_monthly_cny, token_quota_m} for the four tiers.
func TestContract_billingPlans_frontendFields(t *testing.T) {
	r := newContractRouter(t)
	tok := issueToken(t, principal("tenant_admin"))

	w := doReq(t, r, http.MethodGet, "/api/v1/billing/plans", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /billing/plans status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	plans, ok := body["plans"].([]any)
	if !ok {
		t.Fatalf(`body["plans"] = %T, want array`, body["plans"])
	}
	seen := map[string]bool{}
	for _, p := range plans {
		pm, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("plan element = %T, want object", p)
		}
		code, _ := pm["code"].(string)
		if code == "" {
			t.Error("plan element missing non-empty code")
		}
		seen[code] = true
		for _, f := range []string{"name", "price_monthly_cny", "token_quota_m"} {
			if _, ok := pm[f]; !ok {
				t.Errorf(`plan %q missing %q — PlanSelectionPage Plan requires it`, code, f)
			}
		}
		if price, ok := pm["price_monthly_cny"].(float64); !ok || price < 0 {
			t.Errorf("plan %q price_monthly_cny = %v, want non-negative number", code, pm["price_monthly_cny"])
		}
	}
	for _, want := range []string{"free", "pro", "business", "enterprise"} {
		if !seen[want] {
			t.Errorf("plans missing tier %q", want)
		}
	}
}

// --- 6. Reports --------------------------------------------------------------

// TestContract_reports_realStore: list/detail/download run against the real
// report store — unknown ids 404 instead of canned rows.
func TestContract_reports_realStore(t *testing.T) {
	r := newContractRouter(t)
	tok := issueToken(t, principal("analyst"))

	t.Run("list", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/reports", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /reports status = %d, want 200", w.Code)
		}
		body := decodeBody(t, w)
		reports, _ := body["reports"].([]any)
		if reports == nil {
			t.Error(`body["reports"] is not an array`)
		}
		if _, ok := body["total"]; !ok {
			t.Error(`body["total"] missing`)
		}
	})
	t.Run("detail of unknown report is 404", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/reports/r-does-not-exist", tok, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
	t.Run("download of unknown report is 404", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/reports/r-does-not-exist/download", tok, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
	t.Run("templates", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/reports/templates", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /reports/templates status = %d, want 200", w.Code)
		}
		templates, _ := decodeBody(t, w)["templates"].([]any)
		if len(templates) == 0 {
			t.Fatal("templates empty")
		}
		for _, tp := range templates {
			if id, _ := tp.(map[string]any)["id"].(string); id == "" {
				t.Error("template missing id")
			}
		}
	})
}

// --- 7. Authz: unauthenticated + RBAC ----------------------------------------

// TestContract_protectedEndpoint_withoutToken_401Envelope covers the surface
// of protected endpoints behind AuthRequired.
func TestContract_protectedEndpoint_withoutToken_401Envelope(t *testing.T) {
	r := newContractRouter(t)
	for _, path := range []string{
		"/api/v1/analyses",
		"/api/v1/apikeys",
		"/api/v1/dashboard/overview",
		"/api/v1/billing/plans",
		"/api/v1/admin/tenants",
		"/api/v1/reports",
	} {
		t.Run(path, func(t *testing.T) {
			w := doReq(t, r, http.MethodGet, path, "", nil)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("GET %s without token status = %d, want 401", path, w.Code)
			}
			body := decodeBody(t, w)
			if envelopeCode(body) != "UNAUTHORIZED" {
				t.Errorf(`code = %q, want "UNAUTHORIZED"`, envelopeCode(body))
			}
			if w.Header().Get("X-Request-Id") == "" {
				t.Error("X-Request-Id header missing on error response")
			}
		})
	}
}

// TestContract_rbac_viewerCannotCreateAnalysis_403: viewer has analyses:list
// but not analyses:create.
func TestContract_rbac_viewerCannotCreateAnalysis_403(t *testing.T) {
	r := newContractRouter(t)
	tok := issueToken(t, principal("viewer"))

	w := doReq(t, r, http.MethodPost, "/api/v1/analyses", tok, analysisRequest())
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer POST /analyses status = %d, want 403", w.Code)
	}
	body := decodeBody(t, w)
	if envelopeCode(body) != "FORBIDDEN" {
		t.Errorf(`code = %q, want "FORBIDDEN"`, envelopeCode(body))
	}
}

// TestContract_rbac_viewerCanListAnalyses: viewer keeps its granted permission.
func TestContract_rbac_viewerCanListAnalyses_200(t *testing.T) {
	r := newContractRouter(t)
	tok := issueToken(t, principal("viewer"))

	w := doReq(t, r, http.MethodGet, "/api/v1/analyses", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("viewer GET /analyses status = %d, want 200", w.Code)
	}
}

// --- 8. Admin (RBAC-guarded) -------------------------------------------------

// TestContract_adminGroup_rbac: /admin/* requires platform-admin permissions;
// a viewer token is rejected with 403 FORBIDDEN before the handler runs.
func TestContract_adminGroup_rbac(t *testing.T) {
	r := newContractRouter(t)
	viewer := issueToken(t, principal("viewer"))
	admin := issueToken(t, principal("platform_admin"))

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/tenants"},
		{http.MethodPost, "/api/v1/admin/tenants/t1/suspend"},
		{http.MethodPost, "/api/v1/admin/tenants/t1/resume"},
		{http.MethodGet, "/api/v1/admin/usage"},
		{http.MethodGet, "/api/v1/admin/plans"},
		{http.MethodPost, "/api/v1/admin/plans"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+strings.TrimPrefix(tt.path, "/api/v1/admin/"), func(t *testing.T) {
			w := doReq(t, r, tt.method, tt.path, viewer, nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("viewer %s %s status = %d, want 403 FORBIDDEN", tt.method, tt.path, w.Code)
			}
			if code := envelopeCode(decodeBody(t, w)); code != "FORBIDDEN" {
				t.Errorf("code = %q, want FORBIDDEN", code)
			}
			// Same request with an admin token must NOT be blocked by RBAC.
			aw := doReq(t, r, tt.method, tt.path, admin, nil)
			if aw.Code == http.StatusForbidden {
				t.Errorf("platform_admin %s %s rejected by RBAC", tt.method, tt.path)
			}
		})
	}
}

// TestContract_admin_tenantLifecycle: a registered tenant is visible to the
// admin list, can be suspended and resumed (shared platform tenants store).
func TestContract_admin_tenantLifecycle(t *testing.T) {
	r := newContractRouter(t)
	admin := issueToken(t, principal("platform_admin"))
	_, _, user := mustRegister(t, r, "admin-sees-me@example.com", "被管理租户")

	w := doReq(t, r, http.MethodGet, "/api/v1/admin/tenants", admin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/tenants status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	tenants, _ := body["tenants"].([]any)
	var tenantID string
	found := false
	for _, tn := range tenants {
		m := tn.(map[string]any)
		if m["id"] == user["tenant_id"] {
			found = true
			tenantID = m["id"].(string)
			if m["status"] != "active" || m["plan_code"] != "free" {
				t.Errorf("registered tenant row = %v, want active/free", m)
			}
		}
	}
	if !found {
		t.Fatalf("registered tenant %v not present in admin list %v", user["tenant_id"], tenants)
	}

	t.Run("suspend", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/suspend", admin, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("suspend status = %d, want 200\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		if body["status"] != "suspended" {
			t.Errorf("status = %v, want suspended", body["status"])
		}
	})
	t.Run("suspend again conflicts", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/suspend", admin, nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("double suspend status = %d, want 409", w.Code)
		}
	})
	t.Run("resume", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/resume", admin, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("resume status = %d, want 200\nbody: %s", w.Code, w.Body.String())
		}
		if body := decodeBody(t, w); body["status"] != "active" {
			t.Errorf("status = %v, want active", body["status"])
		}
	})
	t.Run("suspend unknown tenant is 404", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/admin/tenants/ghost/suspend", admin, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
}

// TestContract_adminPlans_readOnly: plan listing is real data from the billing
// package; creating plans is pending a plan-management store (501 envelope).
func TestContract_adminPlans_readOnly(t *testing.T) {
	r := newContractRouter(t)
	admin := issueToken(t, principal("platform_admin"))

	w := doReq(t, r, http.MethodGet, "/api/v1/admin/plans", admin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/plans status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	plans, _ := body["plans"].([]any)
	if len(plans) != 4 {
		t.Errorf("len(plans) = %d, want 4", len(plans))
	}

	cw := doReq(t, r, http.MethodPost, "/api/v1/admin/plans", admin, map[string]any{"code": "custom"})
	if cw.Code != http.StatusNotImplemented {
		t.Fatalf("POST /admin/plans status = %d, want 501 (plan store pending)", cw.Code)
	}
	if code := envelopeCode(decodeBody(t, cw)); code != "NOT_IMPLEMENTED" {
		t.Errorf("code = %q, want NOT_IMPLEMENTED", code)
	}
}

// TestContract_adminUsage_aggregatesRealData: platform rollup sums tenants,
// active tenants, meter tokens and analyses from the live stores.
func TestContract_adminUsage_aggregatesRealData(t *testing.T) {
	r, deps := newContractEnv(t)
	admin := issueToken(t, principal("platform_admin"))
	_, _, user := mustRegister(t, r, "usage-target@example.com", "用量租户")
	usageTenant, _ := user["tenant_id"].(string)

	ctx := context.Background()
	// 400 prompt + 80 completion = 480 tokens spent for the registered tenant.
	if err := deps.Usage.Record(ctx, llm.UsageEvent{
		TenantID: usageTenant, PromptTokens: 400, CompletionTokens: 80,
	}); err != nil {
		t.Fatalf("meter record: %v", err)
	}
	if _, err := deps.Analysis.Create(ctx, analysis.CreateAnalysisRequest{
		TenantID: usageTenant, UserID: "u_seed", Name: "计入用量", AnalysisType: "sentiment",
	}); err != nil {
		t.Fatalf("create analysis: %v", err)
	}

	w := doReq(t, r, http.MethodGet, "/api/v1/admin/usage", admin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/usage status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	for _, f := range []string{"total_tenants", "active_tenants", "total_tokens_used", "total_analyses"} {
		if _, ok := body[f].(float64); !ok {
			t.Errorf("%q = %v, want number", f, body[f])
		}
	}
	if body["total_tenants"] != float64(1) {
		t.Errorf("total_tenants = %v, want 1", body["total_tenants"])
	}
	if body["active_tenants"] != float64(1) {
		t.Errorf("active_tenants = %v, want 1", body["active_tenants"])
	}
	if body["total_tokens_used"] != float64(480) {
		t.Errorf("total_tokens_used = %v, want 480", body["total_tokens_used"])
	}
	if body["total_analyses"] != float64(1) {
		t.Errorf("total_analyses = %v, want 1", body["total_analyses"])
	}
}

// --- Stub endpoints always answer with a consistent JSON envelope ------------

// TestContract_stubEndpoints_jsonEnvelope sweeps every remaining
// NOT_IMPLEMENTED stub: JSON envelope with code + message, never HTML.
func TestContract_stubEndpoints_jsonEnvelope(t *testing.T) {
	r := newContractRouter(t)
	tok := issueToken(t, principal("platform_admin"))
	endpoints := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/billing/invoices/i1/download", nil},
	}
	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			w := doReq(t, r, ep.method, ep.path, tok, ep.body)
			body := decodeBody(t, w)
			if envelopeCode(body) != "NOT_IMPLEMENTED" {
				t.Errorf("code = %q, want NOT_IMPLEMENTED", envelopeCode(body))
			}
			if _, ok := body["message"]; !ok {
				t.Error("stub envelope missing message field")
			}
		})
	}
}

// --- Contract gap registry (feeds REVIEW_REPORT.md) --------------------------

// contractGap is one endpoint-level mismatch between today's router behavior
// and the frontend contract in web/src/api/*.ts.
type contractGap struct {
	endpoint string // method + path
	severity string // BLOCKER | MAJOR | MINOR
	status   string // STUB | MISMATCH | SECURITY | MISSING_FIELDS | PENDING
	detail   string
}

// contractGapRegistry is the single source of truth for contract gaps that
// remain open after the dev-director review pass. Closed gaps (auth wiring,
// dashboard, RBAC, billing field alignment, analyses lifecycle) are listed in
// platform/REVIEW_REPORT.md.
var contractGapRegistry = []contractGap{
	{
		endpoint: "GET /api/v1/analyses/:id/result",
		severity: "MAJOR", status: "PENDING",
		detail: "documents/sentiments/topics return empty arrays — real aggregation waits for the documents store + engine pipeline",
	},
	{
		endpoint: "POST /api/v1/admin/plans",
		severity: "MINOR", status: "STUB",
		detail: "creating custom plans needs a plan-management store",
	},
	{
		endpoint: "GET /api/v1/billing/invoices/:id/download",
		severity: "MINOR", status: "STUB",
		detail: "invoice file storage + download pending",
	},
	{
		endpoint: "GET /api/v1/billing/{subscription,usage}",
		severity: "MINOR", status: "PENDING",
		detail: "placeholder rows — real values need the subscription store and the shared usage meter",
	},
	{
		endpoint: "GET /api/v1/reports/:id/download",
		severity: "MINOR", status: "PENDING",
		detail: "returns the gated download_url; actual file bytes wait for the storage driver",
	},
}

// TestContract_gapRegistry_summary logs the remaining gap registry so `go test
// -v` output documents the backlog. Registry doubles as REVIEW_REPORT.md input.
func TestContract_gapRegistry_summary(t *testing.T) {
	t.Logf("contract gap registry: %d open gaps across v1 endpoints", len(contractGapRegistry))
	for _, g := range contractGapRegistry {
		t.Logf("  [%s/%s] %s — %s", g.severity, g.status, g.endpoint, g.detail)
	}
	// Sanity: every entry carries a severity we know how to prioritize.
	for _, g := range contractGapRegistry {
		switch g.severity {
		case "BLOCKER", "MAJOR", "MINOR", "SECURITY":
		default:
			t.Errorf("gap %s has unknown severity %q", g.endpoint, g.severity)
		}
	}
}
