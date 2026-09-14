// Package billing defines subscription plans, pricing, and invoice generation.
//
// 收费体系 = 方案 B「渗透型」（docs/planning/BILLING_PLAN.html，用户 2026-09-15 确认）：
// 速览 Lite 99（Flash 3 维速览 ×4）/ 研判 Pro 999（思考型 5 维 ×10，PDF/API）/
// 旗舰 Enterprise 4999（50 次 + 优先队列）。加购 69/次刻意低于 Pro 套餐内
// 单次 —— 「买套餐更划算」的渗透定价（决策点 2 书面确认，有测试锁定）。
package billing

import "github.com/yuqing/platform/internal/pkg/llm"

// 分析模式：Lite/免费档跑 quick（3 维速览），Pro 及以上跑 full（5 维思考型）。
const (
	ModeQuick = "quick"
	ModeFull  = "full"
)

// Plan defines a subscription tier.
type Plan struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	PriceMonthlyCNY int    `json:"price_monthly_cny"` // in cents (fen)
	// CreditsPerCycle 是套餐含的报告额度（次/月）。0 = 套餐不发售（free 仅注册赠）。
	CreditsPerCycle int `json:"credits_per_cycle"`
	// AnalysisMode 透传洞察引擎：quick=3 维速览（热度/情感/原因），
	// full=5 维完整研判。决定单次成本与页面对外宣传的档位差异。
	AnalysisMode string `json:"analysis_mode"`
	// PriorityQueue 旗舰版优先队列（P2 调度接入，先占住产品字段）。
	PriorityQueue   bool           `json:"priority_queue"`
	TokenQuotaM     int            `json:"token_quota_m"` // millions
	BudgetMode      llm.BudgetMode `json:"budget_mode"`
	MaxConcurrent   int            `json:"max_concurrent"`
	MaxSeatsVal     int            `json:"max_seats"` // 0 = unlimited
	RetentionDays   int            `json:"retention_days"`
	EnabledFeatures map[string]bool `json:"enabled_features"`
	OverageInRatePerM  int `json:"overage_in_rate_per_m"`
	OverageOutRatePerM int `json:"overage_out_rate_per_m"`
}

// IsFeatureEnabled checks if a feature flag is on for this plan.
func (p *Plan) IsFeatureEnabled(feature string) bool {
	return p.EnabledFeatures[feature]
}

// MaxSeats returns the maximum team seats. 0 = unlimited.
func (p *Plan) MaxSeats() int { return p.MaxSeatsVal }

// MaxConcurrency returns the maximum concurrent analyses.
func (p *Plan) MaxConcurrency() int { return p.MaxConcurrent }

// DefaultPlans returns the standard plans (方案 B). 线性比率 99→999（×10）→4999（×5）
// 参考 LLM 供应商 lite/pro/enterprise 分层惯例。
func DefaultPlans() map[string]*Plan {
	return map[string]*Plan{
		"free": {
			Code: "free", Name: "体验版", PriceMonthlyCNY: 0,
			CreditsPerCycle: 0, AnalysisMode: ModeQuick,
			TokenQuotaM: 1, BudgetMode: llm.BudgetHardCap,
			MaxConcurrent: 1, MaxSeatsVal: 1, RetentionDays: 30,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": false,
				"reports:pdf": false, "reports:docx": false,
				"api:read": false, "api:write": false,
				"forum:debate": false, "custom_models": false,
			},
		},
		"lite": {
			Code: "lite", Name: "速览版", PriceMonthlyCNY: 9900,
			CreditsPerCycle: 4, AnalysisMode: ModeQuick,
			TokenQuotaM: 5, BudgetMode: llm.BudgetHardCap,
			MaxConcurrent: 1, MaxSeatsVal: 1, RetentionDays: 90,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": true,
				"reports:pdf": false, "reports:docx": false,
				"api:read": false, "api:write": false,
				"forum:debate": false, "custom_models": false,
			},
		},
		"pro": {
			Code: "pro", Name: "研判版", PriceMonthlyCNY: 99900,
			CreditsPerCycle: 10, AnalysisMode: ModeFull,
			TokenQuotaM: 50, BudgetMode: llm.BudgetHardCap,
			MaxConcurrent: 2, MaxSeatsVal: 3, RetentionDays: 180,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": true,
				"reports:pdf": true, "reports:docx": false,
				"api:read": true, "api:write": false,
				"forum:debate": true, "custom_models": false,
			},
		},
		"enterprise": {
			Code: "enterprise", Name: "旗舰版", PriceMonthlyCNY: 499900,
			CreditsPerCycle: 50, AnalysisMode: ModeFull,
			PriorityQueue: true,
			TokenQuotaM:   0, BudgetMode: llm.BudgetNone,
			MaxConcurrent: 5, MaxSeatsVal: 0, RetentionDays: 365,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": true,
				"reports:pdf": true, "reports:docx": true,
				"api:read": true, "api:write": true,
				"forum:debate": true, "custom_models": true,
			},
		},
	}
}

// SKU is a purchasable catalog entry: a subscription plan or a credit addon.
type SKU struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // plan | addon
	PriceCents int    `json:"price_cents"`
	Credits    int    `json:"credits"`
	Plan       *Plan  `json:"plan,omitempty"`
}

// AddonSkus returns the credit top-up packages. 加购 69/次 < Pro 套餐内单次
// 99.9 元/次 —— 渗透定价（有测试锁定，改价需同步改 TestPlanCatalog_PenetrationPricing）。
func AddonSkus() map[string]*SKU {
	return map[string]*SKU{
		"addon_report": {
			Code: "addon_report", Name: "报告加购包", Kind: "addon",
			PriceCents: 6900, Credits: 1,
		},
	}
}

// ResolveSKU 按 code 查可购买目录（套餐 + 加购）。
func ResolveSKU(code string) *SKU {
	if sku, ok := AddonSkus()[code]; ok {
		return sku
	}
	if p, ok := DefaultPlans()[code]; ok && p.CreditsPerCycle > 0 {
		return &SKU{Code: p.Code, Name: p.Name, Kind: "plan",
			PriceCents: p.PriceMonthlyCNY, Credits: p.CreditsPerCycle, Plan: p}
	}
	return nil
}

// ProratedPrice calculates the price for a mid-cycle plan change.
func ProratedPrice(monthlyPrice, daysRemaining, daysInPeriod int) int {
	if daysInPeriod <= 0 {
		return 0
	}
	return monthlyPrice * daysRemaining / daysInPeriod
}

// Subscription represents an active subscription.
type Subscription struct {
	ID                 string `json:"id"`
	TenantID           string `json:"tenant_id"`
	PlanCode           string `json:"plan_code"`
	Status             string `json:"status"` // trialing, active, past_due, canceled
	CurrentPeriodStart string `json:"current_period_start"`
	CurrentPeriodEnd   string `json:"current_period_end"`
	CancelAtPeriodEnd  bool   `json:"cancel_at_period_end"`
}
