// Package billing defines subscription plans, pricing, and invoice generation.
package billing

import "github.com/yuging/platform/internal/pkg/llm"

// Plan defines a subscription tier.
type Plan struct {
	Code               string            `json:"code"`
	Name               string            `json:"name"`
	PriceMonthlyCNY    int               `json:"price_monthly_cny"` // in cents (fen)
	TokenQuotaM        int               `json:"token_quota_m"`     // millions
	BudgetMode         llm.BudgetMode    `json:"budget_mode"`
	MaxConcurrent      int               `json:"max_concurrent"`
	MaxSeatsVal        int               `json:"max_seats"`         // 0 = unlimited
	RetentionDays      int               `json:"retention_days"`
	EnabledFeatures    map[string]bool   `json:"enabled_features"`
	OverageInRatePerM  int               `json:"overage_in_rate_per_m"`
	OverageOutRatePerM int               `json:"overage_out_rate_per_m"`
}

// IsFeatureEnabled checks if a feature flag is on for this plan.
func (p *Plan) IsFeatureEnabled(feature string) bool {
	return p.EnabledFeatures[feature]
}

// MaxSeats returns the maximum team seats. 0 = unlimited.
func (p *Plan) MaxSeats() int { return p.MaxSeatsVal }

// MaxConcurrency returns the maximum concurrent analyses.
func (p *Plan) MaxConcurrency() int { return p.MaxConcurrent }

// DefaultPlans returns the four standard plans.
func DefaultPlans() map[string]*Plan {
	return map[string]*Plan{
		"free": {
			Code: "free", Name: "体验版", PriceMonthlyCNY: 0,
			TokenQuotaM: 1, BudgetMode: llm.BudgetHardCap,
			MaxConcurrent: 1, MaxSeatsVal: 1, RetentionDays: 30,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": false,
				"reports:pdf": false, "reports:docx": false,
				"api:read": false, "api:write": false,
				"forum:debate": false, "custom_models": false,
			},
		},
		"pro": {
			Code: "pro", Name: "专业版", PriceMonthlyCNY: 9900,
			TokenQuotaM: 10, BudgetMode: llm.BudgetHardCap,
			MaxConcurrent: 2, MaxSeatsVal: 3, RetentionDays: 90,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": true,
				"reports:pdf": false, "reports:docx": false,
				"api:read": false, "api:write": false,
				"forum:debate": true, "custom_models": false,
			},
		},
		"business": {
			Code: "business", Name: "企业版", PriceMonthlyCNY: 49900,
			TokenQuotaM: 100, BudgetMode: llm.BudgetOverage,
			MaxConcurrent: 5, MaxSeatsVal: 20, RetentionDays: 365,
			OverageInRatePerM: 28, OverageOutRatePerM: 84,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": true,
				"reports:pdf": true, "reports:docx": true,
				"api:read": true, "api:write": false,
				"forum:debate": true, "custom_models": false,
			},
		},
		"enterprise": {
			Code: "enterprise", Name: "旗舰版", PriceMonthlyCNY: 0, // custom
			TokenQuotaM: 0, BudgetMode: llm.BudgetNone,
			MaxConcurrent: 20, MaxSeatsVal: 0, RetentionDays: 0,
			EnabledFeatures: map[string]bool{
				"reports:html": true, "reports:markdown": true,
				"reports:pdf": true, "reports:docx": true,
				"api:read": true, "api:write": true,
				"forum:debate": true, "custom_models": true,
			},
		},
	}
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
