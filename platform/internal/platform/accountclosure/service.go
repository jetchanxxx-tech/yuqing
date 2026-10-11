// Package accountclosure owns withdrawal and resumable account anonymization.
package accountclosure

import (
	"context"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type Actor struct {
	UserID, PasswordHash, RequestID string
	Version                         int64
}
type TenantImpact struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Members       int    `json:"members"`
	Role          string `json:"role"`
	Status        string `json:"status"`
	PlanCode      string `json:"plan_code"`
	RetentionDays *int   `json:"retention_days"`
}
type Preview struct {
	WithdrawalDays int            `json:"withdrawal_days"`
	Tenants        []TenantImpact `json:"tenants"`
	Blockers       []string       `json:"blockers"`
}
type Status struct {
	ID            string     `json:"id"`
	State         string     `json:"state"`
	RequestedAt   time.Time  `json:"requested_at"`
	WithdrawUntil time.Time  `json:"withdraw_until"`
	CompletedAt   *time.Time `json:"completed_at"`
	CleanupStatus string     `json:"cleanup_status"`
	LastError     string     `json:"error_code,omitempty"`
	AvatarDeleted int64      `json:"avatar_deleted"`
	Attempts      int64      `json:"attempts"`
}
type AvatarLifecycle = interface {
	Seal(context.Context, string) error
	Reconcile(context.Context, string, int64, int) (int64, bool, int, error)
}
type ExecutionStore interface {
	Process(context.Context, string, int) (*Status, error)
}
type Store interface {
	Preview(context.Context, string, int64) (*Preview, error)
	Request(context.Context, Actor, []string) (*Status, error)
	Cancel(context.Context, Actor) (*Status, error)
	Status(context.Context, string) (*Status, error)
}
type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }
func (s *Service) Preview(ctx context.Context, uid string, version int64) (*Preview, error) {
	if s == nil || s.store == nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	return s.store.Preview(ctx, uid, version)
}
func (s *Service) Request(ctx context.Context, actor Actor, tenants []string) (*Status, error) {
	if s == nil || s.store == nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	return s.store.Request(ctx, actor, tenants)
}
func (s *Service) Cancel(ctx context.Context, actor Actor) (*Status, error) {
	if s == nil || s.store == nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	return s.store.Cancel(ctx, actor)
}
func (s *Service) Status(ctx context.Context, uid string) (*Status, error) {
	if s == nil || s.store == nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	return s.store.Status(ctx, uid)
}
func (s *Service) Process(ctx context.Context, uid string, limit int) (*Status, error) {
	if s == nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	p, ok := s.store.(ExecutionStore)
	if !ok {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	return p.Process(ctx, uid, limit)
}
