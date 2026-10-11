package auth

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// WithCurrentAdministrator is the memory-mode equivalent of the PG actor row
// lock. The callback runs while password/status/role changes are excluded.
func (s *Service) WithCurrentAdministrator(ctx context.Context, p Principal, fn func() error) error {
	reader, ok := s.store.(interface {
		ReadAdministration(context.Context, func(*AdministrationState) error) error
	})
	if !ok {
		return pkgerrors.ErrServiceUnavailable
	}
	return reader.ReadAdministration(ctx, func(st *AdministrationState) error {
		u, ok := st.Users[p.UserID]
		if !ok || u.Status != "active" || u.TokenVersion != p.TokenVersion {
			return pkgerrors.ErrConflict
		}
		admin := false
		for _, r := range st.PlatformRoles[p.UserID] {
			if r == "platform_admin" {
				admin = true
			}
		}
		if !admin {
			return pkgerrors.ErrConflict
		}
		return fn()
	})
}
