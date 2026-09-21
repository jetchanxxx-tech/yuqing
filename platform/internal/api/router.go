// Package api provides the HTTP routing layer.
package api

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/api/v1"
	"github.com/yuqing/platform/internal/config"
)

// NewRouter builds the Gin engine with all middleware, route groups and the
// service graph. deps carries the wired services (internal/app/container.go);
// it is required — handlers are thin and delegate to it.
func NewRouter(cfg *config.Config, log *slog.Logger, deps *v1.Services) *gin.Engine {
	r := gin.New()

	// Global middleware.
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery(log))
	r.Use(middleware.AuditLog(log))

	authCfg := middleware.AuthConfig{JWTSecret: cfg.Auth.JWTSecret}

	// Health check (unauthenticated).
	r.GET("/api/v1/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Auth routes (unauthenticated): register / login / refresh.
	authGroup := r.Group("/api/v1/auth")
	v1.RegisterAuthRoutes(authGroup, deps)

	// 支付渠道回调（公开路由，免鉴权）：安全由渠道验签保证（防线 1）。
	r.POST("/api/v1/callbacks/payment/:channel", v1.HandlePaymentCallback(deps))

	// Authenticated routes. AuthAny accepts a JWT access token or a tenant
	// API key (Authorization: Bearer pangu_…); deps.APIKey may be nil, in
	// which case key credentials fail closed and JWT behavior is unchanged.
	api := r.Group("/api/v1")
	api.Use(middleware.AuthAny(authCfg, deps.APIKey, deps.Tenant))
	api.Use(middleware.RateLimit(middleware.RateLimiterConfig{Enabled: cfg.RateLimit.Enabled}))
	{
		// Session endpoints that read the authenticated principal.
		session := api.Group("/auth")
		v1.RegisterSessionRoutes(session, deps)

		v1.RegisterAnalysisRoutes(api, deps)
		v1.RegisterAPIKeyRoutes(api, deps)
		v1.RegisterReportRoutes(api, deps)
		v1.RegisterDashboardRoutes(api, deps)
		v1.RegisterBillingRoutes(api, deps)
		v1.RegisterTrendsRoutes(api, deps)
		v1.RegisterAdminRoutes(api, deps)
	}

	return r
}
