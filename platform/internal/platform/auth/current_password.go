package auth

import (
	"context"
	"net"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// Issuance shares the reset/activation/closure KDF slots and source budget.
// The immutable account budget prevents target or socket rotation from granting
// one account unlimited password guesses. Stores reserve both counters atomically.
type currentPasswordStore interface {
	ReserveCurrentPassword(context.Context, string, string) error
}

func (s *Service) verifyCurrentPassword(ctx context.Context, u *User, password, sourceIP string) error {
	if password == "" {
		return pkgerrors.ErrUnauthorized
	}
	if len(password) > 1024 {
		return pkgerrors.ErrBadRequest
	}
	ip := net.ParseIP(sourceIP)
	if ip == nil {
		return pkgerrors.ErrBadRequest
	}
	if ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	store, ok := s.verifications.(currentPasswordStore)
	if !ok {
		return pkgerrors.ErrServiceUnavailable
	}
	select {
	case passwordConfirmationSlots <- struct{}{}:
	default:
		return passwordConfirmationLimited{after: 1}
	}
	// Cancellation cannot release a synchronous Argon2 call still executing.
	// No user/database lock survives admission into hashing or supplier dispatch.
	defer func() { <-passwordConfirmationSlots }()
	if err := store.ReserveCurrentPassword(ctx, u.ID, ip.String()); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return pkgerrors.ErrUnauthorized
	}
	if ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	return nil
}

func (m *MemoryVerificationStore) ReserveCurrentPassword(ctx context.Context, uid, ip string) error {
	if ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	pending := map[string]verificationGate{}
	for _, key := range []string{"confirm:ip:" + ip, "confirm:uid:" + uid} {
		gate := m.gates[key]
		if !now.Before(gate.Start.Add(passwordConfirmationWindow)) {
			gate.Start = now
			gate.Sends = 0
		}
		if gate.Sends >= passwordConfirmationLimit {
			return passwordConfirmationLimited{after: confirmationRetryAfter(gate.Start.Add(passwordConfirmationWindow).Sub(now))}
		}
		gate.Sends++
		gate.Last = now
		pending[key] = gate
	}
	for key, gate := range pending {
		m.gates[key] = gate
	}
	return nil
}
