package billing

import (
	"testing"
)

func TestPlanFeatures(t *testing.T) {
	plans := DefaultPlans()
	free := plans["free"]
	pro := plans["pro"]
	business := plans["business"]

	if free.Name != "体验版" {
		t.Errorf("free name = %q", free.Name)
	}
	if pro.PriceMonthlyCNY != 9900 { // 99 CNY in cents
		t.Errorf("pro price = %d, want 9900", pro.PriceMonthlyCNY)
	}
	if business.BudgetMode != "overage" {
		t.Errorf("business budget mode = %q, want overage", business.BudgetMode)
	}
	if !free.IsFeatureEnabled("reports:html") {
		t.Error("free should have HTML reports")
	}
	if free.IsFeatureEnabled("reports:pdf") {
		t.Error("free should NOT have PDF reports")
	}
	if !business.IsFeatureEnabled("api:read") {
		t.Error("business should have read API")
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
	if got := plans["business"].MaxSeats(); got != 20 {
		t.Errorf("business seats = %d, want 20", got)
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
	if got := plans["business"].MaxConcurrency(); got != 5 {
		t.Errorf("business concurrency = %d, want 5", got)
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
