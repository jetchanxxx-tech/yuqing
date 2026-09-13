package apikey

import (
	"context"
	"sort"
	"sync"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// MemoryStore is the in-memory API key store for MVP (restart = reset).
// It mirrors the platform api_keys table: keyed by id, secondary index on
// key_hash for authentication lookups. All reads return copies.
type MemoryStore struct {
	mu     sync.RWMutex
	byID   map[string]*APIKey
	byHash map[string]string // keyHash → keyID
}

// NewMemoryStore creates an empty in-memory API key store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:   make(map[string]*APIKey),
		byHash: make(map[string]string),
	}
}

func (m *MemoryStore) Create(_ context.Context, k *APIKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.byID[k.ID]; exists {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "api key already exists")
	}
	cp := *k
	m.byID[k.ID] = &cp
	m.byHash[k.keyHash] = k.ID
	return nil
}

func (m *MemoryStore) List(_ context.Context, tenantID string) ([]*APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*APIKey, 0)
	for _, k := range m.byID {
		if k.TenantID == tenantID {
			cp := *k
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Revoke stamps revoked_at, scoped to the tenant: foreign or unknown ids are
// indistinguishable ErrNotFound (no cross-tenant existence leak).
func (m *MemoryStore) Revoke(_ context.Context, tenantID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.byID[keyID]
	if !ok || k.TenantID != tenantID {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	if k.RevokedAt == nil {
		now := time.Now().UTC()
		k.RevokedAt = &now
	}
	return nil
}

func (m *MemoryStore) GetByHash(_ context.Context, hash string) (*APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keyID, ok := m.byHash[hash]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	k, ok := m.byID[keyID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	cp := *k
	return &cp, nil
}

func (m *MemoryStore) Touch(_ context.Context, keyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.byID[keyID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	ts := at
	k.LastUsedAt = &ts
	return nil
}

var _ Store = (*MemoryStore)(nil)
