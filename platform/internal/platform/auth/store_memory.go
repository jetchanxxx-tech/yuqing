package auth

import (
	"context"
	"sync"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

// MemoryStore is an in-memory Store implementation for tests and the MVP.
// All lookups return copies so callers cannot mutate stored rows.
type MemoryStore struct {
	mu           sync.RWMutex
	usersByID    map[string]*User
	usersByEmail map[string]string // email → userID
	tenantsByID  map[string]*Tenant
	tenantOfUser map[string]string // userID → tenantID
	roleByMember map[memberKey]string
}

type memberKey struct {
	tenantID string
	userID   string
}

// NewMemoryStore creates an empty in-memory auth store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		usersByID:    make(map[string]*User),
		usersByEmail: make(map[string]string),
		tenantsByID:  make(map[string]*Tenant),
		tenantOfUser: make(map[string]string),
		roleByMember: make(map[memberKey]string),
	}
}

var _ Store = (*MemoryStore)(nil)

// CreateUser inserts a user; duplicate emails conflict.
func (m *MemoryStore) CreateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.usersByEmail[u.Email]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "email already registered")
	}
	cp := u
	m.usersByID[u.ID] = &cp
	m.usersByEmail[u.Email] = u.ID
	return nil
}

// GetUserByEmail finds a user by normalized email.
func (m *MemoryStore) GetUserByEmail(_ context.Context, email string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	userID, ok := m.usersByEmail[email]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	u := m.usersByID[userID]
	cp := *u
	return &cp, nil
}

// CreateTenant inserts a tenant row.
func (m *MemoryStore) CreateTenant(_ context.Context, t Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tenantsByID[t.ID]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "tenant already exists")
	}
	cp := t
	m.tenantsByID[t.ID] = &cp
	return nil
}

// CreateMember binds a user to a tenant with a role.
func (m *MemoryStore) CreateMember(_ context.Context, mem Member) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := memberKey{tenantID: mem.TenantID, userID: mem.UserID}
	if _, ok := m.roleByMember[key]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "membership already exists")
	}
	m.roleByMember[key] = mem.Role
	m.tenantOfUser[mem.UserID] = mem.TenantID
	return nil
}

// GetUserTenant returns the tenant the user belongs to.
func (m *MemoryStore) GetUserTenant(_ context.Context, userID string) (*Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenantID, ok := m.tenantOfUser[userID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "membership not found")
	}
	t := m.tenantsByID[tenantID]
	if t == nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "tenant not found")
	}
	cp := *t
	return &cp, nil
}

// GetUserRole returns the user's role inside a tenant.
func (m *MemoryStore) GetUserRole(_ context.Context, tenantID, userID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	role, ok := m.roleByMember[memberKey{tenantID: tenantID, userID: userID}]
	if !ok {
		return "", pkgerrors.Wrap(pkgerrors.ErrNotFound, "membership not found")
	}
	return role, nil
}
