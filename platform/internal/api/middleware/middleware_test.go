package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/auth"
)

func init() { gin.SetMode(gin.TestMode) }

func TestAuthRequired_missingHeader_returns401(t *testing.T) {
	r := gin.New()
	r.Use(AuthRequired(AuthConfig{}))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthRequired_invalidToken_returns401(t *testing.T) {
	r := gin.New()
	cfg, _ := jwtFixture(t, auth.Principal{UserID: "u_invalid", TenantID: "t1", Roles: []string{"analyst"}})
	r.Use(AuthRequired(cfg))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// jwtFixture creates real account, tenant and membership rows. Permissions are
// read through Service.Authenticate rather than carried by the signed token.
func jwtFixture(t *testing.T, p auth.Principal) (AuthConfig, string) {
	t.Helper()
	store := auth.NewMemoryStore()
	role, bootstrap := "analyst", false
	for _, candidate := range p.Roles {
		if candidate == "platform_admin" {
			bootstrap = true
		} else {
			role = candidate
		}
	}
	if err := store.RegisterAccount(context.Background(),
		auth.User{ID: p.UserID, Email: p.Email, Status: "active", TokenVersion: p.TokenVersion},
		auth.Tenant{ID: p.TenantID, Slug: p.TenantID, DBName: p.TenantID, Status: "active", PlanCode: "free"},
		auth.Member{UserID: p.UserID, TenantID: p.TenantID, Role: role}, bootstrap); err != nil {
		t.Fatalf("register fixture account: %v", err)
	}
	svc := auth.NewService(store, testSecret, "15m", "720h")
	pair, err := auth.GenerateTokenPair(p, testSecret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}
	return AuthConfig{Authenticator: svc}, pair.AccessToken
}

func TestAuthRequired_validToken_passes(t *testing.T) {
	cfg, token := jwtFixture(t, auth.Principal{
		UserID: "u1", TenantID: "t1", Email: "a@b.com", Roles: []string{"analyst"},
	})
	r := gin.New()
	r.Use(AuthRequired(cfg))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestGetPrincipal_afterAuth_returnsPrincipal(t *testing.T) {
	cfg, token := jwtFixture(t, auth.Principal{
		UserID: "u_test", TenantID: "t_test", Email: "test@b.com", Roles: []string{"analyst"},
	})
	r := gin.New()
	r.Use(AuthRequired(cfg))
	r.GET("/whoami", func(c *gin.Context) {
		p := GetPrincipal(c)
		if p == nil {
			c.String(500, "no principal")
			return
		}
		c.JSON(200, gin.H{"user_id": p.UserID})
	})

	req := httptest.NewRequest("GET", "/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestRequirePermission_allowsMatching(t *testing.T) {
	cfg, token := jwtFixture(t, auth.Principal{
		UserID: "admin1", TenantID: "t1", Email: "admin@b.com", Roles: []string{"platform_admin"},
	})
	r := gin.New()
	r.Use(AuthRequired(cfg))
	r.GET("/admin", RequirePermission("admin:tenants:list"), func(c *gin.Context) {
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("platform_admin should have admin:tenants:list, got %d", w.Code)
	}
}

func TestRequirePermission_deniesNonMatching(t *testing.T) {
	cfg, token := jwtFixture(t, auth.Principal{
		UserID: "viewer1", TenantID: "t1", Email: "v@b.com", Roles: []string{"viewer"},
	})
	r := gin.New()
	r.Use(AuthRequired(cfg))
	r.GET("/admin", RequirePermission("admin:tenants:list"), func(c *gin.Context) {
		c.Status(200)
	})

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("viewer should be denied admin, got %d", w.Code)
	}
}

func TestAuthRequired_withoutAuthenticator_rejectsSignedToken(t *testing.T) {
	_, token := jwtFixture(t, auth.Principal{UserID: "u1", TenantID: "t1"})
	r := gin.New()
	r.GET("/whoami", AuthRequired(AuthConfig{}), func(c *gin.Context) { c.Status(200) })
	if w := doBearer(t, r, token); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired authenticator status = %d, want 503", w.Code)
	}
}

type authenticateFunc func(context.Context, string) (*auth.Principal, error)

func (f authenticateFunc) Authenticate(ctx context.Context, token string) (*auth.Principal, error) {
	return f(ctx, token)
}

func TestAuthRequired_storeFailure_returnsServerError(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/whoami", AuthRequired(AuthConfig{Authenticator: authenticateFunc(
		func(context.Context, string) (*auth.Principal, error) {
			return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "database connection failed")
		},
	)}), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := doBearer(t, r, "access-token")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("store failure status = %d, want 503", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	requestID, _ := body["request_id"].(string)
	if body["code"] != "SERVICE_UNAVAILABLE" || requestID == "" || strings.Contains(w.Body.String(), "database connection failed") {
		t.Fatalf("store failure envelope = %s", w.Body.String())
	}
}

func TestRequireActiveTenant_currentMembershipAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name       string
		authType   string
		status     string
		member     bool
		wantStatus int
	}{
		{"current JWT member", "jwt", "active", true, 200},
		{"removed JWT member", "jwt", "active", false, 403},
		{"suspended JWT tenant", "jwt", "suspended", true, 403},
		{"closed JWT tenant", "jwt", "closed", true, 403},
		{"provisioning JWT tenant", "jwt", "provisioning", true, 403},
		{"active tenant key", "api_key", "active", false, 200},
		{"unrecognized credential type", "", "active", true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/whoami", func(c *gin.Context) {
				c.Set(string(CtxPrincipal), &auth.Principal{
					UserID: "u1", TenantID: "t1", AuthType: tc.authType,
					UserStatus: "active", TenantStatus: tc.status, MemberExists: tc.member,
				})
				c.Next()
			}, RequireActiveTenant(), func(c *gin.Context) { c.Status(http.StatusOK) })
			if w := doBearer(t, r, ""); w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}

func TestRequestID_injectsHeader(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id header should be set")
	}
}

func TestRecovery_catchesPanic(t *testing.T) {
	r := gin.New()
	r.Use(Recovery(nil))
	r.GET("/panic", func(c *gin.Context) { panic("oops") })

	req := httptest.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
