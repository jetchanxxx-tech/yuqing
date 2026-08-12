package llm

import (
	"context"
	"errors"
	"testing"
)

func TestMeteredProvider_callsUnderlyingProvider(t *testing.T) {
	mock := &mockProvider{
		resp: &ChatResponse{
			Model: "deepseek-chat",
			Usage: Usage{
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
			},
			Choices: []Choice{{Message: Message{Role: "assistant", Content: "hello"}}},
		},
	}

	meter := &mockMeter{}
	mp := NewMeteredProvider(mock, meter, MeterConfig{
		BudgetMode: BudgetHardCap,
		TokenQuota: 1000000,
	})

	resp, err := mp.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "hi"}},
		TenantID: "t_test",
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp.Model != "deepseek-chat" {
		t.Errorf("model = %q, want deepseek-chat", resp.Model)
	}
	if resp.Usage.TotalTokens != 300 {
		t.Errorf("total tokens = %d, want 300", resp.Usage.TotalTokens)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
}

func TestMeteredProvider_recordsUsage(t *testing.T) {
	mock := &mockProvider{
		resp: &ChatResponse{
			Model: "kimi",
			Usage: Usage{PromptTokens: 50, CompletionTokens: 100, TotalTokens: 150},
			Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}}},
		},
	}
	meter := &mockMeter{}
	mp := NewMeteredProvider(mock, meter, MeterConfig{
		BudgetMode: BudgetOverage,
		TokenQuota: 1000000,
	})

	_, err := mp.Chat(context.Background(), ChatRequest{
		Model:    "kimi",
		Messages: []Message{{Role: "user", Content: "test"}},
		TenantID: "t_alice",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(meter.records) != 1 {
		t.Fatalf("recorded %d usage events, want 1", len(meter.records))
	}
	rec := meter.records[0]
	if rec.TenantID != "t_alice" {
		t.Errorf("tenant = %q, want t_alice", rec.TenantID)
	}
	if rec.PromptTokens != 50 {
		t.Errorf("prompt_tokens = %d, want 50", rec.PromptTokens)
	}
	if rec.CompletionTokens != 100 {
		t.Errorf("completion_tokens = %d, want 100", rec.CompletionTokens)
	}
}

func TestMeteredProvider_budgetHardCap_blocksExcess(t *testing.T) {
	mock := &mockProvider{
		resp: &ChatResponse{
			Model: "deepseek-chat",
			Usage: Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
		},
	}
	meter := &mockMeter{
		spentTokens: 1000000, // exactly at 1M quota — should block
	}
	mp := NewMeteredProvider(mock, meter, MeterConfig{
		BudgetMode: BudgetHardCap,
		TokenQuota: 1000000,
	})

	_, err := mp.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "will this work?"}},
		TenantID: "t_broke",
	})

	if err == nil {
		t.Fatal("expected budget exceeded error, got nil")
	}
	var budgetErr *BudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("expected BudgetError, got %T: %v", err, err)
	}
	if budgetErr.TenantID != "t_broke" {
		t.Errorf("tenant = %q, want t_broke", budgetErr.TenantID)
	}
}

func TestMeteredProvider_budgetOverage_allowsExcess(t *testing.T) {
	mock := &mockProvider{
		resp: &ChatResponse{
			Model: "deepseek-chat",
			Usage: Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
		},
	}
	meter := &mockMeter{
		spentTokens: 999000,
	}
	mp := NewMeteredProvider(mock, meter, MeterConfig{
		BudgetMode: BudgetOverage,
		TokenQuota: 1000000,
	})

	_, err := mp.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "over budget"}},
		TenantID: "t_rich",
	})
	if err != nil {
		t.Fatalf("overage mode should allow excess: %v", err)
	}
}

func TestMeteredProvider_budgetNone_noLimit(t *testing.T) {
	mock := &mockProvider{
		resp: &ChatResponse{
			Model: "deepseek-chat",
			Usage: Usage{PromptTokens: 500000, CompletionTokens: 500000, TotalTokens: 1000000},
		},
	}
	meter := &mockMeter{
		spentTokens: 999000,
	}
	mp := NewMeteredProvider(mock, meter, MeterConfig{
		BudgetMode: BudgetNone,
		TokenQuota: 1000000,
	})

	_, err := mp.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "enterprise"}},
		TenantID: "t_enterprise",
	})
	if err != nil {
		t.Fatalf("enterprise should have no limit: %v", err)
	}
}

func TestMeteredProvider_warnRatio(t *testing.T) {
	mock := &mockProvider{
		resp: &ChatResponse{
			Model: "deepseek-chat",
			Usage: Usage{PromptTokens: 10, CompletionTokens: 10, TotalTokens: 20},
		},
	}
	meter := &mockMeter{
		spentTokens: 810000, // 81% of 1M — above 80% warn
	}
	mp := NewMeteredProvider(mock, meter, MeterConfig{
		BudgetMode: BudgetHardCap,
		TokenQuota: 1000000,
		WarnRatio:  0.8,
	})

	resp, err := mp.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "hi"}},
		TenantID: "t_warn",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Warning {
		t.Error("expected warning flag when above warn ratio")
	}
}

// --- Mocks ---

type mockProvider struct {
	resp *ChatResponse
	err  error
}

func (m *mockProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.resp, nil
}

type mockMeter struct {
	records     []UsageEvent
	spentTokens int64
}

func (m *mockMeter) BudgetStatus(ctx context.Context, tenantID string) (BudgetStatus, error) {
	return BudgetStatus{
		SpentTokens: m.spentTokens,
		Status:      "ok",
	}, nil
}

func (m *mockMeter) Record(ctx context.Context, e UsageEvent) error {
	m.records = append(m.records, e)
	return nil
}
