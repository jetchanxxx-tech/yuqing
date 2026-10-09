package auth

// 用户中心内存实现：UserStore + VerificationStore。
// MVP 阶段与 MemoryStore 共存（同一进程内）；生产接 Redis/PG 时换实现。

import (
	"context"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

var _ UserStore = (*MemoryStore)(nil)
var _ VerificationStore = (*MemoryVerificationStore)(nil)

// UpdatePassword verifies account status/version and updates the credential
// within one lock, so old password snapshots cannot overwrite a newer change.
func (m *MemoryStore) UpdatePassword(_ context.Context, userID, newHash string, expectedVersion int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByID[userID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if u.Status != "active" {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "account unavailable")
	}
	if u.TokenVersion != expectedVersion {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "credentials changed; sign in again")
	}
	now := time.Now()
	u.PasswordHash = newHash
	u.PasswordChangedAt = &now
	u.TokenVersion++
	u.RowVersion++
	return nil
}

// MarkEmailVerified 标记邮箱已验证。
func (m *MemoryStore) MarkEmailVerified(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByID[userID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	now := time.Now()
	u.EmailVerifiedAt = &now
	return nil
}

// UpdateProfile 更新昵称/头像/时区（空串字段跳过）。
func (m *MemoryStore) UpdateProfile(_ context.Context, userID, name, avatarURL, timezone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByID[userID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if name != "" {
		u.Name = name
	}
	if avatarURL != "" {
		u.AvatarURL = avatarURL
	}
	if timezone != "" {
		u.Timezone = timezone
	}
	return nil
}

// SetPhone 绑定手机号（其他账号已绑定时冲突）。
func (m *MemoryStore) SetPhone(_ context.Context, userID, phone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByID[userID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	for id, other := range m.usersByID {
		if id != userID && other.Phone == phone {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "phone already bound to another account")
		}
	}
	now := time.Now()
	u.Phone = phone
	u.PhoneVerifiedAt = &now
	return nil
}

// ClearPhone 解绑手机号。
func (m *MemoryStore) ClearPhone(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByID[userID]
	if !ok {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	u.Phone = ""
	u.PhoneVerifiedAt = nil
	return nil
}

// ConsumeTrialAnalysis 原子消耗试用额度（0→1）。
func (m *MemoryStore) ConsumeTrialAnalysis(_ context.Context, userID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.usersByID[userID]
	if !ok {
		return false, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if u.TrialAnalysisUsed != 0 {
		return false, nil
	}
	u.TrialAnalysisUsed = 1
	return true, nil
}

// GetByID 按 ID 查用户（返回副本）。
func (m *MemoryStore) GetByID(_ context.Context, userID string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.usersByID[userID]
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	cp := *u
	return &cp, nil
}

// GetByPhone 按手机号查用户。
func (m *MemoryStore) GetByPhone(_ context.Context, phone string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.usersByID {
		if u.Phone == phone {
			cp := *u
			return &cp, nil
		}
	}
	return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
}
