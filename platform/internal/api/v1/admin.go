package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// RegisterAdminRoutes mounts the platform-admin endpoints. Every route is
// guarded by RequirePermission so tenant viewers can never reach a handler
// (RBAC matrix in internal/platform/auth/auth.go).
func RegisterAdminRoutes(r *gin.RouterGroup, svcs *Services) {
	admin := r.Group("/admin")
	admin.GET("/tenants", middleware.RequirePermission("admin:tenants:list"), svcs.handleListTenants)
	admin.POST("/tenants/:id/suspend", middleware.RequirePermission("admin:tenants:suspend"), svcs.handleSuspendTenant)
	admin.POST("/tenants/:id/resume", middleware.RequirePermission("admin:tenants:suspend"), svcs.handleResumeTenant)
	admin.GET("/usage", middleware.RequirePermission("billing:manage"), svcs.handlePlatformUsage)
	admin.GET("/plans", middleware.RequirePermission("admin:plans:manage"), svcs.handleAdminListPlans)
	admin.POST("/plans", middleware.RequirePermission("admin:plans:manage"), svcs.handleAdminCreatePlan)
	admin.GET("/settings", middleware.RequirePermission("admin:plans:manage"), svcs.handleGetSettings)
	admin.PUT("/settings", middleware.RequirePermission("admin:plans:manage"), svcs.handleUpdateSettings)
}

// adminTenant is the admin list row (web/src/api/admin.ts Tenant).
type adminTenant struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	DBName   string `json:"db_name,omitempty"`
	PlanCode string `json:"plan_code"`
	Status   string `json:"status"`
}

// handleListTenants lists platform tenants from the shared tenant store.
func (s *Services) handleListTenants(c *gin.Context) {
	tenants, err := s.Tenant.List(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	rows := make([]adminTenant, 0, len(tenants))
	for _, t := range tenants {
		rows = append(rows, adminTenant{
			ID:       t.ID,
			Name:     t.Name,
			Slug:     t.Slug,
			DBName:   t.DBName,
			PlanCode: t.PlanCode,
			Status:   string(t.Status),
		})
	}
	c.JSON(http.StatusOK, gin.H{"tenants": rows, "total": len(rows)})
}

func (s *Services) handleSuspendTenant(c *gin.Context) {
	s.changeTenantStatus(c, true)
}

func (s *Services) handleResumeTenant(c *gin.Context) {
	s.changeTenantStatus(c, false)
}

func (s *Services) changeTenantStatus(c *gin.Context, suspend bool) {
	ctx := c.Request.Context()
	var err error
	if suspend {
		err = s.Tenant.Suspend(ctx, c.Param("id"))
	} else {
		err = s.Tenant.Resume(ctx, c.Param("id"))
	}
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "tenant not found")
			return
		}
		respondError(c, err)
		return
	}
	t, err := s.Tenant.Get(ctx, c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": t.ID, "status": t.Status})
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
	all, err := s.Settings.All(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": all})
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
	for k, v := range req {
		if err := s.Settings.Set(c.Request.Context(), k, v); err != nil {
			respondError(c, err)
			return
		}
	}
	s.handleGetSettings(c) // return updated settings
}
