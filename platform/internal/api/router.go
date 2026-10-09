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

	authCfg := middleware.AuthConfig{}
	if deps.Auth != nil {
		authCfg.Authenticator = deps.Auth
	}
	var apiKeys middleware.APIKeyValidator
	if deps.APIKey != nil {
		apiKeys = deps.APIKey
	}
	var tenants middleware.TenantLookup
	if deps.Tenant != nil {
		tenants = deps.Tenant
	}
	rateLimit := middleware.RateLimit(middleware.RateLimiterConfig{Enabled: cfg.RateLimit.Enabled})

	// Health check (unauthenticated).
	r.GET("/api/v1/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "version": "0.2.3-beta"})
	})

	// Auth routes (unauthenticated): register / login / refresh.
	authGroup := r.Group("/api/v1/auth")
	v1.RegisterAuthRoutes(authGroup, deps)

	// 邮箱验证落地页（公开）：用户点击邮件链接时不带 Authorization 头。
	v1.RegisterUserCenterPublicRoutes(authGroup, deps)

	// 支付渠道回调（公开路由，免鉴权）：安全由渠道验签保证（防线 1）。
	r.POST("/api/v1/callbacks/payment/:channel", v1.HandlePaymentCallback(deps))

	// Account routes remain available to active users with a suspended tenant.
	account := r.Group("/api/v1", middleware.AuthRequired(authCfg), rateLimit)
	v1.RegisterSessionRoutes(account.Group("/auth"), deps)
	v1.RegisterUserCenterRoutes(account, deps)

	// Platform administrators can restore tenants even when their own tenant
	// is suspended. Each administrative action retains its permission check.
	admin := r.Group("/api/v1", middleware.AuthRequired(authCfg), middleware.RequireRole("platform_admin"), rateLimit)
	v1.RegisterAdminRoutes(admin, deps)

	// Tenant business routes accept current JWT members or tenant API keys;
	// all require an active tenant before reaching any business handler.
	api := r.Group("/api/v1", middleware.AuthAny(authCfg, apiKeys, tenants), middleware.RequireActiveTenant(), rateLimit)
	{
		v1.RegisterAnalysisRoutes(api, deps)
		v1.RegisterMonitorPlanRoutes(api, deps)
		v1.RegisterAPIKeyRoutes(api, deps)
		v1.RegisterReportRoutes(api, deps)
		v1.RegisterDashboardRoutes(api, deps)
		v1.RegisterBillingRoutes(api, deps)
		v1.RegisterTrendsRoutes(api, deps)
	}

	return r
}
