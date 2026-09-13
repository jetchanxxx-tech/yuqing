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

// CreateTenant stores the tenant row in the shared tenant store.
func (s *SharedTenantStore) CreateTenant(_ context.Context, t Tenant) error {
	return s.tenants.Create(context.Background(), tenant.Tenant{
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
