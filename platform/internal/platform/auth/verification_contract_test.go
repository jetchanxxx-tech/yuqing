package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func verificationFixture(uid, purpose, target, value string, ttl time.Duration) VerificationCredential {
	return VerificationCredential{ID: id.New(), UserID: uid, IssuerUserID: uid, Purpose: purpose, Target: target, Hash: verificationHash(testSecret, purpose, target, value), ExpiresAt: time.Now().Add(ttl)}
}
func verificationStoreContract(t *testing.T, store func(*testing.T) (UserStore, VerificationStore)) {
	ctx := context.Background()
	t.Run("purpose_and_user_isolation_resend_and_unique_rollback", func(t *testing.T) {
		users, v := store(t)
		seedUCUserOnUserStore(t, users, "owner", "owner@example.com")
		seedUCUserOnUserStore(t, users, "other", "other@example.com")
		issue := func(c VerificationCredential) {
			t.Helper()
			if err := v.Issue(ctx, c); err != nil {
				t.Fatal(err)
			}
			if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
				t.Fatal(err)
			}
		}
		old := verificationFixture("owner", PhoneBind, "13800138000", "111111", time.Minute)
		issue(old)
		latest := verificationFixture("owner", PhoneBind, "13800138000", "222222", time.Minute)
		issue(latest)
		zero := int64(0)
		for _, a := range []VerificationAttempt{
			{Purpose: PhoneBind, Target: old.Target, Hash: old.Hash, UserID: "owner", ExpectedVersion: &zero},
			{Purpose: PhoneLogin, Target: latest.Target, Hash: latest.Hash},
			{Purpose: PhoneBind, Target: latest.Target, Hash: latest.Hash, UserID: "other", ExpectedVersion: &zero},
		} {
			if _, err := v.Consume(ctx, a); err == nil {
				t.Fatal("old, cross-purpose or cross-user credential accepted")
			}
		}
		if err := users.SetPhone(ctx, "other", latest.Target); err != nil {
			t.Fatal(err)
		}
		a := VerificationAttempt{Purpose: PhoneBind, Target: latest.Target, Hash: latest.Hash, UserID: "owner", ExpectedVersion: &zero}
		if _, err := v.Consume(ctx, a); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Fatalf("unique target must reject: %v", err)
		}
		u, _ := users.GetByID(ctx, "owner")
		if u.Phone != "" || u.TokenVersion != 0 || u.RowVersion != 0 {
			t.Fatal("failed unique mutation partially changed owner")
		}
		if err := users.ClearPhone(ctx, "other"); err != nil {
			t.Fatal(err)
		}
		if _, err := v.Consume(ctx, a); err != nil {
			t.Fatalf("unique rollback consumed credential: %v", err)
		}
		if _, err := v.Consume(ctx, a); err == nil {
			t.Fatal("consumed credential replayed")
		}
	})
	t.Run("email_concurrency_preserves_trial_and_profile", func(t *testing.T) {
		users, v := store(t)
		seedUCUserOnUserStore(t, users, "owner", "owner@example.com")
		if _, err := users.ConsumeTrialAnalysis(ctx, "owner"); err != nil {
			t.Fatal(err)
		}
		if err := users.UpdateProfile(ctx, "owner", "保留昵称", "/avatar.png", "Asia/Tokyo"); err != nil {
			t.Fatal(err)
		}
		c := verificationFixture("owner", EmailVerify, "owner@example.com", "email-value", time.Hour)
		if err := v.Issue(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		out := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := v.Consume(ctx, VerificationAttempt{Purpose: EmailVerify, Hash: c.Hash})
				out <- err
			}()
		}
		wg.Wait()
		close(out)
		successes := 0
		for err := range out {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("same token succeeded %d times", successes)
		}
		u, _ := users.GetByID(ctx, "owner")
		if u.EmailVerifiedAt == nil || u.TrialAnalysisUsed != 1 || u.Name != "保留昵称" || u.AvatarURL != "/avatar.png" || u.Timezone != "Asia/Tokyo" || u.RowVersion != 1 {
			t.Fatal("verification lost profile or changed trial credit")
		}
	})
	t.Run("pending_activation_and_purpose_specific_password_mutation", func(t *testing.T) {
		users, v := store(t)
		ctx := context.Background()
		uid := "pending"
		if err := users.(Store).CreateUser(ctx, User{ID: uid, Email: "pending@example.com", Status: "pending_activation", PasswordHash: "unusable"}); err != nil {
			t.Fatal(err)
		}
		c := verificationFixture(uid, SetPassword, "pending@example.com", "activation-value", 30*time.Minute)
		if err := v.Issue(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, err := v.Consume(ctx, VerificationAttempt{Purpose: PasswordReset, Hash: c.Hash, PasswordHash: "$argon2id$fixture"}); err == nil {
			t.Fatal("activation credential crossed reset purpose")
		}
		u, err := v.Consume(ctx, VerificationAttempt{Purpose: SetPassword, Hash: c.Hash, PasswordHash: "$argon2id$fixture"})
		if err != nil || u.Status != "active" || u.TokenVersion != 1 || u.EmailVerifiedAt == nil {
			t.Fatalf("activation not atomic: %v", err)
		}
	})
	t.Run("unaccepted_and_rejected_delivery_cannot_mutate", func(t *testing.T) {
		users, v := store(t)
		seedUCUserOnUserStore(t, users, "owner", "owner@example.com")
		c := verificationFixture("owner", EmailVerify, "owner@example.com", "unaccepted", time.Hour)
		if err := v.Issue(ctx, c); err != nil {
			t.Fatal(err)
		}
		a := VerificationAttempt{Purpose: EmailVerify, Hash: c.Hash}
		if _, err := v.Consume(ctx, a); err == nil {
			t.Fatal("pending supplier acceptance consumed")
		}
		if err := v.RecordDelivery(ctx, c.ID, false); err != nil {
			t.Fatal(err)
		}
		if _, err := v.Consume(ctx, a); err == nil {
			t.Fatal("rejected delivery consumed")
		}
		if err := v.RecordDelivery(ctx, c.ID, true); err == nil {
			t.Fatal("rejected credential resurrected")
		}
	})
	t.Run("durable_public_limits_and_purpose_interval", func(t *testing.T) {
		_, v := store(t)
		if err := v.ReserveSend(ctx, PasswordReset, "known@example.com", "192.0.2.1", VerificationLimits{}); err != nil {
			t.Fatal(err)
		}
		if err := v.ReserveSend(ctx, PasswordReset, "known@example.com", "192.0.2.2", VerificationLimits{}); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
			t.Fatal("target interval bypass")
		}
		if err := v.ReserveSend(ctx, EmailVerify, "known@example.com", "192.0.2.1", VerificationLimits{}); err != nil {
			t.Fatal("separate purpose interval collided")
		}
		for i := 0; i < 18; i++ {
			if err := v.ReserveSend(ctx, PasswordReset, id.New()+"@example.com", "192.0.2.1", VerificationLimits{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := v.ReserveSend(ctx, PasswordReset, "unknown@example.com", "192.0.2.1", VerificationLimits{}); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
			t.Fatal("IP hourly gate bypassed")
		}
	})
}
func TestVerificationPGAtomicContract(t *testing.T) {
	verificationStoreContract(t, func(t *testing.T) (UserStore, VerificationStore) {
		p := pgtest.Pool(t, "verification_atomic_contract")
		return NewPGStore(p), NewPGVerificationStore(p)
	})
}
func TestVerificationMemoryAtomicContract(t *testing.T) {
	verificationStoreContract(t, func(t *testing.T) (UserStore, VerificationStore) {
		u := NewMemoryStore()
		v := NewMemoryVerificationStore()
		v.users = u
		return u, v
	})
}

func TestVerificationPublicAdmissionAntiEnumeration(t *testing.T) {
	s, u, v := newUCService(t)
	seedUCUserOnUserStore(t, u, "known", "known@example.com")
	mail := &fakeMailSender{}
	s.emailSender = mail
	for _, target := range []string{"known@example.com", "unknown@example.com"} {
		if err := s.RequestPublicVerification(context.Background(), PasswordReset, target, "192.0.2.1"); err != nil {
			t.Fatalf("public admission differs: %v", err)
		}
	}
	if mail.calls != 1 {
		t.Fatal("unknown target emitted supplier request")
	}
	s2 := NewService(u, "test-secret", "15m", "720h")
	s2.EnableUserCenter(u, v, nil, mail, "https://test.example.com")
	for _, target := range []string{"known@example.com", "unknown@example.com"} {
		if err := s2.RequestPublicVerification(context.Background(), PasswordReset, target, "192.0.2.2"); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
			t.Fatal("known/unknown persistent rate limit differs")
		}
	}
	if strings.Contains(mail.body, "unknown@example.com") {
		t.Fatal("unknown identity delivered")
	}
}
