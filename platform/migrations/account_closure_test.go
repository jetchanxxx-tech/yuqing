package migrations

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClosureMigrationPreservesAccountsFinancialFactsAndNotificationIntent(t *testing.T) {
	dsn := os.Getenv("YUQING_TEST_PG_URL")
	if dsn == "" {
		t.Skip("disposable hosted PostgreSQL required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.Contains(cfg.ConnConfig.Database, "test") {
		t.Fatal("disposable test database required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "account_closure_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	source, err := fs.Sub(FS, "platform")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,name,status,token_version,row_version) VALUES('history','history@example.invalid','original','Historical owner','active',3,4); INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('history',7,'pro'); INSERT INTO credit_transactions(id,tenant_id,delta,reason,balance_after) VALUES('history-fact','history',7,'grant',7); INSERT INTO verification_tokens(id,user_id,type,purpose,target,expires_at,used_at,notice_target,notice_state,notice_next_attempt) VALUES('notice','history','email_change','email_change','new@example.invalid',now(),now(),'history@example.invalid','processing',now())`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	var intact bool
	if err = pool.QueryRow(ctx, `SELECT (SELECT name='Historical owner' AND status='active' AND token_version=3 AND row_version=4 AND closed_at IS NULL AND pii_anonymized_at IS NULL FROM users WHERE id='history') AND (SELECT balance=7 AND plan_code='pro' FROM report_credits WHERE tenant_id='history') AND (SELECT delta=7 AND balance_after=7 FROM credit_transactions WHERE id='history-fact') AND (SELECT notice_state='processing' AND notice_target='history@example.invalid' FROM verification_tokens WHERE id='notice') AND (SELECT count(*)=0 FROM account_closures)`).Scan(&intact); err != nil || !intact {
		t.Fatalf("closure migration changed historical facts: %v", err)
	}
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("destructive anonymization rollback accepted")
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 22 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}
