package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/platform/apikey"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// stubTenants is a TenantLookup double.
type stubTenants struct {
	status   string
	notFound bool
}

func (s stubTenants) Get(_ context.Context, id string) (*tenant.Tenant, error) {
	if s.notFound {
		return nil, &notFoundErr{}
	}
	return &tenant.Tenant{ID: id, Status: tenant.Status(s.status), PlanCode: "pro"}, nil
}

type notFoundErr struct{}

func (e *notFoundErr) Error() string { return "tenant not found" }

// newAPIKeyFixture returns a service with one live key and one revoked key.
func newAPIKeyFixture(t *testing.T) (*apikey.Service, string, string) {
	t.Helper()
	svc := apikey.NewService(apikey.NewMemoryStore())
	_, rawLive, err := svc.CreateKey(context.Background(), "t_key", "ci", nil)
	if err != nil {
		t.Fatal(err)
	}
	revoked, rawRevoked, err := svc.CreateKey(context.Background(), "t_key", "old", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeKey(context.Background(), "t_key", revoked.ID); err != nil {
		t.Fatal(err)
	}
	_ = rawRevoked
	return svc, rawLive, rawRevoked
}

func keyRouter(svc *apikey.Service, tenants TenantLookup) *gin.Engine {
	r := gin.New()
	r.GET("/whoami", ApiKeyAuth(svc, tenants), func(c *gin.Context) {
		p := GetPrincipal(c)
		c.JSON(200, gin.H{
			"user_id":       p.UserID,
			"tenant_id":     p.TenantID,
			"roles":         p.Roles,
			"plan_code":     p.PlanCode,
			"tenant_status": p.TenantStatus,
		})
	})
	return r
}

func doBearer(t *testing.T, r *gin.Engine, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/whoami", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestApiKeyAuth_validKey_injectsPrincipal(t *testing.T) {
	svc, raw, _ := newAPIKeyFixture(t)
	w := doBearer(t, keyRouter(svc, stubTenants{status: "active"}), raw)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["tenant_id"] != "t_key" {
		t.Errorf("tenant_id = %v, want t_key", body["tenant_id"])
	}
	uid, _ := body["user_id"].(string)
	if uid == "" || uid[:7] != "apikey:" {
		t.Errorf("user_id = %v, want apikey:<id>", body["user_id"])
	}
	if body["plan_code"] != "pro" {
		t.Errorf("plan_code = %v, want pro (resolved from tenant)", body["plan_code"])
	}
	roles, _ := body["roles"].([]any)
	if len(roles) != 1 || roles[0] != "api_service" {
		t.Errorf("roles = %v, want [api_service]", body["roles"])
	}
}

func TestApiKeyAuth_rejections(t *testing.T) {
	svc, rawLive, rawRevoked := newAPIKeyFixture(t)
	cases := []struct {
		name    string
		svc     *apikey.Service
		tenants TenantLookup
		token   string
		status  int
	}{
		{"missing header", svc, stubTenants{status: "active"}, "", 401},
		{"garbage non-prefixed", svc, stubTenants{status: "active"}, "not-a-pangu-key", 401},
		{"unknown pangu key", svc, stubTenants{status: "active"}, "pangu_0123456789ABCDEFGHJKMNPQRS", 401},
		{"revoked key", svc, stubTenants{status: "active"}, rawRevoked, 401},
		{"unknown tenant", svc, stubTenants{notFound: true}, rawLive, 401},
		{"suspended tenant", svc, stubTenants{status: "suspended"}, rawLive, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doBearer(t, keyRouter(tc.svc, tc.tenants), tc.token)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d\nbody: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}

func TestApiKeyAuth_requiresBearerScheme(t *testing.T) {
	svc, raw, _ := newAPIKeyFixture(t)
	req := httptest.NewRequest("GET", "/whoami", nil)
	req.Header.Set("Authorization", "Token "+raw)
	w := httptest.NewRecorder()
	keyRouter(svc, stubTenants{status: "active"}).ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("non-bearer scheme status = %d, want 401", w.Code)
	}
}

// --- AuthAny: JWT or API key on the same group --------------------------------

const testSecret = "middleware-test-secret-key-min-32ch!"

// mustJWT mints a regular user access token (JWT path of AuthAny).
func mustJWT(t *testing.T) string {
	t.Helper()
	pair, err := auth.GenerateTokenPair(
		auth.Principal{UserID: "u1", TenantID: "t1", Roles: []string{"analyst"}, TenantStatus: "active"},
		testSecret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
}

func TestAuthAny_acceptsBothCredentials(t *testing.T) {
	svc, raw, _ := newAPIKeyFixture(t)
	r := gin.New()
	r.GET("/whoami", AuthAny(AuthConfig{JWTSecret: testSecret}, svc, stubTenants{status: "active"}),
		func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	jwtToken := mustJWT(t)
	t.Run("jwt still works", func(t *testing.T) {
		if w := doBearer(t, r, jwtToken); w.Code != 200 {
			t.Fatalf("jwt status = %d, want 200", w.Code)
		}
	})
	t.Run("pangu key works", func(t *testing.T) {
		if w := doBearer(t, r, raw); w.Code != 200 {
			t.Fatalf("api key status = %d, want 200", w.Code)
		}
	})
	t.Run("garbage rejected", func(t *testing.T) {
		if w := doBearer(t, r, "neither-jwt-nor-key"); w.Code != 401 {
			t.Fatalf("garbage status = %d, want 401", w.Code)
		}
	})
}
