package auth

import (
	"context"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// SharedTenantStore implements auth.Store so that tenant rows created during
// Register live in an external tenant.MemoryStore instead of a private copy.
// This is the memory-mode equivalent of the single platform "tenants" table:
// tenant.Service (admin list/suspend/resume) and the auth flow observe the
// same rows.
type SharedTenantStore struct {
	*MemoryStore // users, memberships, tenantOfUser lookups
	tenants      *tenant.MemoryStore
}

// NewSharedTenantStore wraps a base auth MemoryStore and a tenant store that
// both the auth flow and tenant.Service share.
func NewSharedTenantStore(base *MemoryStore, tenants *tenant.MemoryStore) *SharedTenantStore {
	return &SharedTenantStore{MemoryStore: base, tenants: tenants}
}

var _ Store = (*SharedTenantStore)(nil)
var _ RegistrationStore = (*SharedTenantStore)(nil)
var _ AuthorizationStateStore = (*SharedTenantStore)(nil)

// RegisterAccount validates all local rows before creating the shared tenant.
// Once Create succeeds, the remaining writes cannot fail and are committed
// while holding the same account lock used by all identity readers.
func (s *SharedTenantStore) RegisterAccount(ctx context.Context, user User, t Tenant, member Member, bootstrap bool) error {
	if err := validateRegistration(user, t, member); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkUserMemberLocked(user, member); err != nil {
		return err
	}
	if err := s.CreateTenant(ctx, t); err != nil {
		return err
	}
	s.commitAccountLocked(user, member, bootstrap)
	return nil
}

// LoadAuthorizationState must resolve the explicit token tenant through the
// shared store. The embedded MemoryStore has no private copy of tenant rows.
func (s *SharedTenantStore) LoadAuthorizationState(ctx context.Context, userID, tenantID string) (*AuthorizationState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, err := s.authorizationStateLocked(userID, tenantID)
	if err != nil || tenantID == "" {
		return state, err
	}
	t, err := s.tenants.Get(ctx, tenantID)
	if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		state.MemberExists = false
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if t == nil {
		state.MemberExists = false
		return state, nil
	}
	state.TenantStatus, state.PlanCode = string(t.Status), t.PlanCode
	return state, nil
}

// CreateTenant stores the tenant row in the shared tenant store.
func (s *SharedTenantStore) CreateTenant(ctx context.Context, t Tenant) error {
	return s.tenants.Create(ctx, tenant.Tenant{
		ID:       t.ID,
		Name:     t.Name,
		Slug:     t.Slug,
		DBName:   t.DBName,
		Status:   tenant.Status(t.Status),
		PlanCode: t.PlanCode,
	})
}

// GetUserTenant resolves the tenant of a user through the shared store, so a
// later suspend/resume by tenant.Service is reflected here.
func (s *SharedTenantStore) GetUserTenant(ctx context.Context, userID string) (*Tenant, error) {
	s.mu.RLock()
	tenantID, ok := s.tenantOfUser[userID]
	s.mu.RUnlock()
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "membership not found")
	}

	t, err := s.tenants.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "tenant not found")
	}
	return &Tenant{
		ID:       t.ID,
		Name:     t.Name,
		Slug:     t.Slug,
		DBName:   t.DBName,
		Status:   string(t.Status),
		PlanCode: t.PlanCode,
	}, nil
}

// RegisterAdminAccount holds the identity boundary across validation, trial
// preparation and the infallible memory commit. It never seeds platform roles.
func (s *SharedTenantStore) RegisterAdminAccount(ctx context.Context, actor Principal, user User, t Tenant, member Member, trial func() error) error {
	if err := validateRegistration(user, t, member); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	current := s.usersByID[actor.UserID]
	admin := false
	for _, role := range s.platformRolesByUser[actor.UserID] {
		if role == rolePlatformAdmin {
			admin = true
		}
	}
	if current == nil || current.Status != "active" || current.TokenVersion != actor.TokenVersion || !admin {
		return pkgerrors.ErrConflict
	}
	if err := s.checkUserMemberLocked(user, member); err != nil {
		return err
	}
	return s.tenants.CreateWithProvision(ctx, tenant.Tenant{ID: t.ID, Name: t.Name, Slug: t.Slug, DBName: t.DBName, Status: tenant.Status(t.Status), PlanCode: t.PlanCode}, func() error {
		if err := trial(); err != nil {
			return err
		}
		s.commitAccountLocked(user, member, false)
		return nil
	})
}
