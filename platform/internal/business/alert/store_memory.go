package alert

import (
	"context"
	"sync"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// MemoryStore is an in-memory Store implementation: map[tenantID]map[id]*Alert.
type MemoryStore struct {
	mu     sync.RWMutex
	alerts map[string]map[string]*Alert
	order  map[string][]string // tenantID → insertion-ordered alert IDs
}

// NewMemoryStore creates an empty in-memory alert store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		alerts: make(map[string]map[string]*Alert),
		order:  make(map[string][]string),
	}
}

var _ Store = (*MemoryStore)(nil)

// Create inserts an alert under a tenant; duplicate IDs conflict.
func (m *MemoryStore) Create(_ context.Context, tenantID string, a Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenant, ok := m.alerts[tenantID]
	if !ok {
		tenant = make(map[string]*Alert)
		m.alerts[tenantID] = tenant
	}
	if _, exists := tenant[a.ID]; exists {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "alert already exists")
	}
	cp := a
	tenant[a.ID] = &cp
	m.order[tenantID] = append(m.order[tenantID], a.ID)
	return nil
}

// Get returns a copy of one alert scoped to the tenant.
func (m *MemoryStore) Get(_ context.Context, tenantID, alertID string) (*Alert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.alerts[tenantID][alertID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "alert not found")
	}
	cp := *a
	return &cp, nil
}

// List returns copies of a tenant's alerts in creation order.
func (m *MemoryStore) List(_ context.Context, tenantID string) ([]Alert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Alert, 0)
	for _, id := range m.order[tenantID] {
		if a := m.alerts[tenantID][id]; a != nil {
			out = append(out, *a)
		}
	}
	return out, nil
}

// UpdateTrigger records the last trigger time of an alert.
func (m *MemoryStore) UpdateTrigger(_ context.Context, tenantID, alertID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.alerts[tenantID][alertID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "alert not found")
	}
	a.LastTriggeredAt = at
	return nil
}
