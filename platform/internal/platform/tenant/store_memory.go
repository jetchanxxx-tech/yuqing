package tenant

import (
	"context"
	"sync"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

// MemoryStore is an in-memory Store implementation for tests and the MVP.
type MemoryStore struct {
	mu    sync.RWMutex
	byID  map[string]*Tenant
	order []string // insertion order for stable List output
}

// NewMemoryStore creates an empty in-memory tenant store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: make(map[string]*Tenant)}
}

var _ Store = (*MemoryStore)(nil)

// Create inserts a tenant; duplicate IDs conflict.
func (m *MemoryStore) Create(_ context.Context, t Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.byID[t.ID]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "tenant already exists")
	}
	cp := t
	m.byID[t.ID] = &cp
	m.order = append(m.order, t.ID)
	return nil
}

// Get returns a copy of one tenant.
func (m *MemoryStore) Get(_ context.Context, id string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, ok := m.byID[id]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "tenant not found")
	}
	cp := *t
	return &cp, nil
}

// List returns copies of all tenants in creation order.
func (m *MemoryStore) List(_ context.Context) ([]Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Tenant, 0, len(m.order))
	for _, id := range m.order {
		if t, ok := m.byID[id]; ok {
			out = append(out, *t)
		}
	}
	return out, nil
}

// UpdateStatus changes a tenant's lifecycle status.
func (m *MemoryStore) UpdateStatus(_ context.Context, id string, status Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.byID[id]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "tenant not found")
	}
	t.Status = status
	return nil
}
