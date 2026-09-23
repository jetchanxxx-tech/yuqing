package report

import (
	"context"
	"sync"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// MemoryStore is an in-memory Store implementation: map[tenantID]map[id]Report.
type MemoryStore struct {
	mu       sync.RWMutex
	byTenant map[string]map[string]*Report
	order    map[string][]string // tenantID → insertion-ordered report IDs
}

// NewMemoryStore creates an empty in-memory report store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byTenant: make(map[string]map[string]*Report),
		order:    make(map[string][]string),
	}
}

var _ Store = (*MemoryStore)(nil)

// Create inserts a report under a tenant; duplicate IDs conflict.
func (m *MemoryStore) Create(_ context.Context, tenantID, createdBy string, r Report) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.byTenant[tenantID]
	if !ok {
		tenant = make(map[string]*Report)
		m.byTenant[tenantID] = tenant
	}
	if _, exists := tenant[r.ID]; exists {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "report already exists")
	}
	cp := r
	tenant[r.ID] = &cp
	m.order[tenantID] = append(m.order[tenantID], r.ID)
	return nil
}

// Get returns a copy of one report scoped to the tenant.
func (m *MemoryStore) Get(_ context.Context, tenantID, reportID string) (*Report, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.byTenant[tenantID][reportID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "report not found")
	}
	cp := *r
	return &cp, nil
}

// List returns copies of a tenant's reports in creation order, applying f.
func (m *MemoryStore) List(_ context.Context, tenantID string, f Filter) ([]Report, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Report, 0)
	for _, id := range m.order[tenantID] {
		r := m.byTenant[tenantID][id]
		if r == nil {
			continue
		}
		if f.AnalysisID != "" && r.AnalysisID != f.AnalysisID {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		out = append(out, *r)
	}

	// Apply offset/limit after filtering.
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return []Report{}, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// UpdateStatus changes a report's lifecycle status.
func (m *MemoryStore) UpdateStatus(_ context.Context, tenantID, reportID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.byTenant[tenantID][reportID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "report not found")
	}
	r.Status = status
	return nil
}
