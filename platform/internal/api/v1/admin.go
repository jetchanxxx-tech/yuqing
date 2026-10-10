package v1

import (
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/settings"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// RegisterAdminRoutes mounts the platform-admin endpoints. Every route is
// guarded by RequirePermission so tenant viewers can never reach a handler
// (RBAC matrix in internal/platform/auth/auth.go).
func RegisterAdminRoutes(r *gin.RouterGroup, svcs *Services) {
	admin := r.Group("/admin")
	admin.GET("/users", middleware.RequirePermission("admin:users:read"), svcs.handleListAdminUsers)
	admin.GET("/users/:id", middleware.RequirePermission("admin:users:read"), svcs.handleAdminUser)
	admin.POST("/users", middleware.RequirePermission("admin:users:manage"), svcs.handleCreateAdminUser)
	admin.PATCH("/users/:id", middleware.RequirePermission("admin:users:manage"), svcs.handlePatchAdminUser)
	admin.POST("/users/:id/activation-resend", middleware.RequirePermission("admin:users:manage"), svcs.handleResendActivation)
	admin.POST("/users/:id/password-reset", middleware.RequirePermission("admin:users:manage"), svcs.handleAdminPasswordReset)
	admin.POST("/users/:id/disable", middleware.RequirePermission("admin:users:manage"), svcs.handleDisableAdminUser)
	admin.POST("/users/:id/enable", middleware.RequirePermission("admin:users:manage"), svcs.handleEnableAdminUser)
	admin.PUT("/users/:id/platform-role", middleware.RequirePermission("admin:roles:manage"), svcs.handleAdminPlatformRole)
	admin.GET("/tenants", middleware.RequirePermission("admin:tenants:read"), svcs.handleListTenants)
	admin.GET("/tenants/:id", middleware.RequirePermission("admin:tenants:read"), svcs.handleAdminTenant)
	admin.POST("/tenants/:id/credit-adjustments", middleware.RequirePermission("admin:credits:manage"), svcs.handleCreditAdjustment)
	admin.POST("/tenants/:id/suspend", middleware.RequirePermission("admin:tenants:manage"), svcs.handleSuspendTenant)
	admin.POST("/tenants/:id/resume", middleware.RequirePermission("admin:tenants:manage"), svcs.handleResumeTenant)
	admin.PUT("/tenants/:id/members/:user_id/role", middleware.RequirePermission("admin:members:manage"), svcs.handleAdminMemberRole)
	admin.GET("/usage", middleware.RequirePermission("billing:manage"), svcs.handlePlatformUsage)
	admin.GET("/plans", middleware.RequirePermission("admin:plans:manage"), svcs.handleAdminListPlans)
	admin.POST("/plans", middleware.RequirePermission("admin:plans:manage"), svcs.handleAdminCreatePlan)
	admin.GET("/settings", middleware.RequirePermission("admin:plans:manage"), svcs.handleGetSettings)
	admin.PUT("/settings", middleware.RequirePermission("admin:plans:manage"), svcs.handleUpdateSettings)
}

// handleListTenants lists platform tenants from the shared tenant store.
func (s *Services) handleListTenants(c *gin.Context) {
	q, ok := adminQuery(c, false)
	if !ok {
		return
	}
	rows, total, err := s.AccountAdmin.ListTenants(c.Request.Context(), q)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "tenants": rows, "total": total, "page": q.Page, "page_size": q.PageSize})
}

func (s *Services) handleSuspendTenant(c *gin.Context) {
	s.changeTenantStatus(c, true)
}

func (s *Services) handleResumeTenant(c *gin.Context) {
	s.changeTenantStatus(c, false)
}

func (s *Services) changeTenantStatus(c *gin.Context, suspend bool) {
	var req adminStatusRequest
	if !decodeAdmin(c, &req) || !validAdminStatus(c, req) {
		return
	}
	action := "tenant.resume"
	if suspend {
		action = "tenant.suspend"
	}
	s.respondAdminMutation(c, adminMutation(c, req, action))
}

// handleAdminListPlans lists the platform plan catalog (same data as
// /billing/plans, admin view).
func (s *Services) handleAdminListPlans(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plans": sortedPlans()})
}

// handleAdminCreatePlan: creating custom plans needs a plan-management store
// (config-driven catalog today). Answer the JSON envelope until then.
func (s *Services) handleAdminCreatePlan(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"code":       "NOT_IMPLEMENTED",
		"message":    "admin:plans:create not implemented (plan catalog is config-driven)",
		"request_id": requestID(c),
	})
}

// handlePlatformUsage aggregates platform-wide consumption from the live
// stores: tenant roster (status/plan), usage meter token spend and per-tenant
// analysis counts. Memory mode: the meter is the counter ledger itself; in
// PostgreSQL mode the same shape reads usage_daily (the rollup consumer).
func (s *Services) handlePlatformUsage(c *gin.Context) {
	ctx := c.Request.Context()
	tenants, err := s.Tenant.List(ctx)
	if err != nil {
		respondError(c, err)
		return
	}

	spend := map[string]int64{}
	if s.Usage != nil {
		spend = s.Usage.Aggregate()
	}

	var totalTokens, totalAnalyses int64
	activeTenants := 0
	rows := make([]gin.H, 0, len(tenants))
	for _, t := range tenants {
		if t.Status == tenant.StatusActive {
			activeTenants++
		}
		tokens := spend[t.ID]
		totalTokens += tokens
		analyses, err := s.Analysis.List(ctx, t.ID)
		if err != nil {
			respondError(c, err)
			return
		}
		totalAnalyses += int64(len(analyses))
		rows = append(rows, gin.H{
			"tenant_id":   t.ID,
			"name":        t.Name,
			"plan_code":   t.PlanCode,
			"status":      string(t.Status),
			"tokens_used": tokens,
			"analyses":    len(analyses),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total_tenants":     len(tenants),
		"active_tenants":    activeTenants,
		"total_tokens_used": totalTokens,
		"total_analyses":    totalAnalyses,
		"per_tenant":        rows,
	})
}

// sortedPlans returns the four tiers in a stable order.
func sortedPlans() []*billing.Plan {
	plans := billing.DefaultPlans()
	order := []string{"free", "lite", "pro", "enterprise"}
	out := make([]*billing.Plan, 0, len(order))
	for _, code := range order {
		if p, ok := plans[code]; ok {
			out = append(out, p)
		}
	}
	return out
}

// ── Platform Settings (API keys, feature toggles) ──────────────

// handleGetSettings returns all platform settings visible to admins.
func (s *Services) handleGetSettings(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	var all map[string]string
	var err error
	if store, ok := s.Settings.(settings.AdminStore); ok {
		all, err = store.AdminAll(c.Request.Context(), p.UserID, p.TokenVersion)
	} else {
		err = s.Auth.WithCurrentAdministrator(c.Request.Context(), *p, func() error { var e error; all, e = s.Settings.All(c.Request.Context()); return e })
	}
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings.Redact(all)})
}

// handleUpdateSettings merges new values into platform settings.
// Request body: {"bocha_api_key": "sk-xxx", "feature_x": "true"}
func (s *Services) handleUpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": "BAD_REQUEST", "message": "body must be JSON object",
		})
		return
	}
	p := middleware.GetPrincipal(c)
	var err error
	if store, ok := s.Settings.(settings.AdminStore); ok {
		err = store.AdminPatch(c.Request.Context(), p.UserID, p.TokenVersion, req)
	} else {
		err = s.Auth.WithCurrentAdministrator(c.Request.Context(), *p, func() error {
			store, ok := s.Settings.(*settings.MemoryStore)
			if !ok {
				return pkgerrors.ErrServiceUnavailable
			}
			store.Patch(req)
			return nil
		})
	}
	if err != nil {
		respondError(c, err)
		return
	}
	s.handleGetSettings(c)
}
