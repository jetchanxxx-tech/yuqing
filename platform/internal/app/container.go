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
	"github.com/yuqing/platform/internal/business/monitorplan"
	"github.com/yuqing/platform/internal/business/report"
	"github.com/yuqing/platform/internal/business/trends"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/engine"
	"github.com/yuqing/platform/internal/pkg/db"
	"github.com/yuqing/platform/internal/pkg/queue"
	"github.com/yuqing/platform/internal/pkg/storage"
	"github.com/yuqing/platform/internal/platform/accountadmin"
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
	if err := checkQueueReadiness(cfg); err != nil {
		panic(err.Error())
	}
	var q queue.Queue = queue.NewMemory()

	var (
		tenantStore      tenant.Store
		authStore        auth.Store
		analysisSvc      *analysis.Service
		monitorStore     monitorplan.Store
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
		q = queue.NewPGQueue(pool, queue.PGQueueOptions{})

		tenantStore = tenant.NewPGStore(pool)
		authStore = auth.NewPGStore(pool)
		analysisSvc = analysis.NewPGService(pool, q, 4)
		monitorStore = monitorplan.NewPGStore(pool)
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
		monitorStore = monitorplan.NewMemoryStore()
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
	authSvc.SetMeter(usageMeter)

	// 新租户注册赠 1 次试用额度（方案 B：试用归 Lite 档体验）。
	authSvc.SetPostRegister(func(ctx context.Context, tenantID string) error {
		return creditSvc.GrantTrial(ctx, tenantID, credit.TrialCredits)
	})

	// In-memory demonstrations may seed an administrator. PostgreSQL roles
	// require explicit CLI provisioning by a verified immutable account ID.
	if cfg.Store.Driver != "postgres" {
		authSvc.SetBootstrapAdminEmail(os.Getenv("YUQING_BOOTSTRAP_ADMIN_EMAIL"))
	}

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

	avatarRoot := cfg.Storage.AvatarRoot
	if avatarRoot == "" {
		avatarRoot = "data/avatars"
	}
	authSvc.EnableAvatarStorage(storage.NewLocalAvatar(avatarRoot))

	tenantSvc := tenant.NewService(tenantStore)

	// Report plan gating resolves the tenant's current plan from the shared
	// tenant store; unknown tenants default to the most restrictive plan.
	planCodeFor := func(tenantID string) string {
		snapshot, err := creditSvc.Snapshot(context.Background(), tenantID)
		if err != nil {
			return "unavailable"
		}
		if snapshot == nil {
			return "free"
		}
		if billing.DefaultPlans()[snapshot.PlanCode] == nil {
			return "unavailable"
		}
		return snapshot.PlanCode
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
	// ReportEngine 在外层声明，以便 API handler 使用
	var reportEngine *engine.RealReportEngine
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
		if cfg.Engines.Insight.URL != "" {
			insight = engine.NewRealInsightEngine(
				cfg.Engines.Insight.URL, os.Getenv("YUQING_BILLING_SERVICE_TOKEN"), keyFor("llm_api_key")).WithLLMOpts(llmOpts)
		} else {
			logger.Warn("pipeline: 未配置 engines.insight.url，情感/话题分析将降级")
		}
		if cfg.Engines.Report.URL != "" {
			reportEngine = engine.NewRealReportEngine(
				cfg.Engines.Report.URL, os.Getenv("YUQING_BILLING_SERVICE_TOKEN"), keyFor("llm_api_key")).WithLLMOpts(llmOpts)
		} else {
			logger.Warn("pipeline: 未配置 engines.report.url，报告生成将降级")
		}
		if cfg.Store.Driver != "postgres" {
			startPipeline(context.Background(), q, analysisSvc, crawler, insight, reportEngine,
				reportSvc, pipelineBudget(cfg), logger, analysisModeFor(creditSvc))
		}
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

	var accountAdminStore accountadmin.Store
	if platformPool != nil {
		accountAdminStore = accountadmin.NewPGStore(platformPool)
	} else {
		accountAdminStore = accountadmin.NewMemoryStore(authStore.(*auth.SharedTenantStore), tenantStore.(*tenant.MemoryStore), creditSvc, paymentSvc)
	}
	accountAdminSvc := accountadmin.NewService(accountAdminStore)
	accountAdminSvc.SetVerificationSender(func(ctx context.Context, actorID, userID, purpose string, actorVersion int64) error {
		return authSvc.SendAccountVerification(ctx, auth.Principal{UserID: actorID, TokenVersion: actorVersion}, userID, purpose)
	})
	var llmCalls *usage.CallService
	if platformPool != nil {
		priceVersion := os.Getenv("YUQING_PROVIDER_PRICE_VERSION")
		if os.Getenv("YUQING_PROVIDER_PRICE_CURRENCY") != "CNY" {
			priceVersion = ""
		}
		llmCalls = usage.NewCallService(platformPool, os.Getenv("YUQING_BILLING_SERVICE_TOKEN"), priceVersion, cfg.LLM.Models)
	}
	return &v1.Services{
		LLMCalls:        llmCalls,
		Auth:            authSvc,
		AccountAdmin:    accountAdminSvc,
		Analysis:        analysisSvc,
		MonitorPlans:    monitorplan.NewService(monitorStore, nil, func(code string) bool { return billing.DefaultPlans()[code] != nil }),
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
		ReportEngine:    reportEngine,
		PGPool:          platformPool,
	}
}

// checkQueueReadiness rejects modes which would silently lose or ACK analyses.
// PostgreSQL admission atomically commits credits, runs, analyses and messages.
func checkQueueReadiness(cfg *config.Config) error {
	if cfg.Store.Driver == "postgres" {
		if cfg.Queue.Driver != "postgres" {
			return fmt.Errorf("app: PostgreSQL store requires persistent PostgreSQL queue; refusing memory fallback (queue.driver=%q)", cfg.Queue.Driver)
		}
		return nil
	}
	switch cfg.Queue.Driver {
	case "", "memory":
		return nil
	case "postgres":
		return fmt.Errorf("app: PostgreSQL queue requires PostgreSQL store")
	default:
		return fmt.Errorf("app: unsupported queue driver %q; refusing memory fallback", cfg.Queue.Driver)
	}
}

// RunPGWorker consumes durable analyses in a separate process. A session-level
// advisory lock serializes all deliveries of the same tenant/task across workers.
func RunPGWorker(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	if err := checkQueueReadiness(cfg); err != nil {
		return err
	}
	if cfg.Store.Driver != "postgres" {
		return fmt.Errorf("app: independent worker requires PostgreSQL store")
	}
	if cfg.Engines.Query.URL == "" {
		return fmt.Errorf("app: worker requires engines.query.url")
	}
	if cfg.DB.Primary == "" {
		return fmt.Errorf("app: worker requires db.primary")
	}
	if logger == nil {
		logger = slog.Default()
	}
	services := Build(cfg, logger)
	pool := services.PGPool
	q := queue.NewPGQueue(pool, queue.PGQueueOptions{})
	defer q.Close()
	keyFor := func(key string) func() string {
		return func() string { value, _ := services.Settings.Get(context.Background(), key); return value }
	}
	crawler := engine.NewRealCrawlerEngine(cfg.Engines.Query.URL, "", keyFor("bocha_api_key"))
	llmOpts := func() (string, string) {
		baseURL, _ := services.Settings.Get(context.Background(), "llm_base_url")
		model, _ := services.Settings.Get(context.Background(), "llm_model")
		return baseURL, model
	}
	var insight *engine.RealInsightEngine
	if cfg.Engines.Insight.URL != "" {
		insight = engine.NewRealInsightEngine(cfg.Engines.Insight.URL, os.Getenv("YUQING_BILLING_SERVICE_TOKEN"), keyFor("llm_api_key")).WithLLMOpts(llmOpts)
	}
	var reportEngine *engine.RealReportEngine
	if cfg.Engines.Report.URL != "" {
		reportEngine = engine.NewRealReportEngine(cfg.Engines.Report.URL, os.Getenv("YUQING_BILLING_SERVICE_TOKEN"), keyFor("llm_api_key")).WithLLMOpts(llmOpts)
	}
	pipeline := analysis.NewPipeline(services.Analysis, &engineFetcher{crawler: crawler}, pipelineBudget(cfg), logger).WithModeFor(analysisModeFor(services.Credits)).WithReportSvc(&pgReportSvcAdapter{reportSvcAdapter: &reportSvcAdapter{svc: services.Report}})
	if insight != nil {
		pipeline.WithAnalyzer(&engineInsightAdapter{ins: insight})
	}
	if reportEngine != nil {
		pipeline.WithGenerator(&engineReportAdapter{rep: reportEngine})
	}
	handler := analysisTaskHandler(logger, func(ctx context.Context, task analysis.TaskMessage) error {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return err
		}
		defer conn.Release()
		var locked bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1), hashtext($2))`, task.TenantID, task.AnalysisID).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return fmt.Errorf("app: analysis %s is already processing", task.AnalysisID)
		}
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var released bool
			if err := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtext($1), hashtext($2))`, task.TenantID, task.AnalysisID).Scan(&released); err != nil || !released {
				logger.Error("worker: advisory unlock failed", "analysis_id", task.AnalysisID, "err", err)
				_ = conn.Conn().Close(unlockCtx)
			}
		}()
		resolved, current, err := services.Analysis.ResolveTask(ctx, task)
		if err != nil {
			return err
		}
		if !current {
			return nil
		}
		task = resolved
		if err := services.Analysis.RecoverInterrupted(ctx, task.TenantID, task.AnalysisID, task.RunID); err != nil {
			return err
		}
		return pipeline.Handle(ctx, task)
	})
	if err := q.Subscribe(ctx, analysis.TopicAnalysisTasks, handler); err != nil {
		return err
	}
	logger.Info("worker: subscribed to durable analysis tasks")
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-q.Errors():
			logger.Error("worker: delivery failed; retained for retry", "err", err)
		}
	}
}
