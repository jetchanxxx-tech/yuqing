package analysis

import (
	"context"
	"sort"
	"sync"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

// memoryStore keeps analyses in memory: map[tenantID]map[analysisID]*AnalysisResult.
// All reads return copies so callers cannot mutate stored state.
type memoryStore struct {
	mu       sync.RWMutex
	byTenant map[string]map[string]*AnalysisResult
}

func newMemoryStore() *memoryStore {
	return &memoryStore{byTenant: make(map[string]map[string]*AnalysisResult)}
}

// put stores a new analysis under its tenant; duplicate IDs conflict.
func (m *memoryStore) put(_ context.Context, tenantID string, a *AnalysisResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.byTenant[tenantID]
	if !ok {
		tenant = make(map[string]*AnalysisResult)
		m.byTenant[tenantID] = tenant
	}
	if _, exists := tenant[a.ID]; exists {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "analysis already exists")
	}
	cp := *a
	tenant[a.ID] = &cp
	return nil
}

// get returns a copy of one analysis scoped to the tenant.
func (m *memoryStore) get(_ context.Context, tenantID, analysisID string) (*AnalysisResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenant, ok := m.byTenant[tenantID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "analysis not found")
	}
	a, ok := tenant[analysisID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "analysis not found")
	}
	cp := *a
	return &cp, nil
}

// list returns copies of all analyses of a tenant, ordered by CreatedAt then ID.
func (m *memoryStore) list(_ context.Context, tenantID string) ([]AnalysisResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenant, ok := m.byTenant[tenantID]
	if !ok {
		return []AnalysisResult{}, nil
	}
	out := make([]AnalysisResult, 0, len(tenant))
	for _, a := range tenant {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// mutate applies fn to the stored analysis under the write lock.
func (m *memoryStore) mutate(_ context.Context, tenantID, analysisID string, fn func(*AnalysisResult) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.byTenant[tenantID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "analysis not found")
	}
	a, ok := tenant[analysisID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "analysis not found")
	}
	return fn(a)
}
