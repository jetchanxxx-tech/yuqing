package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/platform/auth"
)

func init() { gin.SetMode(gin.TestMode) }

func TestAuthRequired_missingHeader_returns401(t *testing.T) {
	r := gin.New()
	r.Use(AuthRequired(AuthConfig{JWTSecret: "test-secret-key-min-32-chars!!"}))
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
	r.Use(AuthRequired(AuthConfig{JWTSecret: "test-secret-key-min-32-chars!!"}))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-here")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthRequired_validToken_passes(t *testing.T) {
	secret := "test-secret-key-min-32-chars!!"
	r := gin.New()
	r.Use(AuthRequired(AuthConfig{JWTSecret: secret}))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	// Generate a real token.
	pair, err := auth.GenerateTokenPair(auth.Principal{
		UserID: "u1", TenantID: "t1", Email: "a@b.com", Roles: []string{"analyst"},
	}, secret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestGetPrincipal_afterAuth_returnsPrincipal(t *testing.T) {
	secret := "test-secret-key-min-32-chars!!"
	r := gin.New()
	r.Use(AuthRequired(AuthConfig{JWTSecret: secret}))
	r.GET("/whoami", func(c *gin.Context) {
		p := GetPrincipal(c)
		if p == nil {
			c.String(500, "no principal")
			return
		}
		c.JSON(200, gin.H{"user_id": p.UserID})
	})

	pair, _ := auth.GenerateTokenPair(auth.Principal{
		UserID: "u_test", TenantID: "t_test", Email: "test@b.com", Roles: []string{"analyst"},
	}, secret, "15m", "720h")

	req := httptest.NewRequest("GET", "/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestRequirePermission_allowsMatching(t *testing.T) {
	secret := "test-secret-key-min-32-chars!!"
	r := gin.New()
	r.Use(AuthRequired(AuthConfig{JWTSecret: secret}))
	r.GET("/admin", RequirePermission("admin:tenants:list"), func(c *gin.Context) {
		c.Status(200)
	})

	pair, _ := auth.GenerateTokenPair(auth.Principal{
		UserID: "admin1", TenantID: "t1", Email: "admin@b.com", Roles: []string{"platform_admin"},
	}, secret, "15m", "720h")

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("platform_admin should have admin:tenants:list, got %d", w.Code)
	}
}

func TestRequirePermission_deniesNonMatching(t *testing.T) {
	secret := "test-secret-key-min-32-chars!!"
	r := gin.New()
	r.Use(AuthRequired(AuthConfig{JWTSecret: secret}))
	r.GET("/admin", RequirePermission("admin:tenants:list"), func(c *gin.Context) {
		c.Status(200)
	})

	pair, _ := auth.GenerateTokenPair(auth.Principal{
		UserID: "viewer1", TenantID: "t1", Email: "v@b.com", Roles: []string{"viewer"},
	}, secret, "15m", "720h")

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("viewer should be denied admin, got %d", w.Code)
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
