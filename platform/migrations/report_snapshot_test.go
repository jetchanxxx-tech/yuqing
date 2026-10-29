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

func TestReportSnapshotMigrationBackfillsOnlyVerifiableLatestVersion(t *testing.T) {
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
	schema := "report_snapshot_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	if _, err = provider.UpTo(ctx, 29); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES('snapshot-user','snapshot-migration@example.invalid','fixture');
INSERT INTO analyses(id,tenant_id,created_by,name,state,report_content,finished_at) VALUES
 ('latest-a','snapshot-t','snapshot-user','latest','completed','<p>only current version</p>','2026-10-29T10:00:00Z'),
 ('unknown-a','snapshot-t','snapshot-user','unknown','completed','<p>cannot date</p>',NULL);
INSERT INTO reports(id,tenant_id,analysis_id,created_by,format,status,report_version,created_at) VALUES
 ('old-r','snapshot-t','latest-a','snapshot-user','html','completed',1,'2026-10-28T10:00:00Z'),
 ('new-r','snapshot-t','latest-a','snapshot-user','html','completed',2,'2026-10-29T10:00:01Z'),
 ('unknown-r','snapshot-t','unknown-a','snapshot-user','html','completed',1,'2026-10-29T10:00:01Z');`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 30); err != nil {
		t.Fatal(err)
	}
	var valid bool
	if err = pool.QueryRow(ctx, `SELECT
 (SELECT content_html='' FROM reports WHERE id='old-r') AND
 (SELECT content_html='<p>only current version</p>' FROM reports WHERE id='new-r') AND
 (SELECT content_html='' FROM reports WHERE id='unknown-r') AND
 (SELECT report_content='<p>only current version</p>' FROM analyses WHERE id='latest-a') AND
 (SELECT count(*)=1 FROM users WHERE id='snapshot-user')`).Scan(&valid); err != nil || !valid {
		t.Fatal("migration changed or invented history", err)
	}
	if applied, err := provider.UpTo(ctx, 30); err != nil || len(applied) != 0 {
		t.Fatal("repeat migration not noop", err)
	}
	if _, err := provider.Down(ctx); err == nil {
		t.Fatal("destructive snapshot rollback accepted")
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 30 {
		t.Fatal(version, err)
	}
}
