package auth

import (
	"context"
	"errors"
	"github.com/yuqing/platform/internal/testsupport/notificationsandbox"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestIdentityPGEmailChangePersistsOldAddressNotice(t *testing.T) {
	pool := pgtest.Pool(t, "identity_notice")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "notice-owner", "old@example.invalid")
	v := NewPGVerificationStore(pool)
	c := verificationFixture("notice-owner", EmailChange, "new@example.invalid", "sandbox-email-change", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	u, err := v.Consume(ctx, VerificationAttempt{Purpose: EmailChange, Hash: c.Hash, UserID: c.UserID, ExpectedVersion: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "new@example.invalid" || u.TokenVersion != 1 {
		t.Fatal("email change did not commit")
	}
	var state, target string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(to_jsonb(v)->>'notice_state',''),COALESCE(to_jsonb(v)->>'notice_target','') FROM verification_tokens v WHERE id=$1`, c.ID).Scan(&state, &target); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || target != "old@example.invalid" {
		t.Fatal("committed email change lost durable old-address notification intent")
	}
}

// New email uniqueness and outbox failure must roll back the same credential,
// hash and account versions; later retry still consumes exactly once.
func TestIdentityPGEmailNoticeFailureRollsBackWholeMutation(t *testing.T) {
	pool := pgtest.Pool(t, "identity_notice_rollback")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "rollback-owner", "original@example.invalid")
	v := NewPGVerificationStore(pool)
	c := verificationFixture("rollback-owner", EmailChange, "changed@example.invalid", "rollback-notice", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_identity_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.notice_state='pending' THEN RAISE EXCEPTION 'injected notice intent failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_identity_notice BEFORE UPDATE ON verification_tokens FOR EACH ROW EXECUTE FUNCTION fail_identity_notice()`); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	attempt := VerificationAttempt{Purpose: EmailChange, Hash: c.Hash, UserID: c.UserID, ExpectedVersion: &zero}
	if _, err := v.Consume(ctx, attempt); err == nil {
		t.Fatal("notice failure must abort change")
	}
	u, err := users.GetByID(ctx, c.UserID)
	if err != nil {
		t.Fatal(err)
	}
	var used bool
	if err := pool.QueryRow(ctx, `SELECT used_at IS NOT NULL FROM verification_tokens WHERE id=$1`, c.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used || u.Email != "original@example.invalid" || u.TokenVersion != 0 || u.RowVersion != 0 {
		t.Fatal("partial identity/notice transaction committed")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER fail_identity_notice ON verification_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Consume(ctx, attempt); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityPGNoticeFailedSendPersistsAndRetryNeverResendsAccepted(t *testing.T) {
	pool := pgtest.Pool(t, "identity_notice_retry")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "retry-owner", "retry-original@example.invalid")
	v := NewPGVerificationStore(pool)
	c := verificationFixture("retry-owner", EmailChange, "retry-changed@example.invalid", "retry-notice", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := v.Consume(ctx, VerificationAttempt{Purpose: EmailChange, Hash: c.Hash, UserID: c.UserID, ExpectedVersion: &zero}); err != nil {
		t.Fatal(err)
	}
	failed := &identityNoticeSender{reject: true}
	service := func(sender *identityNoticeSender) *Service {
		s := NewService(NewPGStore(pool), testSecret, "15m", "1h")
		s.EnableUserCenter(NewPGStore(pool), NewPGVerificationStore(pool), nil, sender, "https://example.invalid")
		return s
	}
	if err := service(failed).DispatchIdentityNotices(ctx, 25); err == nil {
		t.Fatal("failed send must be observable")
	}
	var state, target string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT notice_state,notice_target,notice_attempts FROM verification_tokens WHERE id=$1`, c.ID).Scan(&state, &target, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || target != "retry-original@example.invalid" || attempts != 1 {
		t.Fatal("failed notification lost retry state")
	}
	u, _ := users.GetByID(ctx, c.UserID)
	if u.Email != "retry-changed@example.invalid" || u.TokenVersion != 1 {
		t.Fatal("notice failure undid committed identity")
	}
	if _, err := pool.Exec(ctx, `UPDATE verification_tokens SET notice_next_attempt=now()-interval '1 second' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	accepted := &identityNoticeSender{}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- service(accepted).DispatchIdentityNotices(ctx, 25) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := service(accepted).DispatchIdentityNotices(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if accepted.count() != 1 {
		t.Fatalf("accepted notice resent by concurrency/reconstruction: %d", accepted.count())
	}
	if err := pool.QueryRow(ctx, `SELECT notice_state,notice_target,notice_attempts FROM verification_tokens WHERE id=$1`, c.ID).Scan(&state, &target, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "accepted" || target != "" || attempts != 2 {
		t.Fatal("acceptance not terminal or recipient retained")
	}
	var receipt string
	if err := pool.QueryRow(ctx, `SELECT notice_receipt::text FROM verification_tokens WHERE id=$1`, c.ID).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(receipt, "example.invalid") || strings.Contains(receipt, "retry-notice") {
		t.Fatal("notice receipt contains private payload")
	}
}

type identityNoticeSender struct {
	mu       sync.Mutex
	reject   bool
	messages int
	inbox    notificationsandbox.Inbox
}

func (s *identityNoticeSender) Send(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reject {
		return errors.New("sandbox rejected")
	}
	if !strings.HasSuffix(to, "@example.invalid") || strings.Contains(body, "token=") {
		return errors.New("unsafe notice payload")
	}
	s.inbox.Accept(to, "email_changed_notice", body)
	s.messages++
	return nil
}
func (s *identityNoticeSender) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.messages }

func TestIdentityPGConcurrentPasswordResetOnlyOneCommit(t *testing.T) {
	pool := pgtest.Pool(t, "identity_reset_concurrent")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "reset-owner", "concurrent@example.invalid")
	v := NewPGVerificationStore(pool)
	c := verificationFixture("reset-owner", PasswordReset, "concurrent@example.invalid", "reset-concurrency", time.Minute)
	if err := v.Issue(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	s := NewService(users, testSecret, "15m", "1h")
	s.EnableUserCenter(users, v, nil, nil, "")
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- s.ResetPassword(ctx, "reset-concurrency", "", "", "ConcurrentReset123") }()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-errs == nil {
			successes++
		}
	}
	u, err := users.GetByID(ctx, c.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if successes != 1 || u.TokenVersion != 1 || u.RowVersion != 1 || !VerifyPassword(u.PasswordHash, "ConcurrentReset123") {
		t.Fatal("concurrent reset did not commit exactly one consumption/version")
	}
}
