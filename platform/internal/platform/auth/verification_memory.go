package auth

import (
	"context"
	"crypto/subtle"
	"strings"
	"sync"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type verificationGate struct {
	Start, Last time.Time
	Sends       int
}
type MemoryVerificationStore struct {
	mu          sync.Mutex
	users       *MemoryStore
	credentials map[string]VerificationCredential
	gates       map[string]verificationGate
}

func (m *MemoryStore) verificationUsers() *MemoryStore { return m }
func NewMemoryVerificationStore() *MemoryVerificationStore {
	return &MemoryVerificationStore{credentials: map[string]VerificationCredential{}, gates: map[string]verificationGate{}}
}
func (m *MemoryVerificationStore) ReserveSend(_ context.Context, purpose, target, ip string, limits VerificationLimits) error {
	if _, err := verificationTarget(purpose, target); err != nil {
		return err
	}
	limits = limits.defaults()
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	keys := []string{"target:" + purpose + ":" + target}
	if ip != "" {
		keys = append(keys, "ip:"+ip)
	}
	pending := map[string]verificationGate{}
	for i, key := range keys {
		v := m.gates[key]
		if !now.Before(v.Start.Add(limits.Window)) {
			v.Start = now
			v.Sends = 0
		}
		maximum := limits.TargetLimit
		if i > 0 {
			maximum = limits.IPLimit
		}
		if v.Sends >= maximum || (i == 0 && !v.Last.IsZero() && now.Before(v.Last.Add(limits.Interval))) {
			return pkgerrors.ErrQuotaExceeded
		}
		v.Last = now
		v.Sends++
		pending[key] = v
	}
	for key, v := range pending {
		m.gates[key] = v
	}
	return nil
}
func (m *MemoryVerificationStore) Issue(_ context.Context, c VerificationCredential) error {
	if err := validateVerificationCredential(c); err != nil {
		return err
	}
	if m.users == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	m.users.mu.Lock()
	defer m.users.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	u, issuer := m.users.usersByID[c.UserID], m.users.usersByID[c.IssuerUserID]
	if !verificationUserMatches(u, c) || issuer == nil || issuer.TokenVersion != c.IssuerVersion {
		return verificationInvalid()
	}
	if c.IssuerUserID != c.UserID {
		admin := false
		for _, role := range m.users.platformRolesByUser[issuer.ID] {
			if role == "platform_admin" {
				admin = true
			}
		}
		if issuer.Status != "active" || !admin || (c.Purpose != SetPassword && c.Purpose != PasswordReset) {
			return pkgerrors.ErrForbidden
		}
	}
	if _, exists := m.credentials[c.ID]; exists {
		return pkgerrors.ErrConflict
	}
	now := time.Now()
	for key, old := range m.credentials {
		if old.UsedAt == nil && old.Purpose == c.Purpose && (old.Target == c.Target || old.UserID == c.UserID) {
			old.UsedAt = &now
			m.credentials[key] = old
		}
	}
	m.credentials[c.ID] = c
	return nil
}
func (m *MemoryVerificationStore) RecordDelivery(_ context.Context, id string, accepted bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.credentials[id]
	if !ok || c.UsedAt != nil || c.Accepted {
		return verificationInvalid()
	}
	c.Accepted = accepted
	if !accepted {
		now := time.Now()
		c.UsedAt = &now
	}
	m.credentials[id] = c
	return nil
}
func (m *MemoryVerificationStore) Consume(_ context.Context, a VerificationAttempt) (*User, error) {
	if m.users == nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	m.users.mu.Lock()
	defer m.users.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	var c VerificationCredential
	found := false
	for _, v := range m.credentials {
		if v.UsedAt == nil && v.Purpose == a.Purpose && ((verificationSMS(a.Purpose) && v.Target == a.Target) || (!verificationSMS(a.Purpose) && v.Hash == a.Hash)) {
			c = v
			found = true
			break
		}
	}
	if !found {
		return nil, verificationInvalid()
	}
	stored := m.users.usersByID[c.UserID]
	now := time.Now()
	if !verificationUserMatches(stored, c) || !c.Accepted || !now.Before(c.ExpiresAt) || c.Attempts >= maxCodeTries || (a.UserID != "" && a.UserID != c.UserID) || (a.ExpectedVersion != nil && *a.ExpectedVersion != stored.TokenVersion) {
		return nil, verificationInvalid()
	}
	if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(a.Hash)) != 1 {
		c.Attempts++
		if c.Attempts >= maxCodeTries {
			c.UsedAt = &now
		}
		m.credentials[c.ID] = c
		if c.Attempts >= maxCodeTries {
			return nil, pkgerrors.ErrNotFound
		}
		return nil, verificationInvalid()
	}
	u := *stored
	if err := applyVerification(&u, c, a, now); err != nil {
		return nil, err
	}
	for uid, other := range m.users.usersByID {
		if uid != u.ID && ((u.Phone != "" && u.Phone == other.Phone) || strings.EqualFold(u.Email, other.Email)) {
			return nil, pkgerrors.ErrConflict
		}
	}
	if u.Email != stored.Email {
		delete(m.users.usersByEmail, stored.Email)
		m.users.usersByEmail[u.Email] = u.ID
	}
	if u.TokenVersion != stored.TokenVersion {
		m.invalidateLocked(u.ID, now)
	}
	*stored = u
	c.UsedAt = &now
	m.credentials[c.ID] = c
	return &u, nil
}
func (m *MemoryVerificationStore) invalidateLocked(uid string, now time.Time) {
	for key, c := range m.credentials {
		if c.UserID == uid && c.UsedAt == nil {
			c.UsedAt = &now
			m.credentials[key] = c
		}
	}
}
func (m *MemoryVerificationStore) UnbindPhone(_ context.Context, uid string, version int64) error {
	if m.users == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	m.users.mu.Lock()
	defer m.users.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.users.usersByID[uid]
	if u == nil || u.Status != "active" || u.TokenVersion != version {
		return verificationInvalid()
	}
	if u.EmailVerifiedAt == nil {
		return pkgerrors.ErrConflict
	}
	if u.Phone == "" {
		return pkgerrors.ErrNotFound
	}
	u.Phone = ""
	u.PhoneVerifiedAt = nil
	u.TokenVersion++
	u.RowVersion++
	m.invalidateLocked(uid, time.Now())
	return nil
}
