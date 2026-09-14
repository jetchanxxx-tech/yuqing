package billing

import (
	"testing"
)

// 方案 B「渗透型」套餐目录 —— 用户 2026-09-15 确认：
// Lite 99 / Pro 999 / Enterprise 4999，加购 69/次刻意低于 Pro 套餐内单次。
func TestPlanCatalog_PenetratingPlanB(t *testing.T) {
	plans := DefaultPlans()

	lite := plans["lite"]
	if lite.Name != "速览版" || lite.PriceMonthlyCNY != 9900 {
		t.Errorf("lite = %s %d, want 速览版 9900", lite.Name, lite.PriceMonthlyCNY)
	}
	if lite.CreditsPerCycle != 4 || lite.AnalysisMode != ModeQuick {
		t.Errorf("lite credits=%d mode=%q, want 4/quick", lite.CreditsPerCycle, lite.AnalysisMode)
	}

	pro := plans["pro"]
	if pro.PriceMonthlyCNY != 99900 || pro.CreditsPerCycle != 10 || pro.AnalysisMode != ModeFull {
		t.Errorf("pro = %d credits=%d mode=%q, want 99900/10/full",
			pro.PriceMonthlyCNY, pro.CreditsPerCycle, pro.AnalysisMode)
	}

	ent := plans["enterprise"]
	if ent.PriceMonthlyCNY != 499900 || ent.CreditsPerCycle != 50 {
		t.Errorf("enterprise = %d credits=%d, want 499900/50",
			ent.PriceMonthlyCNY, ent.CreditsPerCycle)
	}
	if !ent.PriorityQueue {
		t.Error("enterprise should have priority queue")
	}
	if plans["free"].CreditsPerCycle != 0 {
		t.Error("free plan is never purchased; trial grant is separate")
	}
}

// 决策点 2（用户书面确认）：渗透定价 —— 加购单价必须低于 Pro 套餐内单次，
// 「买套餐比单买划算」是刻意的转化设计，不是定价错误。
func TestPlanCatalog_PenetrationPricing(t *testing.T) {
	proPerReport := DefaultPlans()["pro"].PriceMonthlyCNY / DefaultPlans()["pro"].CreditsPerCycle // 9990 ≈ ¥100
	addon := AddonSkus()["addon_report"]
	if addon == nil {
		t.Fatal("addon_report SKU missing")
	}
	if addon.PriceCents != 6900 || addon.Credits != 1 {
		t.Errorf("addon = %d cents / %d credits, want 6900/1", addon.PriceCents, addon.Credits)
	}
	if addon.PriceCents >= proPerReport {
		t.Errorf("penetration broken: addon %d >= pro per-report %d", addon.PriceCents, proPerReport)
	}
}

func TestPlanFeatures(t *testing.T) {
	plans := DefaultPlans()
	if !plans["free"].IsFeatureEnabled("reports:html") {
		t.Error("free should have HTML reports")
	}
	if plans["free"].IsFeatureEnabled("reports:pdf") {
		t.Error("free should NOT have PDF reports")
	}
	if !plans["pro"].IsFeatureEnabled("reports:pdf") {
		t.Error("pro should have PDF reports")
	}
	if !plans["pro"].IsFeatureEnabled("api:read") {
		t.Error("pro should have read API")
	}
	if plans["lite"].IsFeatureEnabled("reports:pdf") {
		t.Error("lite should NOT have PDF reports")
	}
	if !plans["enterprise"].IsFeatureEnabled("api:write") {
		t.Error("enterprise should have write API")
	}
}

func TestPlanFeatures_seats(t *testing.T) {
	plans := DefaultPlans()
	if got := plans["free"].MaxSeats(); got != 1 {
		t.Errorf("free seats = %d, want 1", got)
	}
	if got := plans["pro"].MaxSeats(); got != 3 {
		t.Errorf("pro seats = %d, want 3", got)
	}
	if got := plans["enterprise"].MaxSeats(); got != 0 {
		t.Errorf("enterprise seats = %d, want 0 (unlimited)", got)
	}
}

func TestPlanFeatures_concurrency(t *testing.T) {
	plans := DefaultPlans()
	if got := plans["free"].MaxConcurrency(); got != 1 {
		t.Errorf("free concurrency = %d, want 1", got)
	}
	if got := plans["enterprise"].MaxConcurrency(); got != 5 {
		t.Errorf("enterprise concurrency = %d, want 5", got)
	}
}

func TestProratedPrice(t *testing.T) {
	// Upgrade mid-month: customer pays only remaining days.
	// 30-day month, 15 days remaining → 50% of price.
	monthlyPrice := 9900 // 99.00 CNY in cents
	daysRemaining := 15
	got := ProratedPrice(monthlyPrice, daysRemaining, 30)
	want := 4950 // 49.50 CNY
	if got != want {
		t.Errorf("prorated = %d, want %d", got, want)
	}
}
