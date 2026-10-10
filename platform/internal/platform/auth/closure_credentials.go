package auth

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// ClosureCredentialSnapshot is internal commit evidence, never an HTTP DTO.
// The closure store must compare this version/hash under its mutation lock.
func (s *Service) ClosureCredentialSnapshot(ctx context.Context, actor Principal, password string) (*User, error) {
	if s.userStore == nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	u, err := s.userStore.GetByID(ctx, actor.UserID)
	if err != nil {
		return nil, err
	}
	if u.TokenVersion != actor.TokenVersion || (u.Status != "active" && u.Status != "closure_pending") {
		return nil, pkgerrors.ErrConflict
	}
	if len(password) > 1024 || !VerifyPassword(u.PasswordHash, password) {
		return nil, pkgerrors.ErrUnauthorized
	}
	return u, nil
}
