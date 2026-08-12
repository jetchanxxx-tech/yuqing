// Package api provides the HTTP routing layer.
package api

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/api/middleware"
	"github.com/yuging/platform/internal/api/v1"
	"github.com/yuging/platform/internal/config"
)

// NewRouter builds the Gin engine with all middleware and route groups.
func NewRouter(cfg *config.Config, log *slog.Logger) *gin.Engine {
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

	// Auth routes (unauthenticated).
	authGroup := r.Group("/api/v1/auth")
	v1.RegisterAuthRoutes(authGroup, authCfg)

	// Authenticated routes.
	api := r.Group("/api/v1")
	api.Use(middleware.AuthRequired(authCfg))
	api.Use(middleware.RateLimit(middleware.RateLimiterConfig{Enabled: cfg.RateLimit.Enabled}))
	{
		// TODO: wire real services (Phase 1 continuation).
		v1.RegisterAnalysisRoutes(api)
		v1.RegisterReportRoutes(api)
		v1.RegisterDashboardRoutes(api)
		v1.RegisterBillingRoutes(api)
		v1.RegisterAdminRoutes(api)
	}

	return r
}
