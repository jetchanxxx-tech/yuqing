package auth

import (
	"context"
	"net"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

const passwordConfirmationLimit = 20
const passwordConfirmationWindow = time.Minute

// The three password-confirmation purposes share a process-wide ceiling. A
// request never waits for capacity, and cancellation cannot release the slot
// until its synchronous, non-interruptible Argon2 call has actually returned.
var passwordConfirmationSlots = make(chan struct{}, 2)

// PasswordConfirmationStore reserves a source budget before inspecting a
// credential. PreflightPassword validates without consuming valid credentials;
// wrong SMS codes spend one durable attempt. Consume remains authoritative.
// Custom stores must implement this port; password purposes have no fallback.
type PasswordConfirmationStore interface {
	ReservePasswordConfirmation(context.Context, string) error
	PreflightPassword(context.Context, VerificationAttempt) error
}

func passwordConfirmationPurpose(purpose string) bool {
	return purpose == PasswordReset || purpose == PhoneReset || purpose == SetPassword
}

type passwordConfirmationLimited struct{ after int }

func (e passwordConfirmationLimited) Error() string {
	return "password confirmation temporarily limited"
}
func (e passwordConfirmationLimited) Unwrap() error          { return pkgerrors.ErrQuotaExceeded }
func (e passwordConfirmationLimited) RetryAfterSeconds() int { return e.after }
func confirmationRetryAfter(d time.Duration) int {
	n := int((d + time.Second - 1) / time.Second)
	if n < 1 {
		return 1
	}
	if n > 60 {
		return 60
	}
	return n
}

func (s *Service) beginPasswordConfirmation(ctx context.Context, a VerificationAttempt, sourceIP []string) (func(), error) {
	if len(sourceIP) != 1 {
		return nil, pkgerrors.Wrap(pkgerrors.ErrBadRequest, "trusted confirmation source required")
	}
	ip := net.ParseIP(sourceIP[0])
	if ip == nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrBadRequest, "trusted confirmation source required")
	}
	if ctx.Err() != nil {
		return nil, pkgerrors.ErrServiceUnavailable
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
	release := func() { <-passwordConfirmationSlots }
	// Reserve/preflight may themselves fail or panic; leave no occupied slot.
	admitted := false
	defer func() {
		if !admitted {
			release()
		}
	}()
	if err := store.ReservePasswordConfirmation(ctx, ip.String()); err != nil {
		return nil, err
	}
	if err := store.PreflightPassword(ctx, a); err != nil {
		if a.Purpose == PhoneReset && pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			return nil, verificationInvalid()
		}
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	admitted = true
	return release, nil
}

func (m *MemoryVerificationStore) ReservePasswordConfirmation(ctx context.Context, ip string) error {
	if ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	key := "confirm:ip:" + ip
	v := m.gates[key]
	if !now.Before(v.Start.Add(passwordConfirmationWindow)) {
		v.Start = now
		v.Sends = 0
	}
	if v.Sends >= passwordConfirmationLimit {
		return passwordConfirmationLimited{after: confirmationRetryAfter(v.Start.Add(passwordConfirmationWindow).Sub(now))}
	}
	v.Sends++
	v.Last = now
	m.gates[key] = v
	return nil
}
func (m *MemoryVerificationStore) PreflightPassword(ctx context.Context, a VerificationAttempt) error {
	if !passwordConfirmationPurpose(a.Purpose) {
		return pkgerrors.ErrConflict
	}
	if ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	_, err := m.consumeVerification(ctx, a, true)
	return err
}
