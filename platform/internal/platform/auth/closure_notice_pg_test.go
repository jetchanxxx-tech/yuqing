package auth

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/notification"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/storage"
	"github.com/yuqing/platform/internal/platform/accountclosure"
)

func seedClosureNotice(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO users(id,email,password_hash,name) VALUES('notice-owner','current@example.invalid','hash','Owner'); INSERT INTO verification_tokens(id,user_id,type,purpose,target,expires_at,used_at,notice_target,notice_state,notice_next_attempt) VALUES('closure-notice','notice-owner','email_change','email_change','current@example.invalid',now(),now(),'old@example.invalid','pending',now())`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestIdentityPGNoticeClaimPreservesDisabledSecurityButRejectsClosure(t *testing.T) {
	for _, status := range []string{"active", "disabled", "closure_pending", "closed"} {
		for _, issuer := range []bool{false, true} {
			t.Run(status+map[bool]string{false: "/owner", true: "/issuer"}[issuer], func(t *testing.T) {
				pool := pgtest.Pool(t, "closure_notice_claim")
				seedClosureNotice(t, pool)
				ctx := context.Background()
				uid := "notice-owner"
				if issuer {
					if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,name) VALUES('issuer','issuer@example.invalid','hash','Issuer'); UPDATE verification_tokens SET issuer_user_id='issuer' WHERE id='closure-notice'`); err != nil {
						t.Fatal(err)
					}
					uid = "issuer"
				}
				if _, err := pool.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, uid, status); err != nil {
					t.Fatal(err)
				}
				v := NewPGVerificationStore(pool)
				n, err := v.ClaimIdentityNotice(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if status == "closure_pending" || status == "closed" {
					if n != nil {
						t.Fatalf("closure identity was newly dispatched: %+v", n)
					}
					return
				}
				if n == nil || n.Recipient != "old@example.invalid" {
					t.Fatalf("required security notice suppressed for %s: %+v", status, n)
				}
				if err := v.CompleteIdentityNotice(ctx, *n, notification.Accepted("custom", "sandbox-security-notice")); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestClosurePGFailedInflightNoticeCannotBeReclaimedAfterRequest(t *testing.T) {
	pool := pgtest.Pool(t, "closure_notice_failed")
	seedClosureNotice(t, pool)
	ctx := context.Background()
	v := NewPGVerificationStore(pool)
	n, err := v.ClaimIdentityNotice(ctx)
	if err != nil || n == nil {
		t.Fatalf("initial claim: %+v %v", n, err)
	}
	s := accountclosure.NewPGStore(pool)
	s.SetAvatarStorage(storage.NewLocalAvatar(t.TempDir()))
	if _, err := s.Request(ctx, accountclosure.Actor{UserID: "notice-owner", PasswordHash: "hash"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := v.CompleteIdentityNotice(ctx, *n, notification.Receipt{Provider: "custom", State: "rejected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE verification_tokens SET notice_next_attempt=now() WHERE id='closure-notice'`); err != nil {
		t.Fatal(err)
	}
	if retry, err := v.ClaimIdentityNotice(ctx); err != nil || retry != nil {
		t.Fatalf("failed pre-request attempt was reclaimed during closure: %+v %v", retry, err)
	}
	var intact bool
	if err := pool.QueryRow(ctx, `SELECT notice_state='failed' AND notice_attempts=1 AND notice_receipt->>'state'='rejected' FROM verification_tokens WHERE id='closure-notice'`).Scan(&intact); err != nil || !intact {
		t.Fatalf("denied claim changed actual failed receipt: %v", err)
	}
}

type closureNoticeAvatarBarrier struct {
	*storage.LocalAvatar
	entered chan struct{}
	release chan struct{}
}

func (b *closureNoticeAvatarBarrier) Reconcile(ctx context.Context, uid string, cursor int64, limit int) (int64, bool, int, error) {
	close(b.entered)
	select {
	case <-ctx.Done():
		return cursor, false, 0, ctx.Err()
	case <-b.release:
		return b.LocalAvatar.Reconcile(ctx, uid, cursor, limit)
	}
}

// A previously admitted worker may commit its processing state after closure's
// claim snapshot. The page must recheck the row after waiting for that worker,
// preserve its copied recipient, and allow the actual attempt's receipt CAS.
func TestClosurePGPersonalPagePreservesNoticeThatWinsRowLock(t *testing.T) {
	pool := pgtest.Pool(t, "closure_notice_row_lock")
	seedClosureNotice(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	v := NewPGVerificationStore(pool)
	n, err := v.ClaimIdentityNotice(ctx)
	if err != nil || n == nil {
		t.Fatalf("initial claim: %+v %v", n, err)
	}
	avatar := &closureNoticeAvatarBarrier{LocalAvatar: storage.NewLocalAvatar(t.TempDir()), entered: make(chan struct{}), release: make(chan struct{})}
	s := accountclosure.NewPGStore(pool)
	s.SetAvatarStorage(avatar)
	if _, err = s.Request(ctx, accountclosure.Actor{UserID: "notice-owner", PasswordHash: "hash"}, nil); err != nil {
		t.Fatal(err)
	}
	if err = v.CompleteIdentityNotice(ctx, *n, notification.Receipt{Provider: "custom", State: "rejected"}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second'; UPDATE verification_tokens SET notice_next_attempt=now() WHERE id='closure-notice'`); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		status *accountclosure.Status
		err    error
	}
	result := make(chan outcome, 1)
	go func() { r, e := s.Process(ctx, "notice-owner", 100); result <- outcome{r, e} }()
	select {
	case <-avatar.entered:
	case <-ctx.Done():
		t.Fatal("closure did not reach post-claim storage boundary")
	}
	gate, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	// Model an already admitted old worker, intentionally bypassing the new
	// status guard. This remains a necessary defense during rolling upgrades.
	if err = gate.QueryRow(ctx, `UPDATE verification_tokens SET notice_state='processing',notice_attempts=notice_attempts+1,notice_next_attempt=now()+interval '2 minutes' WHERE id='closure-notice' RETURNING id,notice_target,notice_attempts`).Scan(&n.ID, &n.Recipient, &n.Attempt); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err = gate.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	close(avatar.release)
	blocked := false
	for !blocked && ctx.Err() == nil {
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if !blocked {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("personal page did not wait for notice row lock")
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-result
	if r.err != nil || r.status == nil {
		t.Fatalf("page outcome: %+v %v", r.status, r.err)
	}
	var intact bool
	if err = pool.QueryRow(ctx, `SELECT (SELECT notice_state='processing' AND notice_target='old@example.invalid' AND notice_attempts=2 FROM verification_tokens WHERE id='closure-notice') AND (SELECT status='closure_pending' AND email='current@example.invalid' FROM users WHERE id='notice-owner')`).Scan(&intact); err != nil || !intact || r.status.State == "completed" {
		t.Fatalf("page erased in-flight evidence or released identity: %+v intact=%v err=%v", r.status, intact, err)
	}
	sender := &identityNoticeSender{}
	if err = sender.Send(ctx, n.Recipient, "Security notice", "Email changed"); err != nil {
		t.Fatal(err)
	}
	if err = v.CompleteIdentityNotice(ctx, *n, notification.Accepted("custom", "sandbox-closure-notice")); err != nil {
		t.Fatalf("actual attempt receipt was lost: %v", err)
	}
	s.SetAvatarStorage(avatar.LocalAvatar)
	r.status, r.err = s.Process(ctx, "notice-owner", 100)
	if r.err != nil || r.status.State != "completed" {
		t.Fatalf("settled attempt blocked resumed closure: %+v %v", r.status, r.err)
	}
	if err = pool.QueryRow(ctx, `SELECT notice_state='accepted' AND notice_target='' AND COALESCE(target,'')='' AND notice_attempts=2 AND notice_receipt->>'state'='accepted' FROM verification_tokens WHERE id='closure-notice'`).Scan(&intact); err != nil || !intact || sender.count() != 1 {
		t.Fatalf("receipt lost or recipient retained after completion: %v", err)
	}
}
