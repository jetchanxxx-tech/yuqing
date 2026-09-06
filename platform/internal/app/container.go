// Package app is the composition root: it wires the concrete service
// implementations once and hands them to the HTTP router (see CLAUDE.md —
// interface-first modularity, hand-rolled DI).
//
// MVP mode uses in-memory stores + the memory queue, so server, worker and
// tests each build their own graph. The single platform "tenants" table is
// simulated by one shared tenant.MemoryStore that backs both the auth flow
// (registration provisions the tenant) and the tenant admin service.
package app

import (
	"context"

	"github.com/yuging/platform/internal/api/v1"
	"github.com/yuging/platform/internal/business/alert"
	"github.com/yuging/platform/internal/business/analysis"
	"github.com/yuging/platform/internal/business/dashboard"
	"github.com/yuging/platform/internal/business/report"
	"github.com/yuging/platform/internal/config"
	"github.com/yuging/platform/internal/pkg/queue"
	"github.com/yuging/platform/internal/platform/auth"
	"github.com/yuging/platform/internal/platform/billing"
	"github.com/yuging/platform/internal/platform/tenant"
)

// Build wires the full MVP service graph over in-memory stores.
func Build(cfg *config.Config) *v1.Services {
	q := queue.NewMemory()

	// One shared tenant store: auth.Register provisions tenants into it and
	// tenant.Service (admin list/suspend/resume) reads from it — mirroring
	// the single platform tenants table in PostgreSQL mode.
	tenants := tenant.NewMemoryStore()
	authStore := auth.NewSharedTenantStore(auth.NewMemoryStore(), tenants)
	authSvc := auth.NewService(authStore, cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL)

	tenantSvc := tenant.NewService(tenants)
	analysisSvc := analysis.NewService(q, 4)

	// Report plan gating resolves the tenant's current plan from the shared
	// tenant store; unknown tenants default to the most restrictive plan.
	planCodeFor := func(tenantID string) string {
		t, err := tenantSvc.Get(context.Background(), tenantID)
		if err != nil {
			return ""
		}
		return t.PlanCode
	}
	planProvider := func(planCode string) *billing.Plan {
		return billing.DefaultPlans()[planCode]
	}
	reportSvc := report.NewService(report.NewMemoryStore(), planCodeFor, planProvider)

	dashboardSvc := dashboard.NewService(analysisSvc, reportSvc)
	// MVP: no real email transport; nil sender silently discards alerts.
	alertSvc := alert.NewService(alert.NewMemoryStore(), nil)

	return &v1.Services{
		Auth:      authSvc,
		Analysis:  analysisSvc,
		Dashboard: dashboardSvc,
		Report:    reportSvc,
		Tenant:    tenantSvc,
		Alert:     alertSvc,
	}
}
