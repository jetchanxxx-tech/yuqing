package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/platform/apikey"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/tenant"
)

type unavailableKeys struct{}

func (unavailableKeys) ValidateKey(context.Context, string) (*apikey.APIKey, error) {
	return nil, fmt.Errorf("private database credential in driver error")
}

type unavailableTenants struct{}

func (unavailableTenants) Get(context.Context, string) (*tenant.Tenant, error) {
	return nil, fmt.Errorf("private database credential in driver error")
}

func TestAuthorizationStorageUnavailableReturns503WithoutClearingIdentity(t *testing.T) {
	keys, liveKey, _ := newAPIKeyFixture(t)
	for _, tc := range []struct {
		name    string
		auth    AuthConfig
		keys    APIKeyValidator
		tenants TenantLookup
		token   string
	}{
		{"account resolver missing", AuthConfig{}, keys, stubTenants{status: "active"}, "signed-access"},
		{"account database failed", AuthConfig{Authenticator: authenticateFunc(func(context.Context, string) (*auth.Principal, error) {
			return nil, fmt.Errorf("private database credential in driver error")
		})}, keys, stubTenants{status: "active"}, "signed-access"},
		{"key store missing", AuthConfig{}, nil, stubTenants{status: "active"}, liveKey},
		{"key database failed", AuthConfig{}, unavailableKeys{}, stubTenants{status: "active"}, liveKey},
		{"tenant database failed", AuthConfig{}, keys, unavailableTenants{}, liveKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(RequestID())
			r.GET("/whoami", AuthAny(tc.auth, tc.keys, tc.tenants), func(c *gin.Context) {
				t.Error("unavailable authorization must not reach the protected handler")
				c.Status(http.StatusOK)
			})
			w := doBearer(t, r, tc.token)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d, want 503; body=%s", w.Code, w.Body.String())
			}
			var envelope map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["code"] != "SERVICE_UNAVAILABLE" || envelope["request_id"] == "" || strings.Contains(w.Body.String(), "private database") {
				t.Fatalf("unsafe availability envelope: %s", w.Body.String())
			}
		})
	}
}
