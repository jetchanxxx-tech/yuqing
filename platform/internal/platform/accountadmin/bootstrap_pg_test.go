package accountadmin_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/platform/accountadmin"
	"github.com/yuqing/platform/internal/platform/auth"
)

func TestAccountAdminPGBootstrapSerializesInitialGrant(t *testing.T) {
	pool := pgtest.Pool(t, "account_admin_bootstrap")
	ctx := context.Background()
	first, second := id.New(), id.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES
		($1,'bootstrap-lock-first@example.invalid','fixture','active'),
		($2,'bootstrap-lock-second@example.invalid','fixture','active')`, first, second); err != nil {
		t.Fatal(err)
	}
	store := accountadmin.NewPGStore(pool)
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Rollback(context.Background()) })
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auth.PlatformAdminLockID); err != nil {
		t.Fatal(err)
	}
	// Holding the same lock as ordinary management must prevent the CLI from
	// granting a role. Removing the production lock makes this call succeed.
	blockedCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	initialized, err := store.BootstrapPlatformAdmin(blockedCtx, first)
	cancel()
	if err == nil || initialized {
		t.Fatalf("bootstrap bypassed the management lock: initialized=%v error=%v", initialized, err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		initialized bool
		err         error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for _, userID := range []string{first, second} {
		go func(userID string) {
			<-start
			initialized, err := store.BootstrapPlatformAdmin(ctx, userID)
			results <- outcome{initialized: initialized, err: err}
		}(userID)
	}
	close(start)
	grants, rejections := 0, 0
	for range 2 {
		result := <-results
		if result.initialized && result.err == nil {
			grants++
		} else if !result.initialized && result.err != nil {
			rejections++
		} else {
			t.Errorf("unexpected concurrent bootstrap result: %+v", result)
		}
	}
	var roles, audits, versions, credentials int
	if err := pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM platform_user_roles),
		(SELECT COUNT(*) FROM audit_logs WHERE action='user.bootstrap_admin'),
		(SELECT SUM(row_version) FROM users),(SELECT SUM(token_version) FROM users)`).
		Scan(&roles, &audits, &versions, &credentials); err != nil {
		t.Fatal(err)
	}
	if grants != 1 || rejections != 1 || roles != 1 || audits != 1 || versions != 1 || credentials != 1 {
		t.Errorf("concurrent initialization: grants=%d rejections=%d roles=%d audits=%d versions=%d credentials=%d",
			grants, rejections, roles, audits, versions, credentials)
	}
}
