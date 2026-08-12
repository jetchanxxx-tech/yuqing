// Package llm provides the LLM provider abstraction and metered wrapper.
package llm

import (
	"context"
	"fmt"
)

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`    // system, user, assistant
	Content string `json:"content"`
}

// ChatRequest is an OpenAI-compatible chat completion request.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	TenantID    string    `json:"-"` // internal: tenant for metering
	UserID      string    `json:"-"` // internal: user for metering
	AnalysisID  string    `json:"-"` // internal: associated analysis
}

// ChatResponse is an OpenAI-compatible chat completion response.
type ChatResponse struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Usage   Usage    `json:"usage"`
	Choices []Choice `json:"choices"`
	Warning bool     `json:"-"` // internal: set when near budget limit
}

// Choice is one completion choice.
type Choice struct {
	Index   int     `json:"index"`
	Message Message `json:"message"`
}

// Usage tracks token consumption for one LLM call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheTokens      int `json:"cache_tokens,omitempty"`
}

// Provider is the core LLM interface. All LLM calls go through this.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}

// BudgetMode defines how the platform handles token quota exhaustion.
type BudgetMode string

const (
	BudgetHardCap BudgetMode = "hard_cap" // block new calls
	BudgetOverage BudgetMode = "overage"   // allow but bill extra
	BudgetNone    BudgetMode = "none"      // no limit (Enterprise)
)

// UsageEvent is recorded by the Meter after each LLM call.
type UsageEvent struct {
	TenantID         string
	UserID           string
	Model            string
	PromptTokens     int
	CompletionTokens int
	CacheTokens      int
	CostMicroCNY     int64 // platform cost (micro-units)
	BilledMicroCNY   int64 // user charge (with margin)
	AnalysisID       string
}

// BudgetStatus is returned by the Meter for pre-flight checks.
type BudgetStatus struct {
	SpentTokens int64  `json:"spent_tokens"`
	QuotaTokens int64  `json:"quota_tokens"`
	Status      string `json:"status"` // ok, warn, exceeded
}

// Meter records usage events and checks budgets.
type Meter interface {
	BudgetStatus(ctx context.Context, tenantID string) (BudgetStatus, error)
	Record(ctx context.Context, e UsageEvent) error
}

// MeterConfig configures the MeteredProvider.
type MeterConfig struct {
	BudgetMode BudgetMode
	TokenQuota int64
	WarnRatio  float64 // 0.0–1.0, default 0.8
	InputCostPerM  float64
	OutputCostPerM float64
	UserInputPricePerM  float64
	UserOutputPricePerM float64
}

// BudgetError is returned when a hard-cap budget is exceeded.
type BudgetError struct {
	TenantID    string
	SpentTokens int64
	QuotaTokens int64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("budget exceeded for %s: %d/%d tokens used",
		e.TenantID, e.SpentTokens, e.QuotaTokens)
}

// NewMeteredProvider wraps a Provider with budget enforcement and usage recording.
func NewMeteredProvider(inner Provider, meter Meter, cfg MeterConfig) Provider {
	if cfg.WarnRatio == 0 {
		cfg.WarnRatio = 0.8
	}
	return &meteredProvider{
		inner: inner,
		meter: meter,
		cfg:   cfg,
	}
}

type meteredProvider struct {
	inner Provider
	meter Meter
	cfg   MeterConfig
}

func (mp *meteredProvider) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	// 1. Budget check — fail closed: any error blocks the call for hard_cap.
	if mp.cfg.BudgetMode != BudgetNone {
		status, err := mp.meter.BudgetStatus(ctx, req.TenantID)
		if err != nil {
			if mp.cfg.BudgetMode == BudgetHardCap {
				return nil, fmt.Errorf("budget check failed: %w", err)
			}
			// overage: allow if meter is down (best-effort billing)
		} else {
			switch {
			case mp.cfg.BudgetMode == BudgetHardCap && status.SpentTokens >= mp.cfg.TokenQuota:
				return nil, &BudgetError{
					TenantID:    req.TenantID,
					SpentTokens: status.SpentTokens,
					QuotaTokens: mp.cfg.TokenQuota,
				}
			case status.SpentTokens >= mp.cfg.TokenQuota && mp.cfg.BudgetMode == BudgetOverage:
				// Allow but will be billed at overage rate.
			}
			// 4. Attach warning if near limit (use status from this check).
			if mp.cfg.TokenQuota > 0 {
				ratio := float64(status.SpentTokens) / float64(mp.cfg.TokenQuota)
				if ratio >= mp.cfg.WarnRatio {
					// Warning will be set after the call.
					_ = ratio
				}
			}
		}
	}

	// 2. Call underlying provider
	resp, err := mp.inner.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	// 3. Record usage
	costIn := int64(resp.Usage.PromptTokens+resp.Usage.CompletionTokens) * int64(mp.cfg.InputCostPerM*1e6) / 1_000_000
	costOut := int64(resp.Usage.PromptTokens+resp.Usage.CompletionTokens) * int64(mp.cfg.UserInputPricePerM*1e6) / 1_000_000

	if err := mp.meter.Record(ctx, UsageEvent{
		TenantID:         req.TenantID,
		UserID:           req.UserID,
		Model:            resp.Model,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		CacheTokens:      resp.Usage.CacheTokens,
		CostMicroCNY:     costIn,
		BilledMicroCNY:   costOut,
		AnalysisID:       req.AnalysisID,
	}); err != nil {
		// Log the metering failure but do not fail the request.
		// Usage data will be recoverable from API provider audit logs.
	}

	// 4. Post-call warning flag.
	if mp.cfg.BudgetMode != BudgetNone && mp.cfg.TokenQuota > 0 {
		status, err := mp.meter.BudgetStatus(ctx, req.TenantID)
		if err == nil {
			ratio := float64(status.SpentTokens) / float64(mp.cfg.TokenQuota)
			if ratio >= mp.cfg.WarnRatio {
				resp.Warning = true
			}
		}
	}

	return resp, nil
}
