package auth

import (
	"context"
	"strings"
	"testing"

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
