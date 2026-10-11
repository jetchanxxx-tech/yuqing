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

func TestAtomicBillingMigrationPreservesHistoricalUnlinkedFacts(t *testing.T) {
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
	schema := "billing_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	if _, err = provider.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO users(id,email,password_hash,status) VALUES('historical-user','admin@pangu.com','existing-hash','active')`,
		`INSERT INTO tenants(id,name,slug,db_name,status) VALUES('historical-team','original','original','original','active')`,
		`INSERT INTO analyses(id,tenant_id,name,created_by,state,report_content) VALUES('old-queued','historical-team','queued','historical-user','queued',''),('old-completed','historical-team','completed','historical-user','completed','<p>original report</p>')`,
		`INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('historical-team',7,'lite')`,
		`INSERT INTO credit_transactions(id,tenant_id,analysis_id,reason,delta,balance_after) VALUES('old-consume','historical-team','old-completed','consume',-1,7)`,
		`INSERT INTO api_keys(id,tenant_id,name,key_hash) VALUES('unknown-old-key','historical-team','historical','fixture-only-hash')`,
		`INSERT INTO usage_events(tenant_id,user_id,model,prompt_tokens,completion_tokens,cache_tokens,cost_micro_cny,billed_micro_cny) VALUES('historical-team','historical-user','historical-model',400,100,20,3200,0)`,
	} {
		if _, err = pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = provider.UpTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	var balance, runs, bindings, unlinked int
	if err = pool.QueryRow(ctx, `SELECT (SELECT balance FROM report_credits WHERE tenant_id='historical-team'),(SELECT count(*) FROM analysis_runs WHERE charge_mode='legacy_unbilled' AND actor_user_id='historical-user' AND consume_tx_id IS NULL),(SELECT count(*) FROM billing_exempt_principals),(SELECT count(*) FROM credit_transactions WHERE id='old-consume' AND run_id IS NULL AND delta=-1 AND balance_after=7)`).Scan(&balance, &runs, &bindings, &unlinked); err != nil {
		t.Fatal(err)
	}
	if balance != 7 || runs != 2 || bindings != 0 || unlinked != 1 {
		t.Fatalf("migration rewrote historical financial facts: %d/%d/%d/%d", balance, runs, bindings, unlinked)
	}
	var current, content string
	if err = pool.QueryRow(ctx, `SELECT current_run_id,report_content FROM analyses WHERE id='old-completed'`).Scan(&current, &content); err != nil {
		t.Fatal(err)
	}
	if current != "legacy:old-completed" || content != "<p>original report</p>" {
		t.Fatalf("historical output/run lost: %s/%s", current, content)
	}
	var unknown bool
	if err = pool.QueryRow(ctx, `SELECT creator_user_id IS NULL FROM api_keys WHERE id='unknown-old-key'`).Scan(&unknown); err != nil || !unknown {
		t.Fatalf("migration guessed key creator: %v/%v", unknown, err)
	}
	var tokens, cost, billed int64
	var usageStatus, costStatus string
	if err = pool.QueryRow(ctx, `SELECT quota_tokens,cost_micro_cny,billed_micro_cny,usage_status,cost_status FROM usage_events WHERE tenant_id='historical-team'`).Scan(&tokens, &cost, &billed, &usageStatus, &costStatus); err != nil {
		t.Fatal(err)
	}
	if tokens != 520 || cost != 3200 || billed != 0 || usageStatus != "unknown" || costStatus != "pending" {
		t.Fatalf("old usage reinterpreted as verified: %d/%d/%d/%s/%s", tokens, cost, billed, usageStatus, costStatus)
	}
}
