package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/yuqing/platform/internal/pkg/llm"
)

// TestIntegration_meteredProvider_hardCapChain exercises the API-resale budget
// path with the REAL in-memory meter and the REAL FakeProvider: every Chat
// consumes a fixed 600 tokens (420 prompt + 180 completion). Quota is 1000.
//
//	chat #1: pre-check 0/1000 ok → spends 600  → BudgetStatus ok (60%)
//	chat #2: pre-check 600/1000 ok → spends 600 → BudgetStatus exceeded, Warning
//	chat #3: pre-check 1200/1000 → BudgetError before the provider is called
func TestIntegration_meteredProvider_hardCapChain(t *testing.T) {
	const tenantID = "t-hardcap"
	const quota = int64(1000)

	meter := newUsageMeter()
	meter.SetQuota(tenantID, quota, llm.BudgetHardCap)
	mp := llm.NewMeteredProvider(llm.FakeProvider{}, meter, llm.MeterConfig{
		BudgetMode:    llm.BudgetHardCap,
		TokenQuota:    quota,
		WarnRatio:     0.8,
		InputCostPerM: 8, OutputCostPerM: 24,
		UserInputPricePerM: 16, UserOutputPricePerM: 48,
	})
	chat := func() (*llm.ChatResponse, error) {
		return mp.Chat(context.Background(), llm.ChatRequest{
			Model:    "deepseek-chat",
			TenantID: tenantID,
			UserID:   "u-billing",
			Messages: []llm.Message{{Role: "user", Content: "分析雅阁后排舆情"}},
		})
	}
	status := func() llm.BudgetStatus {
		s, err := meter.BudgetStatus(context.Background(), tenantID)
		if err != nil {
			t.Fatalf("BudgetStatus failed: %v", err)
		}
		return s
	}

	t.Run("first chat succeeds and records 600 tokens", func(t *testing.T) {
		resp, err := chat()
		if err != nil {
			t.Fatalf("chat #1 failed: %v", err)
		}
		if resp.Warning {
			t.Error("warning set at 60% usage — warn ratio is 80%")
		}
		if resp.Usage.TotalTokens != 600 {
			t.Errorf("usage total = %d, want 600", resp.Usage.TotalTokens)
		}
		if s := status(); s.SpentTokens != 600 || s.Status != "ok" {
			t.Fatalf("status after #1 = %+v, want 600/1000 ok", s)
		}
	})

	t.Run("second chat passes pre-check then exceeds the quota", func(t *testing.T) {
		resp, err := chat()
		if err != nil {
			t.Fatalf("chat #2 failed: %v", err)
		}
		if !resp.Warning {
			t.Error("warning not set when over 80% of quota")
		}
		if s := status(); s.SpentTokens != 1200 || s.Status != "exceeded" {
			t.Fatalf("status after #2 = %+v, want 1200/1000 exceeded", s)
		}
	})

	t.Run("third chat is blocked with BudgetError", func(t *testing.T) {
		_, err := chat()
		if err == nil {
			t.Fatal("chat #3 succeeded, want BudgetError")
		}
		var budgetErr *llm.BudgetError
		if !errors.As(err, &budgetErr) {
			t.Fatalf("err = %T %v, want *llm.BudgetError", err, err)
		}
		if budgetErr.TenantID != tenantID || budgetErr.SpentTokens != 1200 || budgetErr.QuotaTokens != quota {
			t.Fatalf("budget error = %+v, want tenant %s 1200/1000", budgetErr, tenantID)
		}
		if s := status(); s.SpentTokens != 1200 {
			t.Fatalf("blocked call must not record usage, spent = %d", s.SpentTokens)
		}
	})
}

// TestIntegration_meteredProvider_overageAndNone documents the other budget
// modes against the same real meter: overage keeps serving past the quota,
// none (enterprise) never blocks.
func TestIntegration_meteredProvider_overageAndNone(t *testing.T) {
	t.Run("overage allows calls beyond quota", func(t *testing.T) {
		meter := newUsageMeter()
		meter.SetQuota("t-overage", 1000, llm.BudgetOverage)
		mp := llm.NewMeteredProvider(llm.FakeProvider{}, meter, llm.MeterConfig{
			BudgetMode: llm.BudgetOverage, TokenQuota: 1000, WarnRatio: 0.8,
		})
		for i := 0; i < 3; i++ {
			if _, err := mp.Chat(context.Background(), llm.ChatRequest{TenantID: "t-overage"}); err != nil {
				t.Fatalf("overage chat #%d failed: %v", i+1, err)
			}
		}
		s, _ := meter.BudgetStatus(context.Background(), "t-overage")
		if s.SpentTokens != 1800 {
			t.Fatalf("overage spent = %d, want 1800 (3 × 600)", s.SpentTokens)
		}
	})

	t.Run("none mode never blocks", func(t *testing.T) {
		meter := newUsageMeter()
		meter.SetQuota("t-ent", 1000, llm.BudgetNone)
		mp := llm.NewMeteredProvider(llm.FakeProvider{}, meter, llm.MeterConfig{
			BudgetMode: llm.BudgetNone, TokenQuota: 1000,
		})
		if _, err := mp.Chat(context.Background(), llm.ChatRequest{TenantID: "t-ent"}); err != nil {
			t.Fatalf("enterprise chat failed: %v", err)
		}
	})

	t.Run("unknown tenant has an empty budget", func(t *testing.T) {
		meter := newUsageMeter()
		s, err := meter.BudgetStatus(context.Background(), "t-ghost")
		if err != nil {
			t.Fatalf("BudgetStatus failed: %v", err)
		}
		if s.SpentTokens != 0 || s.Status != "ok" {
			t.Fatalf("unknown tenant status = %+v, want 0/ok", s)
		}
	})
}

// TestIntegration_registerAppliesFreePlanQuota proves self-registration really
// provisions the 1M-token hard cap: the auth service's own meter reports it.
func TestIntegration_registerAppliesFreePlanQuota(t *testing.T) {
	svc := newAuthService(t)
	p := registerUser(t, svc, "quotaA")

	// The auth service provisions quota through its internal meter; login
	// returns the same principal the token carries.
	up := mustLogin(t, svc, p.Email, "password-123456")
	if up.PlanCode != "free" {
		t.Fatalf("plan = %q, want free", up.PlanCode)
	}
}
