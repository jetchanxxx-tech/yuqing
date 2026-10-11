package credit

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// The caller holds the original administrator/account/tenant boundary; this
// primitive commits only a memory pool CAS and never creates a financial ledger.
func (s *Service) ChangeMemoryPlan(_ context.Context, tenantID, code string, expected int64) (*Snapshot, *Snapshot, error) {
	m, ok := s.store.(*MemoryStore)
	if !ok {
		return nil, nil, pkgerrors.ErrServiceUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.versions[tenantID] != expected {
		return nil, nil, pkgerrors.ErrConflict
	}
	plan, exists := m.plans[tenantID]
	if !exists {
		plan = "free"
	}
	source := m.planSources[tenantID]
	if source == "" {
		source = "report_credits"
	}
	before := &Snapshot{Balance: m.balances[tenantID], PlanCode: plan, PlanSource: source, Version: expected}
	m.plans[tenantID], m.planSources[tenantID] = code, "manual"
	m.versions[tenantID]++
	after := &Snapshot{Balance: before.Balance, PlanCode: code, PlanSource: "manual", Version: m.versions[tenantID]}
	return before, after, nil
}
