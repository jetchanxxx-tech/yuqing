// Package middleware provides HTTP middleware for authentication, authorization,
// tenant resolution, rate limiting, and request audit.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/platform/auth"
)

// Context keys for middleware-injected values.
type ctxKey string

const (
	CtxPrincipal ctxKey = "principal"
	CtxRequestID ctxKey = "request_id"
)

// AuthConfig holds auth middleware settings.
type AuthConfig struct {
	JWTSecret string
}

// AuthRequired validates the JWT Bearer token and injects Principal into context.
func AuthRequired(cfg AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, why := bearerToken(c)
		if why != "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": why,
			})
			return
		}
		authenticateJWT(c, cfg, token)
	}
}

// RequireRole checks that the authenticated principal has at least one of the required roles.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := GetPrincipal(c)
		if p == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": "authentication required",
			})
			return
		}
		for _, role := range roles {
			for _, r := range p.Roles {
				if r == role {
					c.Next()
					return
				}
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": "FORBIDDEN", "message": "insufficient permissions",
		})
	}
}

// RequirePermission checks that the authenticated principal has a specific permission.
func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := GetPrincipal(c)
		if p == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": "UNAUTHORIZED", "message": "authentication required",
			})
			return
		}
		for _, role := range p.Roles {
			if auth.RoleHasPermission(role, permission) {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": "FORBIDDEN", "message": "insufficient permissions",
		})
	}
}

// GetPrincipal extracts the Principal from the gin context.
func GetPrincipal(c *gin.Context) *auth.Principal {
	if v, ok := c.Get(string(CtxPrincipal)); ok {
		if p, ok := v.(*auth.Principal); ok {
			return p
		}
	}
	return nil
}

// RequestID injects or propagates a request_id into every request.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" {
			rid = generateShortID()
		}
		c.Set(string(CtxRequestID), rid)
		c.Header("X-Request-Id", rid)
		c.Next()
	}
}

func generateShortID() string {
	return id.New()
}

// Recovery logs panics and returns 500.
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Error("panic recovered",
						slog.Any("panic", r),
						slog.String("path", c.Request.URL.Path),
					)
				}
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":    "INTERNAL",
					"message": "internal server error",
				})
			}
		}()
		c.Next()
	}
}

// RateLimiterConfig holds rate limiter settings.
type RateLimiterConfig struct {
	Enabled bool
}

// RateLimit is a stub rate limiter. Real implementation uses Redis sliding window.
func RateLimit(cfg RateLimiterConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enabled {
			c.Next()
			return
		}
		// TODO: Redis sliding window rate limit (Phase 2).
		c.Next()
	}
}

// AuditLog logs every authenticated request.
func AuditLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		p := GetPrincipal(c)
		actorID := ""
		tenantID := ""
		if p != nil {
			actorID = p.UserID
			tenantID = p.TenantID
		}
		logger.Info("request",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("actor_id", actorID),
			slog.String("tenant_id", tenantID),
		)
	}
}

// contextKey is a private type to avoid context key collisions.
type contextKey string

// WithPrincipal injects a Principal into a context.Context (for use outside gin).
func WithPrincipal(ctx context.Context, p *auth.Principal) context.Context {
	return context.WithValue(ctx, contextKey(CtxPrincipal), p)
}

// PrincipalFromContext extracts a Principal from context.Context.
func PrincipalFromContext(ctx context.Context) *auth.Principal {
	if p, ok := ctx.Value(contextKey(CtxPrincipal)).(*auth.Principal); ok {
		return p
	}
	return nil
}
