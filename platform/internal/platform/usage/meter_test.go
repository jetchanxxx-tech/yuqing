package usage

import (
	"context"
	"testing"

	"github.com/yuging/platform/internal/pkg/llm"
)

func TestMeter_BudgetStatus_defaultsToOk(t *testing.T) {
	m := NewMeter()
	status, err := m.BudgetStatus(context.Background(), "unknown_tenant")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "ok" {
		t.Errorf("default status = %q, want ok", status.Status)
	}
}

func TestMeter_Record_incrementsSpent(t *testing.T) {
	m := NewMeter()
	m.SetQuota("t1", 1000, llm.BudgetHardCap)

	m.Record(context.Background(), llm.UsageEvent{
		TenantID: "t1", PromptTokens: 100, CompletionTokens: 100,
	})
	m.Record(context.Background(), llm.UsageEvent{
		TenantID: "t1", PromptTokens: 300, CompletionTokens: 100,
	})

	status, _ := m.BudgetStatus(context.Background(), "t1")
	if status.SpentTokens != 600 {
		t.Errorf("spent = %d, want 600", status.SpentTokens)
	}
}

func TestMeter_BudgetStatus_warnAt80Percent(t *testing.T) {
	m := NewMeter()
	m.SetQuota("t_warn", 1000, llm.BudgetHardCap)
	m.Record(context.Background(), llm.UsageEvent{
		TenantID: "t_warn", PromptTokens: 810, CompletionTokens: 0,
	})

	status, _ := m.BudgetStatus(context.Background(), "t_warn")
	if status.Status != "warn" {
		t.Errorf("status at 81%% = %q, want warn", status.Status)
	}
}

func TestMeter_BudgetStatus_exceeded(t *testing.T) {
	m := NewMeter()
	m.SetQuota("t_full", 1000, llm.BudgetHardCap)
	m.Record(context.Background(), llm.UsageEvent{
		TenantID: "t_full", PromptTokens: 1000, CompletionTokens: 100,
	})

	status, _ := m.BudgetStatus(context.Background(), "t_full")
	if status.Status != "exceeded" {
		t.Errorf("status = %q, want exceeded", status.Status)
	}
}

func TestMeter_isolatesTenants(t *testing.T) {
	m := NewMeter()
	m.SetQuota("alice", 1000, llm.BudgetHardCap)
	m.SetQuota("bob", 500, llm.BudgetHardCap)

	m.Record(context.Background(), llm.UsageEvent{TenantID: "alice", PromptTokens: 800})
	m.Record(context.Background(), llm.UsageEvent{TenantID: "bob", PromptTokens: 100})

	alice, _ := m.BudgetStatus(context.Background(), "alice")
	bob, _ := m.BudgetStatus(context.Background(), "bob")

	if alice.SpentTokens != 800 {
		t.Errorf("alice spent = %d, want 800", alice.SpentTokens)
	}
	if bob.SpentTokens != 100 {
		t.Errorf("bob spent = %d, want 100", bob.SpentTokens)
	}
}

// --- Aggregate (platform-wide rollup for /admin/usage) -------------------------

func TestMeter_Aggregate_empty(t *testing.T) {
	m := NewMeter()
	if agg := m.Aggregate(); len(agg) != 0 {
		t.Errorf("fresh meter aggregates = %v, want empty", agg)
	}
}

func TestMeter_Aggregate_perTenantSpend(t *testing.T) {
	ctx := context.Background()
	m := NewMeter()
	m.SetQuota("t1", 1000, llm.BudgetHardCap)
	m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 100, CompletionTokens: 50})
	m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 10})
	m.Record(ctx, llm.UsageEvent{TenantID: "t2", PromptTokens: 10, CacheTokens: 5})

	agg := m.Aggregate()
	if len(agg) != 2 {
		t.Fatalf("tenants = %d, want 2 (%v)", len(agg), agg)
	}
	if agg["t1"] != 160 {
		t.Errorf("t1 spent = %d, want 160", agg["t1"])
	}
	if agg["t2"] != 15 {
		t.Errorf("t2 spent = %d, want 15", agg["t2"])
	}

	t.Run("copy is detached", func(t *testing.T) {
		agg["t1"] = 999
		m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 1})
		if got := m.Aggregate()["t1"]; got != 161 {
			t.Errorf("t1 after external mutation = %d, want 161", got)
		}
	})
}
