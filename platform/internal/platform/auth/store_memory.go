package auth

import (
	"context"
	"sync"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// MemoryStore is an in-memory Store implementation for tests and the MVP.
// All lookups return copies so callers cannot mutate stored rows.
type MemoryStore struct {
	mu                  sync.RWMutex
	usersByID           map[string]*User
	usersByEmail        map[string]string // email → userID
	tenantsByID         map[string]*Tenant
	tenantOfUser        map[string]string // userID → tenantID
	roleByMember        map[memberKey]string
	platformRolesByUser map[string][]string
}

type memberKey struct {
	tenantID string
	userID   string
}

// NewMemoryStore creates an empty in-memory auth store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		usersByID:           make(map[string]*User),
		usersByEmail:        make(map[string]string),
		tenantsByID:         make(map[string]*Tenant),
		tenantOfUser:        make(map[string]string),
		roleByMember:        make(map[memberKey]string),
		platformRolesByUser: make(map[string][]string),
	}
}

var _ Store = (*MemoryStore)(nil)
var _ RegistrationStore = (*MemoryStore)(nil)
var _ AuthorizationStateStore = (*MemoryStore)(nil)
var _ LoginRecorder = (*MemoryStore)(nil)

func accountUserDefaults(u User) User {
	if u.Status == "" {
		u.Status = "active"
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	return u
}

func validateRegistration(user User, tenant Tenant, member Member) error {
	if user.ID == "" || tenant.ID == "" || member.UserID != user.ID || member.TenantID != tenant.ID ||
		user.TokenVersion < 0 || user.RowVersion < 0 {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "invalid registration rows")
	}
	return nil
}

func (m *MemoryStore) checkUserMemberLocked(user User, member Member) error {
	if _, ok := m.usersByID[user.ID]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "user already exists")
	}
	if _, ok := m.usersByEmail[user.Email]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "email already registered")
	}
	if _, ok := m.roleByMember[memberKey{tenantID: member.TenantID, userID: member.UserID}]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "membership already exists")
	}
	return nil
}

// commitAccountLocked is called only after every fallible registration step.
func (m *MemoryStore) commitAccountLocked(user User, member Member, bootstrap bool) {
	u := accountUserDefaults(user)
	m.usersByID[u.ID] = &u
	m.usersByEmail[u.Email] = u.ID
	m.roleByMember[memberKey{tenantID: member.TenantID, userID: member.UserID}] = member.Role
	m.tenantOfUser[u.ID] = member.TenantID
	if bootstrap {
		hasRoles := false
		for _, roles := range m.platformRolesByUser {
			if len(roles) > 0 {
				hasRoles = true
				break
			}
		}
		if !hasRoles {
			m.platformRolesByUser[u.ID] = []string{rolePlatformAdmin}
		}
	}
}

func (m *MemoryStore) RegisterAccount(_ context.Context, user User, tenant Tenant, member Member, bootstrap bool) error {
	if err := validateRegistration(user, tenant, member); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkUserMemberLocked(user, member); err != nil {
		return err
	}
	for _, existing := range m.tenantsByID {
		if existing.ID == tenant.ID || existing.Slug == tenant.Slug || existing.DBName == tenant.DBName {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "tenant already exists")
		}
	}
	cp := tenant
	m.tenantsByID[tenant.ID] = &cp
	m.commitAccountLocked(user, member, bootstrap)
	return nil
}

func (m *MemoryStore) authorizationStateLocked(userID, tenantID string) (*AuthorizationState, error) {
	u, ok := m.usersByID[userID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	role, member := m.roleByMember[memberKey{tenantID: tenantID, userID: userID}]
	return &AuthorizationState{
		UserID: u.ID, Email: u.Email, UserStatus: u.Status, TokenVersion: u.TokenVersion,
		PlatformRoles: append([]string(nil), m.platformRolesByUser[userID]...),
		TenantID:      tenantID, MemberRole: role, MemberExists: member,
	}, nil
}

func (m *MemoryStore) LoadAuthorizationState(_ context.Context, userID, tenantID string) (*AuthorizationState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, err := m.authorizationStateLocked(userID, tenantID)
	if err != nil {
		return nil, err
	}
	if tenant := m.tenantsByID[tenantID]; tenant != nil {
		state.TenantStatus, state.PlanCode = tenant.Status, tenant.PlanCode
	} else {
		state.MemberExists = false
	}
	return state, nil
}

func (m *MemoryStore) RecordSuccessfulLogin(_ context.Context, userID string, version int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.usersByID[userID]
	if u == nil || u.Status != "active" || u.TokenVersion != version {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "account or credential revoked")
	}
	now := time.Now()
	u.LastLoginAt = &now
	return nil
}

// CreateUser inserts a user; duplicate emails conflict.
func (m *MemoryStore) CreateUser(_ context.Context, u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.usersByEmail[u.Email]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "email already registered")
	}
	if _, ok := m.usersByID[u.ID]; ok {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "user already exists")
	}
	cp := accountUserDefaults(u)
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
