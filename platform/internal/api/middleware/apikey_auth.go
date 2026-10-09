// apikey_auth.go adds machine-to-machine authentication on top of the JWT
// session auth: opaque tenant API keys (pangu_…) validated by the apikey
// service, mapped to a least-privilege "api_service" principal.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/apikey"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// APIKeyValidator is the apikey-service surface the middleware needs.
type APIKeyValidator interface {
	ValidateKey(ctx context.Context, raw string) (*apikey.APIKey, error)
}

// TenantLookup resolves the key's tenant to fetch plan + status.
type TenantLookup interface {
	Get(ctx context.Context, id string) (*tenant.Tenant, error)
}

// bearerToken extracts the raw credential from an "Authorization: Bearer …"
// header. The second result is a human-readable reason when extraction fails
// ("" on success) — the same wording AuthRequired has always used.
func bearerToken(c *gin.Context) (string, string) {
	header := c.GetHeader("Authorization")
	if header == "" {
		return "", "missing authorization header"
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", "invalid authorization format"
	}
	return parts[1], ""
}

// authenticateJWT resolves current account state and injects its principal,
// aborting the chain on failure. Shared by AuthRequired and AuthAny.
func authenticateJWT(c *gin.Context, cfg AuthConfig, token string) {
	if cfg.Authenticator == nil {
		abortAuthorizationUnavailable(c)
		return
	}
	p, err := cfg.Authenticator.Authenticate(c.Request.Context(), token)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": "invalid or expired token",
				"request_id": c.GetString(string(CtxRequestID)),
			})
		} else {
			abortAuthorizationUnavailable(c)
		}
		return
	}
	if p == nil || p.UserID == "" || p.AuthType != "jwt" || p.UserStatus != "active" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code": "UNAUTHORIZED", "message": "invalid or expired token",
			"request_id": c.GetString(string(CtxRequestID)),
		})
		return
	}
	c.Set(string(CtxPrincipal), p)
	c.Next()
}

func abortAuthorizationUnavailable(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable,
		pkgerrors.ToEnvelope(pkgerrors.ErrServiceUnavailable, c.GetString(string(CtxRequestID))))
}

// authenticateWithAPIKey validates the raw key, checks the tenant and injects
// an api_service principal. It aborts the chain on failure. Unknown/revoked
// keys fail closed with 401; every non-active tenant answers 403.
func authenticateWithAPIKey(c *gin.Context, apiKeys APIKeyValidator, tenants TenantLookup, raw string) {
	if apiKeys == nil || tenants == nil {
		abortAuthorizationUnavailable(c)
		return
	}
	key, err := apiKeys.ValidateKey(c.Request.Context(), raw)
	if err != nil && !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) && !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		abortAuthorizationUnavailable(c)
		return
	}
	if err != nil || key == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code": "UNAUTHORIZED", "message": "invalid api key",
			"request_id": c.GetString(string(CtxRequestID)),
		})
		return
	}
	t, err := tenants.Get(c.Request.Context(), key.TenantID)
	if err != nil && !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		abortAuthorizationUnavailable(c)
		return
	}
	if err != nil || t == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code": "UNAUTHORIZED", "message": "invalid api key",
			"request_id": c.GetString(string(CtxRequestID)),
		})
		return
	}
	if t.Status != tenant.StatusActive {
		abortInactiveTenant(c, string(t.Status))
		return
	}
	p := &auth.Principal{
		UserID:       "apikey:" + key.ID,
		TenantID:     key.TenantID,
		Roles:        []string{"api_service"},
		PlanCode:     t.PlanCode,
		TenantStatus: string(t.Status),
		AuthType:     "api_key",
	}
	c.Set(string(CtxPrincipal), p)
	c.Next()
}

// ApiKeyAuth authenticates a request with a tenant API key only
// (Authorization: Bearer pangu_…). Use it on dedicated machine endpoints;
// AuthAny composes it with JWT for the shared /api/v1 surface.
func ApiKeyAuth(apiKeys APIKeyValidator, tenants TenantLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, why := bearerToken(c)
		if why != "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": why,
				"request_id": c.GetString(string(CtxRequestID)),
			})
			return
		}
		authenticateWithAPIKey(c, apiKeys, tenants, token)
	}
}

// AuthAny authenticates either a JWT access token or a pangu_ API key.
// Credentials carrying the API key prefix go to the key validator; everything
// else uses current account authentication. Missing dependencies fail closed.
func AuthAny(cfg AuthConfig, apiKeys APIKeyValidator, tenants TenantLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, why := bearerToken(c)
		if why != "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": why,
				"request_id": c.GetString(string(CtxRequestID)),
			})
			return
		}
		if strings.HasPrefix(token, apikey.KeyPrefix) {
			authenticateWithAPIKey(c, apiKeys, tenants, token)
			return
		}
		authenticateJWT(c, cfg, token)
	}
}
