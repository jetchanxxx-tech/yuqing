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
	"log/slog"
	"os"

	"github.com/yuging/platform/internal/api/v1"
	"github.com/yuging/platform/internal/business/alert"
	"github.com/yuging/platform/internal/business/analysis"
	"github.com/yuging/platform/internal/business/dashboard"
	"github.com/yuging/platform/internal/business/report"
	"github.com/yuging/platform/internal/config"
	"github.com/yuging/platform/internal/engine"
	"github.com/yuging/platform/internal/pkg/queue"
	"github.com/yuging/platform/internal/platform/apikey"
	"github.com/yuging/platform/internal/platform/auth"
	"github.com/yuging/platform/internal/platform/billing"
	"github.com/yuging/platform/internal/platform/settings"
	"github.com/yuging/platform/internal/platform/tenant"
	"github.com/yuging/platform/internal/platform/usage"
)

// Build wires the full MVP service graph over in-memory stores.
// logger 用于管线与适配器的运行日志；nil 时退回 slog.Default。
func Build(cfg *config.Config, logger *slog.Logger) *v1.Services {
	q := queue.NewMemory()

	// One shared tenant store: auth.Register provisions tenants into it and
	// tenant.Service (admin list/suspend/resume) reads from it — mirroring
	// the single platform tenants table in PostgreSQL mode.
	tenants := tenant.NewMemoryStore()
	authStore := auth.NewSharedTenantStore(auth.NewMemoryStore(), tenants)
	authSvc := auth.NewService(authStore, cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL)

	// 引导管理员：内存 store 下无法用 CLI/DB 造出 platform_admin，
	// 用该邮箱注册的账号即获得平台管理权限（YUGING_BOOTSTRAP_ADMIN_EMAIL）。
	authSvc.SetBootstrapAdminEmail(os.Getenv("YUGING_BOOTSTRAP_ADMIN_EMAIL"))

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
	alertSvc := alert.NewService(alert.NewMemoryStore(), nil)

	// Platform settings: seeded from environment (e.g., BOCHA_API_KEY).
	// Admins can override via PUT /api/v1/admin/settings.
	platformSettings := settings.NewMemoryStore(map[string]string{
		"bocha_api_key":    os.Getenv("BOCHA_API_KEY"),
		"deepseek_api_key": os.Getenv("DEEPSEEK_API_KEY"),
	})

	// Tenant API keys (pangu_…) + the platform usage meter the admin
	// rollup reads. The meter is shared with Auth's quota provisioning
	// target; REVIEW_REPORT §5 tracks unifying auth's private meter.
	apiKeySvc := apikey.NewService(apikey.NewMemoryStore())
	usageMeter := usage.NewMeter()

	// ── 分析管线 ────────────────────────────────────────────
	// 在同一进程内消费 analysis.tasks。内存 store/queue 都是进程私有的，
	// 独立 worker 进程既收不到消息也看不到任务 —— 这正是「任务永久停在
	// queued」的根因（实测 worker 收到任务数为 0）。
	//
	// 仅当配置了 query 引擎地址时启动：测试构造的精简配置不含该地址，
	// 若强行启动会让管线发起真实 HTTP 调用并快速失败，把任务标记为
	// failed，破坏断言 queued 的用例。
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Engines.Query.URL == "" {
		logger.Warn("pipeline: 未配置 engines.query.url，分析任务将停留在 queued")
	} else {
		keyFor := func(name string) func() string {
			return func() string {
				v, _ := platformSettings.Get(context.Background(), name)
				return v
			}
		}
		crawler := engine.NewRealCrawlerEngine(
			cfg.Engines.Query.URL,
			"", // 引擎内网认证：MVP 未启用
			keyFor("bocha_api_key"),
		)
		// insight/report 引擎未配置时传 nil —— 管线跳过对应步骤并记录 warning
		var insight *engine.RealInsightEngine
		var report *engine.RealReportEngine
		if cfg.Engines.Insight.URL != "" {
			insight = engine.NewRealInsightEngine(
				cfg.Engines.Insight.URL, "", keyFor("deepseek_api_key"))
		} else {
			logger.Warn("pipeline: 未配置 engines.insight.url，情感/话题分析将降级")
		}
		if cfg.Engines.Report.URL != "" {
			report = engine.NewRealReportEngine(
				cfg.Engines.Report.URL, "", keyFor("deepseek_api_key"))
		} else {
			logger.Warn("pipeline: 未配置 engines.report.url，报告生成将降级")
		}
		startPipeline(context.Background(), q, analysisSvc, crawler, insight, report, pipelineBudget(cfg), logger)
	}

	return &v1.Services{
		Auth:      authSvc,
		Analysis:  analysisSvc,
		Dashboard: dashboardSvc,
		Report:    reportSvc,
		Tenant:    tenantSvc,
		Alert:     alertSvc,
		Settings:  platformSettings,
		APIKey:    apiKeySvc,
		Usage:     usageMeter,
	}
}
