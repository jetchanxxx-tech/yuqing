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
