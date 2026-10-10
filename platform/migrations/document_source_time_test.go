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

func TestDocumentSourceTimeMigrationPreservesKnownAndUnknown(t *testing.T) {
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
	schema := "source_time_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	if _, err = provider.UpTo(ctx, 18); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO raw_documents(id,tenant_id,analysis_id,title,published_at) VALUES ('known','fixture','fixture','known instant','2026-03-08T07:00:00Z'),('lost','fixture','fixture','old unknown',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	var intact bool
	if err = pool.QueryRow(ctx, `SELECT (SELECT published_at='2026-03-08T07:00:00Z'::timestamptz AND source_published_at IS NULL FROM raw_documents WHERE id='known') AND (SELECT published_at IS NULL AND source_published_at IS NULL FROM raw_documents WHERE id='lost')`).Scan(&intact); err != nil || !intact {
		t.Fatal("migration invented or changed historical timestamp facts")
	}
	if _, err = pool.Exec(ctx, `UPDATE raw_documents SET source_published_at='2026-03-08' WHERE id='lost'`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Down(ctx); err == nil {
		t.Fatal("destructive rollback unexpectedly accepted")
	}
	if err = pool.QueryRow(ctx, `SELECT published_at IS NULL AND source_published_at='2026-03-08' FROM raw_documents WHERE id='lost'`).Scan(&intact); err != nil || !intact {
		t.Fatal("failed rollback lost source text")
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 19 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}
