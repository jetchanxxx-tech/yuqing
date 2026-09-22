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
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/api/v1"
	"github.com/yuqing/platform/internal/business/alert"
	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/business/dashboard"
	"github.com/yuqing/platform/internal/business/report"
	"github.com/yuqing/platform/internal/business/trends"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/engine"
	"github.com/yuqing/platform/internal/pkg/db"
	"github.com/yuqing/platform/internal/pkg/queue"
	"github.com/yuqing/platform/internal/platform/apikey"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/payment"
	"github.com/yuqing/platform/internal/platform/settings"
	"github.com/yuqing/platform/internal/platform/tenant"
	"github.com/yuqing/platform/internal/platform/usage"
)

// Build wires the full MVP service graph. Store 后端由 cfg.Store.Driver
// 选择：memory（测试/开发）或 postgres（生产持久化，重启不丢数据）。
// logger 用于管线与适配器的运行日志；nil 时退回 slog.Default。
func Build(cfg *config.Config, logger *slog.Logger) *v1.Services {
	q := queue.NewMemory()

	var (
		tenantStore      tenant.Store
		authStore        auth.Store
		analysisSvc      *analysis.Service
		reportStore      report.Store
		alertStore       alert.Store
		apiKeyStore      apikey.Store
		platformSettings settings.Store
		usageMeter       usage.PlatformMeter
		creditSvc        *credit.Service
		platformPool     *pgxpool.Pool
	)

	if cfg.Store.Driver == "postgres" {
		dbManager := db.MustNewManager(db.ManagerConfig{
			PlatformDSN: cfg.DB.Primary,
			MaxConns:    cfg.DB.MaxConns,
		})
		pool := dbManager.Platform(context.Background())
		platformPool = pool

		tenantStore = tenant.NewPGStore(pool)
		authStore = auth.NewPGStore(pool)
		analysisSvc = analysis.NewPGService(pool, q, 4)
		reportStore = report.NewPGStore(pool)
		// 单库模式：告警经 ResolverStore 每次按 tenant_id 绑定（库内过滤）
		alertStore = alert.NewResolverStore(func(ctx context.Context, tenantID string) (*pgxpool.Pool, error) {
			return pool, nil
		})
		apiKeyStore = apikey.NewPGStore(pool)
		usageMeter = usage.NewPGMeter(pool)
		creditSvc = credit.NewService(credit.NewPGStore(pool))

		var err error
		platformSettings, err = settings.NewPGStore(pool, map[string]string{
			"bocha_api_key": os.Getenv("BOCHA_API_KEY"),
			"llm_api_key":   os.Getenv("LLM_API_KEY"),
			"llm_base_url":  os.Getenv("LLM_BASE_URL"),
			"llm_model":     os.Getenv("LLM_MODEL"),
		})
		if err != nil {
			panic(fmt.Sprintf("app: seed settings: %v", err))
		}
	} else {
		// One shared tenant store: auth.Register provisions tenants into it and
		// tenant.Service (admin list/suspend/resume) reads from it — mirroring
		// the single platform tenants table in PostgreSQL mode.
		tenants := tenant.NewMemoryStore()
		tenantStore = tenants
		authStore = auth.NewSharedTenantStore(auth.NewMemoryStore(), tenants)
		analysisSvc = analysis.NewService(q, 4)
		reportStore = report.NewMemoryStore()
		alertStore = alert.NewMemoryStore()
		apiKeyStore = apikey.NewMemoryStore()
		usageMeter = usage.NewMeter()
		creditSvc = credit.NewService(credit.NewMemoryStore())

		// Platform settings: seeded from environment (e.g., BOCHA_API_KEY).
		// Admins can override via PUT /api/v1/admin/settings.
		platformSettings = settings.NewMemoryStore(map[string]string{
			"bocha_api_key": os.Getenv("BOCHA_API_KEY"),
			"llm_api_key":   os.Getenv("LLM_API_KEY"),
			"llm_base_url":  os.Getenv("LLM_BASE_URL"),
			"llm_model":     os.Getenv("LLM_MODEL"),
		})
	}

	// 报告额度闸门：Create/Rerun 各扣 1 次，管线失败/取消自动回补。
	analysisSvc.SetCreditReserver(creditSvc)

	authSvc := auth.NewService(authStore, cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL)

	// 新租户注册赠 1 次试用额度（方案 B：试用归 Lite 档体验）。
	authSvc.SetPostRegister(func(ctx context.Context, tenantID string) error {
		return creditSvc.GrantTrial(ctx, tenantID, credit.TrialCredits)
	})

	// 引导管理员：内存 store 下无法用 CLI/DB 造出 platform_admin，
	// 用该邮箱注册的账号即获得平台管理权限（YUQING_BOOTSTRAP_ADMIN_EMAIL）。
	authSvc.SetBootstrapAdminEmail(os.Getenv("YUQING_BOOTSTRAP_ADMIN_EMAIL"))

	// 用户中心 P0：邮箱验证 / 手机号绑定 / 个人资料 / 修改密码。
	// UserStore 复用 authStore 底层实现（内存/PG 都实现了 UserStore）；
	// 验证凭据按 driver 选存储；邮件/短信通道从 settings 动态读取（admin 在线配置）。
	userStore, ok := authStore.(auth.UserStore)
	if !ok {
		panic("app: auth store does not implement auth.UserStore")
	}
	var verifications auth.VerificationStore
	if cfg.Store.Driver == "postgres" && platformPool != nil {
		verifications = auth.NewPGVerificationStore(platformPool)
	} else {
		verifications = auth.NewMemoryVerificationStore()
	}
	authSvc.EnableUserCenter(
		userStore,
		verifications,
		NewSettingsSMS(platformSettings),
		NewSettingsMailer(platformSettings),
		os.Getenv("YUQING_PUBLIC_BASE_URL"),
	)

	tenantSvc := tenant.NewService(tenantStore)

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
	reportSvc := report.NewService(reportStore, planCodeFor, planProvider)

	dashboardSvc := dashboard.NewService(analysisSvc, reportSvc)
	alertSvc := alert.NewService(alertStore, nil)

	// Tenant API keys (pangu_…) + the platform usage meter the admin
	// rollup reads. The meter is shared with Auth's quota provisioning
	// target; REVIEW_REPORT §5 tracks unifying auth's private meter.
	apiKeySvc := apikey.NewService(apiKeyStore)

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
		// LLM 供应商可配置（后台「数据源配置」）：每次请求实时读 settings，
		// 换供应商/key/model 零重启生效。空值返回空串 —— 引擎侧空串回退自身默认。
		llmOpts := func() (string, string) {
			baseURL, _ := platformSettings.Get(context.Background(), "llm_base_url")
			model, _ := platformSettings.Get(context.Background(), "llm_model")
			return baseURL, model
		}
		// insight/report 引擎未配置时传 nil —— 管线跳过对应步骤并记录 warning
		var insight *engine.RealInsightEngine
		var report *engine.RealReportEngine
		if cfg.Engines.Insight.URL != "" {
			insight = engine.NewRealInsightEngine(
				cfg.Engines.Insight.URL, "", keyFor("llm_api_key")).WithLLMOpts(llmOpts)
		} else {
			logger.Warn("pipeline: 未配置 engines.insight.url，情感/话题分析将降级")
		}
		if cfg.Engines.Report.URL != "" {
			report = engine.NewRealReportEngine(
				cfg.Engines.Report.URL, "", keyFor("llm_api_key")).WithLLMOpts(llmOpts)
		} else {
			logger.Warn("pipeline: 未配置 engines.report.url，报告生成将降级")
		}
		startPipeline(context.Background(), q, analysisSvc, crawler, insight, report,
			pipelineBudget(cfg), logger, analysisModeFor(tenantSvc))
	}

	// ── 收费体系（方案 B）────────────────────────────────────
	// 支付渠道经 Registry 懒构建 + 配置哈希缓存：admin 后台改渠道配置
	// 零重启生效。beta 部署时渠道配置为空 —— 购买页不显示任何渠道，
	// 待用户在 admin 界面填入商户参数后渠道自动出现。
	paymentRegistry := payment.NewRegistry(func(ctx context.Context, key string) (string, error) {
		return platformSettings.Get(ctx, key)
	})
	var paymentStore payment.Store
	if platformPool != nil {
		paymentStore = payment.NewPGStore(platformPool)
	} else {
		paymentStore = payment.NewMemoryStore()
	}
	initialProviders, _ := paymentRegistry.Resolve(context.Background())
	paymentSvc := payment.NewService(paymentStore, creditSvc, initialProviders, logger)
	paymentSvc.SetProviderReload(paymentRegistry.Resolve)

	// ── 热榜聚合（F21）────────────────────────────────────────
	// RSSHub 地址可配置（生产绑 127.0.0.1:1200，测试用公共实例），
	// 5 分钟定时全平台刷新。未配置时 trends 为 nil → API 返回 503。
	var trendsSvc *trends.Service
	if rsshubBase := cfg.RSSHubBase; rsshubBase != "" {
		trendsSvc = trends.NewService(rsshubBase, 5*time.Minute, logger)
	} else {
		logger.Warn("trends: 未配置 rsshub_base，热榜聚合不可用")
	}

	return &v1.Services{
		Auth:            authSvc,
		Analysis:        analysisSvc,
		Dashboard:       dashboardSvc,
		Report:          reportSvc,
		Tenant:          tenantSvc,
		Alert:           alertSvc,
		Settings:        platformSettings,
		APIKey:          apiKeySvc,
		Usage:           usageMeter,
		Credits:         creditSvc,
		Payment:         paymentSvc,
		PaymentRegistry: paymentRegistry,
		Trends:          trendsSvc,
	}
}
