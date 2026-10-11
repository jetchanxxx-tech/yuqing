package auth

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"net"
)

// ClosureCredentialSnapshot is internal commit evidence, never an HTTP DTO.
// The closure store must compare this version/hash under its mutation lock.
func (s *Service) ClosureCredentialSnapshot(ctx context.Context, actor Principal, password, sourceIP string) (*User, error) {
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
	if len(password) == 0 || len(password) > 1024 {
		return nil, pkgerrors.ErrUnauthorized
	}
	ip := net.ParseIP(sourceIP)
	if ip == nil {
		return nil, pkgerrors.ErrBadRequest
	}
	store, ok := s.verifications.(PasswordConfirmationStore)
	if !ok {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	select {
	case passwordConfirmationSlots <- struct{}{}:
	default:
		return nil, passwordConfirmationLimited{after: 1}
	}
	defer func() { <-passwordConfirmationSlots }()
	if err = store.ReservePasswordConfirmation(ctx, ip.String()); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return nil, pkgerrors.ErrUnauthorized
	}
	return u, nil
}
