package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/api/middleware"
	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/platform/billing"
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

// handlePlatformUsage: platform-wide usage aggregation needs the usage rollup
// consumer. Answer the JSON envelope until then.
func (s *Services) handlePlatformUsage(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"code":       "NOT_IMPLEMENTED",
		"message":    "admin:usage not implemented (usage rollup consumer pending)",
		"request_id": requestID(c),
	})
}

// sortedPlans returns the four tiers in a stable order.
func sortedPlans() []*billing.Plan {
	plans := billing.DefaultPlans()
	order := []string{"free", "pro", "business", "enterprise"}
	out := make([]*billing.Plan, 0, len(order))
	for _, code := range order {
		if p, ok := plans[code]; ok {
			out = append(out, p)
		}
	}
	return out
}
