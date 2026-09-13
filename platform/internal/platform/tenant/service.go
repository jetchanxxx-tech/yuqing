package tenant

import (
	"context"
	"fmt"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// Tenant is a platform tenant row.
type Tenant struct {
	ID       string
	Name     string
	Slug     string
	DBName   string
	Status   Status
	PlanCode string
}

// Store persists tenant rows.
type Store interface {
	Create(ctx context.Context, t Tenant) error
	Get(ctx context.Context, id string) (*Tenant, error)
	List(ctx context.Context) ([]Tenant, error)
	UpdateStatus(ctx context.Context, id string, status Status) error
}

// Service implements tenant lifecycle operations (suspend/resume).
type Service struct {
	store Store
}

// NewService creates a tenant lifecycle service.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Get returns one tenant by ID.
func (s *Service) Get(ctx context.Context, id string) (*Tenant, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// List returns all tenants in stable creation order.
func (s *Service) List(ctx context.Context) ([]Tenant, error) {
	return s.store.List(ctx)
}

// Suspend moves an active tenant to the suspended status.
func (s *Service) Suspend(ctx context.Context, id string) error {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !CanTransition(string(t.Status), string(StatusSuspended)) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict,
			fmt.Sprintf("tenant cannot transition from %s to suspended", t.Status))
	}
	return s.store.UpdateStatus(ctx, id, StatusSuspended)
}

// Resume moves a suspended tenant back to active.
func (s *Service) Resume(ctx context.Context, id string) error {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !CanTransition(string(t.Status), string(StatusActive)) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict,
			fmt.Sprintf("tenant cannot transition from %s to active", t.Status))
	}
	return s.store.UpdateStatus(ctx, id, StatusActive)
}
