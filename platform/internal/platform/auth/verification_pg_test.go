package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestVerificationPGIssuingUserBinding(t *testing.T) {
	pool := pgtest.Pool(t, "verification_binding")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "verify-owner", "verify-owner@example.com")
	seedUCUserOnUserStore(t, users, "verify-other", "verify-other@example.com")
	sms := &fakeSMS{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), sms, nil, "https://example.com")
	if err := svc.SendPhoneCode(ctx, "verify-owner", "13800138000"); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindPhone(ctx, "verify-other", "13800138000", sms.code); err == nil {
		t.Error("credential issued to another immutable user must not bind this account")
	}
	other, _ := users.GetByID(ctx, "verify-other")
	if other.Phone != "" {
		t.Error("cross-user replay mutated account")
	}
}

func TestVerificationPGAttemptsAndSendGateSurviveRestart(t *testing.T) {
	pool := pgtest.Pool(t, "verification_restart")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "verify-restart", "verify-restart@example.com")
	sms := &fakeSMS{}
	service := func() *Service {
		s := NewService(users, testSecret, "15m", "720h")
		s.EnableUserCenter(users, NewPGVerificationStore(pool), sms, nil, "https://example.com")
		return s
	}
	svc := service()
	if err := svc.SendPhoneCode(ctx, "verify-restart", "13800138000"); err != nil {
		t.Fatal(err)
	}
	good := sms.code
	for i := 0; i < 5; i++ {
		if err := service().BindPhone(ctx, "verify-restart", "13800138000", "not-a-code"); err == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if err := service().BindPhone(ctx, "verify-restart", "13800138000", good); err == nil {
		t.Error("five failed attempts must remain invalidated across service reconstruction")
	}
	if err := service().SendPhoneCode(ctx, "verify-restart", "13800138000"); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
		t.Errorf("durable send gate lost on restart: %v", err)
	}
}

func TestVerificationPGEmailHashOnlyAndRevocation(t *testing.T) {
	pool := pgtest.Pool(t, "verification_email")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "verify-email", "verify-email@example.com")
	mail := &fakeMailSender{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), nil, mail, "https://example.com")
	if err := svc.SendVerificationEmail(ctx, "verify-email", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	token := strings.Split(strings.Split(mail.body, "token=")[1], "\"")[0]
	var plaintext int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM verification_tokens WHERE token=$1`, token).Scan(&plaintext); err != nil {
		t.Fatal(err)
	}
	if plaintext != 0 {
		t.Error("database persists raw emailed bearer credential")
	}
	if len(token) < 43 {
		t.Error("email credential lacks 32 bytes of random entropy")
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled',token_version=token_version+1 WHERE id='verify-email'`); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyEmail(ctx, token); err == nil {
		t.Error("disabled/revoked account must not consume credential or mutate email verification")
	}
	u, _ := users.GetByID(ctx, "verify-email")
	if u.EmailVerifiedAt != nil {
		t.Error("disabled account was mutated")
	}
}

func TestVerificationPGConsumptionFailureRollsBackIdentity(t *testing.T) {
	pool := pgtest.Pool(t, "verification_atomic")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "verify-atomic", "verify-atomic@example.com")
	mail := &fakeMailSender{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), nil, mail, "https://example.com")
	if err := svc.SendVerificationEmail(ctx, "verify-atomic", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	token := strings.Split(strings.Split(mail.body, "token=")[1], "\"")[0]
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_verification_consume() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.used_at IS NOT NULL THEN RAISE EXCEPTION 'injected consumption failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_verification_consume BEFORE UPDATE ON verification_tokens FOR EACH ROW EXECUTE FUNCTION reject_verification_consume()`); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyEmail(ctx, token); err == nil {
		t.Fatal("injected consume failure must fail operation")
	}
	u, _ := users.GetByID(ctx, "verify-atomic")
	if u.EmailVerifiedAt != nil {
		t.Error("identity update committed even though credential consumption failed")
	}
	var used bool
	if err := pool.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM verification_tokens WHERE user_id='verify-atomic'`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used || u.RowVersion != 0 || u.TokenVersion != 0 {
		t.Fatal("consume failure committed credential or identity versions")
	}

}

func TestVerificationPGPhoneHashOnlyAndVersionRevocation(t *testing.T) {
	pool := pgtest.Pool(t, "verification_phone_hash")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "verify-phone", "verify-phone@example.com")
	sms := &fakeSMS{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), sms, nil, "https://example.com")
	if err := svc.SendPhoneCode(ctx, "verify-phone", "13800138000"); err != nil {
		t.Fatal(err)
	}
	var raw int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sms_verification_codes WHERE code=$1`, sms.code).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != 0 {
		t.Error("database persists plaintext low-entropy SMS credential")
	}
	if err := svc.BindPhone(ctx, "verify-phone", "13800138000", sms.code); err != nil {
		t.Fatal(err)
	}
	u, _ := users.GetByID(ctx, "verify-phone")
	if u.Phone != "13800138000" || u.TokenVersion != 1 || u.RowVersion != 1 {
		t.Errorf("identity change must bind phone and revoke old JWT atomically: phone_bound=%v token_version=%d row_version=%d", u.Phone != "", u.TokenVersion, u.RowVersion)
	}
}

func TestVerificationPGQueuedRevocationRejectsOriginalActor(t *testing.T) {
	for _, action := range []string{"disable", "password"} {
		t.Run(action, func(t *testing.T) {
			pool := pgtest.Pool(t, "verification_queued")
			users := NewPGStore(pool)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			seedUCUserOnUserStore(t, users, "queued-user", "queued@example.com")
			cfg := pool.Config()
			cfg.ConnConfig.RuntimeParams["application_name"] = "verification-queued-" + action
			queued, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer queued.Close()
			svc := NewService(NewPGStore(queued), testSecret, "15m", "720h")
			sms := &fakeSMS{}
			svc.EnableUserCenter(NewPGStore(queued), NewPGVerificationStore(queued), sms, nil, "https://example.com")
			hold, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback(context.Background())
			if _, err = hold.Exec(ctx, `SELECT id FROM users WHERE id='queued-user' FOR UPDATE`); err != nil {
				t.Fatal(err)
			}
			out := make(chan error, 1)
			go func() { out <- svc.SendPhoneCode(ctx, "queued-user", "13800138000", 0) }()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				var blocked bool
				if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, "verification-queued-"+action).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("issuance never blocked on original user lock")
				}
			}
			statement := `UPDATE users SET token_version=token_version+1,password_hash='new-password-hash' WHERE id='queued-user'`
			if action == "disable" {
				statement = `UPDATE users SET token_version=token_version+1,status='disabled' WHERE id='queued-user'`
			}
			if _, err = hold.Exec(ctx, statement); err != nil {
				t.Fatal(err)
			}
			if err = hold.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-out; err == nil {
				t.Fatal("queued issuance adopted revoked actor version")
			}
			var credentials int
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM sms_verification_codes WHERE user_id='queued-user'`).Scan(&credentials); err != nil {
				t.Fatal(err)
			}
			if credentials != 0 || sms.calls != 0 {
				t.Fatal("revoked actor issued/delivered credential")
			}
		})
	}
}

func TestVerificationPGFailureRollsBackCredentialAndVersions(t *testing.T) {
	pool := pgtest.Pool(t, "verification_update_failure")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "update-user", "update@example.com")
	v := NewPGVerificationStore(pool)
	c := verificationFixture("update-user", PhoneBind, "13800138000", "123456", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE users ADD CONSTRAINT reject_verified_phone CHECK(phone IS NULL)`); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	a := VerificationAttempt{Purpose: PhoneBind, Target: c.Target, Hash: c.Hash, UserID: c.UserID, ExpectedVersion: &zero}
	if _, err := v.Consume(ctx, a); err == nil {
		t.Fatal("injected user write failure succeeded")
	}
	var used bool
	if err := pool.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM sms_verification_codes WHERE id=$1`, c.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	u, _ := users.GetByID(ctx, c.UserID)
	if used || u.Phone != "" || u.TokenVersion != 0 || u.RowVersion != 0 {
		t.Fatal("user write failure partially committed")
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE users DROP CONSTRAINT reject_verified_phone`); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Consume(ctx, a); err != nil {
		t.Fatalf("retry after rollback lost credential: %v", err)
	}
}

func TestVerificationPGPurposeIsolationAndPersistentHourlyGate(t *testing.T) {
	pool := pgtest.Pool(t, "verification_purpose_gate")
	u := NewPGStore(pool)
	ctx := context.Background()
	v := NewPGVerificationStore(pool)
	seedUCUserOnUserStore(t, u, "purpose-user", "purpose@example.com")
	if err := u.SetPhone(ctx, "purpose-user", "13800138000"); err != nil {
		t.Fatal(err)
	}
	bind := verificationFixture("purpose-user", PhoneBind, "13800138000", "123456", time.Minute)
	login := verificationFixture("purpose-user", PhoneLogin, "13800138000", "654321", time.Minute)
	for _, c := range []VerificationCredential{bind, login} {
		if err := v.Issue(ctx, c); err != nil {
			t.Fatal(err)
		}
		if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := v.Consume(ctx, VerificationAttempt{Purpose: PhoneLogin, Target: login.Target, Hash: bind.Hash}); err == nil {
		t.Fatal("bind credential crossed login purpose")
	}
	if _, err := v.Consume(ctx, VerificationAttempt{Purpose: PhoneLogin, Target: login.Target, Hash: login.Hash}); err != nil {
		t.Fatalf("independent purpose was overwritten: %v", err)
	}
	zero := int64(0)
	if _, err := v.Consume(ctx, VerificationAttempt{Purpose: PhoneBind, Target: bind.Target, Hash: bind.Hash, UserID: bind.UserID, ExpectedVersion: &zero}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := NewPGVerificationStore(pool).ReserveSend(ctx, PasswordReset, "unknown@example.com", "192.0.2.1", VerificationLimits{}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE verification_send_gates SET last_sent=now()-interval '61 seconds'`); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewPGVerificationStore(pool).ReserveSend(ctx, PasswordReset, "unknown@example.com", "192.0.2.2", VerificationLimits{}); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
		t.Fatal("fifth target hour limit reset on reconstruction")
	}
}

type rejectingVerificationSMS struct{ code string }

func (s *rejectingVerificationSMS) Send(_ context.Context, _, _ string, params map[string]string) error {
	s.code = params["code"]
	return fmt.Errorf("supplier rejected test payload")
}
func TestVerificationPGSupplierFailureInvalidatesCredentialKeepsGate(t *testing.T) {
	pool := pgtest.Pool(t, "verification_supplier_rejected")
	ctx := context.Background()
	users := NewPGStore(pool)
	seedUCUserOnUserStore(t, users, "reject-user", "reject@example.com")
	sender := &rejectingVerificationSMS{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), sender, nil, "https://example.com")
	if err := svc.SendPhoneCode(ctx, "reject-user", "13800138000"); !pkgerrors.Is(err, pkgerrors.ErrServiceUnavailable) {
		t.Fatal("rejected supplier falsely reported acceptance")
	}
	if err := svc.BindPhone(ctx, "reject-user", "13800138000", sender.code); err == nil {
		t.Fatal("supplier failure allowed phone binding")
	}
	var rejected bool
	var hash string
	if err := pool.QueryRow(ctx, `SELECT delivery_status='rejected' AND used_at IS NOT NULL AND code IS NULL,code_hash FROM sms_verification_codes WHERE user_id='reject-user'`).Scan(&rejected, &hash); err != nil {
		t.Fatal(err)
	}
	bare := sha256.Sum256([]byte(sender.code))
	if !rejected || hash == hex.EncodeToString(bare[:]) || hash == sender.code {
		t.Fatal("SMS failure state or server-secret HMAC missing")
	}
	restarted := NewService(users, testSecret, "15m", "720h")
	restarted.EnableUserCenter(users, NewPGVerificationStore(pool), &fakeSMS{}, nil, "https://example.com")
	if err := restarted.SendPhoneCode(ctx, "reject-user", "13800138000"); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
		t.Fatal("supplier failure rolled back durable send admission")
	}
	u, _ := users.GetByID(ctx, "reject-user")
	if u.Phone != "" || u.TokenVersion != 0 {
		t.Fatal("supplier failure changed identity")
	}
}

func TestVerificationPGPublicRequestsHaveMatchingDurableAdmission(t *testing.T) {
	pool := pgtest.Pool(t, "verification_public")
	ctx := context.Background()
	u := NewPGStore(pool)
	seedUCUserOnUserStore(t, u, "known-user", "known@example.com")
	mail := &fakeMailSender{}
	makeService := func() *Service {
		s := NewService(u, testSecret, "15m", "720h")
		s.EnableUserCenter(u, NewPGVerificationStore(pool), nil, mail, "https://example.com")
		return s
	}
	for _, target := range []string{"known@example.com", "unknown@example.com"} {
		if err := makeService().RequestPublicVerification(ctx, PasswordReset, target, "192.0.2.20"); err != nil {
			t.Fatal(err)
		}
	}
	if mail.calls != 1 {
		t.Fatal("unknown target caused delivery")
	}
	for _, target := range []string{"known@example.com", "unknown@example.com"} {
		if err := makeService().RequestPublicVerification(ctx, PasswordReset, target, "192.0.2.21"); !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
			t.Fatal("restart allowed public existence inference through rate limits")
		}
	}
	var gates int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM verification_send_gates WHERE gate_key LIKE 'target:password_reset:%'`).Scan(&gates); err != nil || gates != 2 {
		t.Fatal("unknown target gate was not durable")
	}
}

type verificationLoginBarrier struct {
	*PGStore
	reached chan struct{}
	release chan struct{}
}

func (s *verificationLoginBarrier) GetUserTenant(ctx context.Context, uid string) (*Tenant, error) {
	close(s.reached)
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.PGStore.GetUserTenant(ctx, uid)
}
func TestVerificationPGPhoneLoginCannotUpgradeConsumedVersion(t *testing.T) {
	pool := pgtest.Pool(t, "verification_login_version")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	users := NewPGStore(pool)
	seedUCUserOnUserStore(t, users, "login-user", "login@example.com")
	if err := users.SetPhone(ctx, "login-user", "13800138000"); err != nil {
		t.Fatal(err)
	}
	v := NewPGVerificationStore(pool)
	c := verificationFixture("login-user", PhoneLogin, "13800138000", "123456", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	barrier := &verificationLoginBarrier{PGStore: users, reached: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(barrier, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, v, nil, nil, "https://example.com")
	out := make(chan error, 1)
	go func() {
		_, pair, err := svc.LoginWithPhoneCode(ctx, c.Target, "123456")
		if pair != nil {
			out <- fmt.Errorf("issued fresh tokens after original credential revocation")
			return
		}
		out <- err
	}()
	select {
	case <-barrier.reached:
	case <-ctx.Done():
		t.Fatal("login never consumed credential")
	}
	if err := users.UpdatePassword(ctx, "login-user", "new-password-hash", 0); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	if err := <-out; !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Fatalf("consumed version must stay authoritative: %v", err)
	}
}

func TestVerificationPGPublicCodeFailuresStayUniform(t *testing.T) {
	pool := pgtest.Pool(t, "verification_public_failures")
	ctx := context.Background()
	u := NewPGStore(pool)
	seedUCUserOnUserStore(t, u, "login-user", "uniform@example.com")
	if err := u.SetPhone(ctx, "login-user", "13800138000"); err != nil {
		t.Fatal(err)
	}
	v := NewPGVerificationStore(pool)
	c := verificationFixture("login-user", PhoneLogin, "13800138000", "123456", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	s := NewService(u, testSecret, "15m", "720h")
	s.EnableUserCenter(u, v, nil, nil, "https://example.com")
	for i := 0; i < 6; i++ {
		for _, target := range []string{"13800138000", "13900139000"} {
			if _, _, err := s.LoginWithPhoneCode(ctx, target, "wrong"); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
				t.Fatalf("public failure differed at attempt %d: %v", i+1, err)
			}
		}
	}
}
