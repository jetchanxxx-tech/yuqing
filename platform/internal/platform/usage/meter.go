// Package usage provides LLM token metering with Redis-backed counters.
package usage

import (
	"context"
	"fmt"
	"sync"

	"github.com/yuging/platform/internal/pkg/llm"
)

// Meter implements llm.Meter with in-memory counters (Redis-backed in production).
type Meter struct {
	mu     sync.RWMutex
	counts map[string]*tenantCounters // keyed by tenantID
}

type tenantCounters struct {
	spentTokens int64
	quotaTokens int64
	budgetMode  llm.BudgetMode
}

// NewMeter creates an in-memory usage meter (MVP).
// Production uses Redis counters for persistence across restarts.
func NewMeter() *Meter {
	return &Meter{counts: make(map[string]*tenantCounters)}
}

// SetQuota configures a tenant's token budget.
func (m *Meter) SetQuota(tenantID string, quota int64, mode llm.BudgetMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.counts[tenantID]; ok {
		c.quotaTokens = quota
		c.budgetMode = mode
	} else {
		m.counts[tenantID] = &tenantCounters{quotaTokens: quota, budgetMode: mode}
	}
}

// BudgetStatus returns current token usage vs quota.
func (m *Meter) BudgetStatus(ctx context.Context, tenantID string) (llm.BudgetStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.counts[tenantID]
	if !ok {
		return llm.BudgetStatus{Status: "ok", SpentTokens: 0, QuotaTokens: 0}, nil
	}

	status := "ok"
	ratio := float64(0)
	if c.quotaTokens > 0 {
		ratio = float64(c.spentTokens) / float64(c.quotaTokens)
	}
	switch {
	case ratio >= 1.0:
		status = "exceeded"
	case ratio >= 0.8:
		status = "warn"
	}

	return llm.BudgetStatus{
		SpentTokens: c.spentTokens,
		QuotaTokens: c.quotaTokens,
		Status:      status,
	}, nil
}

// Record records a usage event, incrementing the tenant's token counter.
func (m *Meter) Record(ctx context.Context, e llm.UsageEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.counts[e.TenantID]
	if !ok {
		c = &tenantCounters{}
		m.counts[e.TenantID] = c
	}

	c.spentTokens += int64(e.PromptTokens + e.CompletionTokens + e.CacheTokens)

	// TODO: publish to queue topic usage.events for async rollup.
	_ = e.AnalysisID
	return nil
}

var _ llm.Meter = (*Meter)(nil)

// Ensure valid at compile time.
var _ = fmt.Sprintf
