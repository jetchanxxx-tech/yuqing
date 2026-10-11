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

func TestAdminCreditMigrationPreservesLedgerAndVersions(t *testing.T) {
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
	schema := "admin_credit_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	if _, err = provider.UpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('historical',7,'pro'); INSERT INTO credit_transactions(id,tenant_id,delta,reason,balance_after) VALUES('history','historical',7,'grant',7)`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var intact bool
	if err = pool.QueryRow(ctx, `SELECT (SELECT balance=7 AND plan_code='pro' AND version=0 FROM report_credits WHERE tenant_id='historical') AND (SELECT delta=7 AND reason='grant' AND balance_after=7 AND reason_detail='' AND actor_id IS NULL AND idempotency_key IS NULL FROM credit_transactions WHERE id='history')`).Scan(&intact); err != nil || !intact {
		t.Fatal("migration changed historical financial facts")
	}
	if _, err = pool.Exec(ctx, `UPDATE report_credits SET balance=6 WHERE tenant_id='historical'`); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT balance=6 AND version=1 FROM report_credits WHERE tenant_id='historical'`).Scan(&intact); err != nil || !intact {
		t.Fatal("existing writer did not advance version")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO credit_transactions(id,tenant_id,delta,reason,balance_after) VALUES('invalid','historical',1,'admin_adjust',7)`); err == nil {
		t.Fatal("database accepted adjustment without durable intent")
	}
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("destructive ledger rollback accepted")
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 20 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}
