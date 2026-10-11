package migrations

import (
	"context"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestK11MigrationPreservesHistoricalEntitlementsAndDoesNotInventEvents(t *testing.T) {
	dsn := os.Getenv("YUQING_TEST_PG_URL")
	if dsn == "" {
		t.Skip("hosted disposable PG required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.Contains(cfg.ConnConfig.Database, "test") {
		t.Fatal("disposable test DB required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "k11_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
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
	p, err := goose.NewProvider(goose.DialectPostgres, db, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 23); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,name,token_version,row_version,notification_prefs) VALUES('old-owner','history@example.invalid','old-hash','Historical',3,4,'{"task_completed":false,"billing_reminder":true}'); INSERT INTO tenants(id,name,slug,db_name,status) VALUES('old-team','Historical team','old-team','old-team','active'); INSERT INTO report_credits(tenant_id,balance,plan_code,version) VALUES('old-team',7,'pro',5); INSERT INTO credit_transactions(id,tenant_id,delta,reason,balance_after,version) VALUES('old-ledger','old-team',7,'grant',7,5); INSERT INTO analyses(id,tenant_id,name,state,created_by) VALUES('old-complete','old-team','Historical complete','completed','old-owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	var intact bool
	if err := pool.QueryRow(ctx, `SELECT (SELECT balance=7 AND plan_code='pro' AND version=5 AND plan_source='report_credits' FROM report_credits WHERE tenant_id='old-team') AND (SELECT email='history@example.invalid' AND name='Historical' AND password_hash='old-hash' AND token_version=3 AND row_version=4 AND notification_epoch=0 AND notification_prefs='{"task_completed":false,"billing_reminder":true}'::jsonb FROM users WHERE id='old-owner') AND (SELECT delta=7 AND balance_after=7 AND version=5 FROM credit_transactions WHERE id='old-ledger') AND (SELECT count(*)=0 FROM account_notification_events) AND (SELECT state='completed' FROM analyses WHERE id='old-complete')`).Scan(&intact); err != nil || !intact {
		t.Fatalf("new migration rewrote history or invented intent: %v", err)
	}
	if _, err := p.Down(ctx); err == nil {
		t.Fatal("destructive receipt/source rollback accepted")
	}
	if version, err := p.GetDBVersion(ctx); err != nil || version != 25 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}
