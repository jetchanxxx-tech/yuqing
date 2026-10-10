package auth

import (
	"context"
	"time"

	"github.com/yuqing/platform/internal/pkg/notification"
)

type memoryIdentityNotice struct {
	IdentityNotice
	State   string
	Next    time.Time
	Receipt notification.Receipt
}

func (m *MemoryVerificationStore) IdentityLinkState(_ context.Context, purpose, hash, uid string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.credentials {
		if c.Purpose != purpose || c.Hash != hash || (uid != "" && c.UserID != uid) {
			continue
		}
		if c.UsedAt != nil {
			return "used", nil
		}
		if !time.Now().Before(c.ExpiresAt) {
			return "expired", nil
		}
	}
	return "invalid", nil
}
func (m *MemoryVerificationStore) ClaimIdentityNotice(_ context.Context) (*IdentityNotice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, n := range m.notices {
		if n.State == "accepted" || time.Now().Before(n.Next) {
			continue
		}
		n.State = "processing"
		n.Attempt++
		n.Next = time.Now().Add(2 * time.Minute)
		m.notices[key] = n
		copy := n.IdentityNotice
		return &copy, nil
	}
	return nil, nil
}
func (m *MemoryVerificationStore) CompleteIdentityNotice(_ context.Context, n IdentityNotice, receipt notification.Receipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.notices[n.ID]
	if !ok || current.State != "processing" || current.Attempt != n.Attempt {
		return verificationInvalid()
	}
	r, err := notification.Normalize(receipt, "email_changed_notice")
	if err != nil {
		return verificationInvalid()
	}
	current.Receipt = r
	current.State = "failed"
	current.Next = time.Now().Add(time.Minute)
	if r.State == "accepted" {
		current.State = "accepted"
		current.Recipient = ""
	}
	m.notices[n.ID] = current
	return nil
}
