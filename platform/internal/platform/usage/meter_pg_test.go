package usage

import (
	"context"
	"testing"

	"github.com/yuqing/platform/internal/pkg/llm"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

// meterContract 是计量的行为契约，参数化到任意实现上执行。
//
// 断言逐条取自内存版既有测试（meter_test.go）：预算状态阈值、租户隔离、
// 全租户聚合、聚合结果必须是副本。
func meterContract(t *testing.T, newMeter func(t *testing.T) PlatformMeter) {
	t.Helper()
	ctx := context.Background()

	t.Run("BudgetStatus 默认 ok", func(t *testing.T) {
		m := newMeter(t)
		status, err := m.BudgetStatus(ctx, "unknown_tenant")
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "ok" {
			t.Errorf("default status = %q, want ok", status.Status)
		}
	})

	t.Run("Record 累加已用 token", func(t *testing.T) {
		m := newMeter(t)
		m.SetQuota("t1", 1000, llm.BudgetHardCap)

		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 100, CompletionTokens: 100}); err != nil {
			t.Fatalf("Record failed: %v", err)
		}
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 300, CompletionTokens: 100}); err != nil {
			t.Fatalf("Record failed: %v", err)
		}

		status, err := m.BudgetStatus(ctx, "t1")
		if err != nil {
			t.Fatal(err)
		}
		if status.SpentTokens != 600 {
			t.Errorf("spent = %d, want 600", status.SpentTokens)
		}
		if status.QuotaTokens != 1000 {
			t.Errorf("quota = %d, want 1000", status.QuotaTokens)
		}
	})

	t.Run("BudgetStatus 80% 起 warn", func(t *testing.T) {
		m := newMeter(t)
		m.SetQuota("t_warn", 1000, llm.BudgetHardCap)
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t_warn", PromptTokens: 810}); err != nil {
			t.Fatal(err)
		}

		status, _ := m.BudgetStatus(ctx, "t_warn")
		if status.Status != "warn" {
			t.Errorf("status at 81%% = %q, want warn", status.Status)
		}
	})

	t.Run("BudgetStatus 超额为 exceeded", func(t *testing.T) {
		m := newMeter(t)
		m.SetQuota("t_full", 1000, llm.BudgetHardCap)
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t_full", PromptTokens: 1000, CompletionTokens: 100}); err != nil {
			t.Fatal(err)
		}

		status, _ := m.BudgetStatus(ctx, "t_full")
		if status.Status != "exceeded" {
			t.Errorf("status = %q, want exceeded", status.Status)
		}
	})

	t.Run("租户之间互不影响", func(t *testing.T) {
		m := newMeter(t)
		m.SetQuota("alice", 1000, llm.BudgetHardCap)
		m.SetQuota("bob", 500, llm.BudgetHardCap)

		if err := m.Record(ctx, llm.UsageEvent{TenantID: "alice", PromptTokens: 800}); err != nil {
			t.Fatal(err)
		}
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "bob", PromptTokens: 100}); err != nil {
			t.Fatal(err)
		}

		alice, _ := m.BudgetStatus(ctx, "alice")
		bob, _ := m.BudgetStatus(ctx, "bob")

		if alice.SpentTokens != 800 {
			t.Errorf("alice spent = %d, want 800", alice.SpentTokens)
		}
		if bob.SpentTokens != 100 {
			t.Errorf("bob spent = %d, want 100", bob.SpentTokens)
		}
	})

	t.Run("Aggregate 空表为空", func(t *testing.T) {
		m := newMeter(t)
		if agg := m.Aggregate(); len(agg) != 0 {
			t.Errorf("fresh meter aggregates = %v, want empty", agg)
		}
	})

	t.Run("Aggregate 按租户汇总且返回副本", func(t *testing.T) {
		m := newMeter(t)
		m.SetQuota("t1", 1000, llm.BudgetHardCap)
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 100, CompletionTokens: 50}); err != nil {
			t.Fatal(err)
		}
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 10}); err != nil {
			t.Fatal(err)
		}
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t2", PromptTokens: 10, CacheTokens: 5}); err != nil {
			t.Fatal(err)
		}

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

		agg["t1"] = 999
		if err := m.Record(ctx, llm.UsageEvent{TenantID: "t1", PromptTokens: 1}); err != nil {
			t.Fatal(err)
		}
		if got := m.Aggregate()["t1"]; got != 161 {
			t.Errorf("t1 after external mutation = %d, want 161（聚合结果必须是副本）", got)
		}
	})
}

// 本地（无 YUQING_TEST_PG_URL）也有信号：契约本身必须被内存版满足。
func TestMeter_Memory_satisfiesContract(t *testing.T) {
	meterContract(t, func(t *testing.T) PlatformMeter { return NewMeter() })
}

func TestPGMeter_satisfiesContract(t *testing.T) {
	meterContract(t, func(t *testing.T) PlatformMeter {
		pool := pgtest.Pool(t, "usage")
		return NewPGMeter(pool)
	})
}

// ── PG 专有 ────────────────────────────────────────────────────

// 持久化：换实例（模拟进程重启）后已用 token 仍在，预算状态不再归零 ——
// 这正是内存版最严重的缺陷（重启即放过已超额租户）。
func TestPGMeter_survivesNewInstance(t *testing.T) {
	pool := pgtest.Pool(t, "usage")
	ctx := context.Background()

	const tenantID = "t-persist"
	first := NewPGMeter(pool)
	first.SetQuota(tenantID, 1000, llm.BudgetHardCap)
	if err := first.Record(ctx, llm.UsageEvent{TenantID: tenantID, PromptTokens: 900}); err != nil {
		t.Fatal(err)
	}

	// 新实例 = 重启后的进程：配额需要重新配置（配额仍存在内存里），
	// 但已用 token 从 usage_events 读回。
	second := NewPGMeter(pool)
	second.SetQuota(tenantID, 1000, llm.BudgetHardCap)

	status, err := second.BudgetStatus(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if status.SpentTokens != 900 {
		t.Errorf("重启后 spent = %d, want 900", status.SpentTokens)
	}
	if status.Status != "warn" {
		t.Errorf("重启后 status = %q, want warn", status.Status)
	}
}

// usage_events 是流水表：Record 不幂等（内存版同样只是累加，
// 表上也没有可用于去重的唯一键），重复投递同一条事件会重复计数。
func TestPGMeter_recordIsNotIdempotent(t *testing.T) {
	pool := pgtest.Pool(t, "usage")
	ctx := context.Background()
	m := NewPGMeter(pool)

	e := llm.UsageEvent{
		TenantID: "t1", UserID: "u1", Model: "deepseek-chat",
		PromptTokens: 10, CompletionTokens: 5,
		CostMicroCNY: 120, BilledMicroCNY: 200, AnalysisID: "a1",
	}
	for i := 0; i < 2; i++ {
		if err := m.Record(ctx, e); err != nil {
			t.Fatalf("Record #%d failed: %v", i, err)
		}
	}

	status, err := m.BudgetStatus(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if status.SpentTokens != 30 {
		t.Errorf("spent = %d, want 30（两次事件各 15，流水表不去重）", status.SpentTokens)
	}
}

// 成本字段与 token 分档写进 usage_events（供 /admin/usage 与出账读取）。
func TestPGMeter_persistsCostColumns(t *testing.T) {
	pool := pgtest.Pool(t, "usage")
	ctx := context.Background()
	m := NewPGMeter(pool)

	if err := m.Record(ctx, llm.UsageEvent{
		TenantID: "t-cost", UserID: "u1", Model: "deepseek-chat", AnalysisID: "a1",
		PromptTokens: 1000, CompletionTokens: 2000, CacheTokens: 50,
		CostMicroCNY: 1500, BilledMicroCNY: 3000,
	}); err != nil {
		t.Fatal(err)
	}

	var (
		prompt, completion, cache int
		cost, billed              int64
		model, analysisID, userID string
	)
	err := pool.QueryRow(ctx,
		`SELECT prompt_tokens, completion_tokens, cache_tokens, cost_micro_cny, billed_micro_cny,
		        model, analysis_id, user_id
		 FROM usage_events WHERE tenant_id = $1`, "t-cost").
		Scan(&prompt, &completion, &cache, &cost, &billed, &model, &analysisID, &userID)
	if err != nil {
		t.Fatalf("读取 usage_events 失败: %v", err)
	}
	if prompt != 1000 || completion != 2000 || cache != 50 {
		t.Errorf("tokens = %d/%d/%d, want 1000/2000/50", prompt, completion, cache)
	}
	if cost != 1500 || billed != 3000 {
		t.Errorf("cost = %d/%d, want 1500/3000（成本与计费分列）", cost, billed)
	}
	if model != "deepseek-chat" || analysisID != "a1" || userID != "u1" {
		t.Errorf("溯源字段 = %q/%q/%q, want deepseek-chat/a1/u1", model, analysisID, userID)
	}
}
