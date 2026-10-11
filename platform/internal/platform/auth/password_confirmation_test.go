package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

func TestPasswordConfirmationMemoryAdmissionAndPreflight(t *testing.T) {
	ctx := context.Background()
	users := NewMemoryStore()
	v := NewMemoryVerificationStore()
	if err := users.CreateUser(ctx, User{ID: "hash-activation", Email: "activation@example.invalid", Status: "pending_activation", PasswordHash: "unusable"}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(users, testSecret, "15m", "1h")
	svc.EnableUserCenter(users, v, nil, nil, "")
	c := verificationFixture("hash-activation", SetPassword, "activation@example.invalid", "activation-control", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmVerification(ctx, SetPassword, "", "activation-control", "Activation12345", nil); !pkgerrors.Is(err, pkgerrors.ErrBadRequest) {
		t.Fatal("missing trusted source bypassed memory admission")
	}
	for i := 0; i < 20; i++ {
		purpose := PasswordReset
		if i%2 == 1 {
			purpose = SetPassword
		}
		if _, err := svc.ConfirmVerification(ctx, purpose, "", fmt.Sprintf("unknown-%d", i), "Activation12345", nil, "192.0.2.10"); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			t.Fatalf("cheap rejection: %v", err)
		}
	}
	// Reconstructing the service keeps its store budget, including activation.
	svc = NewService(users, testSecret, "15m", "1h")
	svc.EnableUserCenter(users, v, nil, nil, "")
	if _, err := svc.ConfirmVerification(ctx, SetPassword, "", "activation-control", "Activation12345", nil, "192.0.2.10"); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
		t.Fatal("memory activation escaped exhausted budget")
	}
	u, _ := users.GetByID(ctx, c.UserID)
	if u.Status != "pending_activation" || u.TokenVersion != 0 {
		t.Fatal("limited activation mutated identity")
	}
	v.mu.Lock()
	gate := v.gates["confirm:ip:192.0.2.10"]
	gate.Start = time.Now().Add(-time.Minute - time.Second)
	v.gates["confirm:ip:192.0.2.10"] = gate
	v.mu.Unlock()
	u, err := svc.ConfirmVerification(ctx, SetPassword, "", "activation-control", "Activation12345", nil, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if u.Status != "active" || u.TokenVersion != 1 || !strings.HasPrefix(u.PasswordHash, "$argon2id$v=19$m=65536,t=3,p=4$") || !VerifyPassword(u.PasswordHash, "Activation12345") {
		t.Fatal("bounded activation lost hash strength or atomic version/state mutation")
	}
}
