package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

type unavailableRandom struct{}

func (unavailableRandom) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestVerificationRandomFailureCannotIssueCredential(t *testing.T) {
	users := NewMemoryStore()
	seedUCUserOnUserStore(t, users, "entropy-user", "entropy@example.com")
	mail := &fakeMailSender{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewMemoryVerificationStore(), nil, mail, "https://example.com")
	previous := rand.Reader
	rand.Reader = unavailableRandom{}
	defer func() { rand.Reader = previous }()
	if err := svc.SendVerificationEmail(context.Background(), "entropy-user", "https://example.com"); err == nil {
		t.Error("randomness failure must fail closed instead of timestamp fallback")
	}
	if mail.calls != 0 {
		t.Error("credential delivered despite entropy source failure")
	}
}
