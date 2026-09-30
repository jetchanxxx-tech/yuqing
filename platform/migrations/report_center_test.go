package migrations

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestReportCenterMigration(t *testing.T) {
	url := os.Getenv("YUQING_TEST_PG_URL")
	if url == "" {
		t.Skip("YUQING_TEST_PG_URL is required")
	}

	for _, hotfix := range []bool{false, true} {
		name := "clean_v7"
		if hotfix {
			name = "hotfixed_v7"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			admin, err := pgxpool.New(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			schema := "reconcile_" + strconv.FormatInt(time.Now().UnixNano(), 10)
			if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
					t.Errorf("drop test schema: %v", err)
				}
			}()
			if _, err := admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS citext"); err != nil {
				t.Fatal(err)
			}

			cfg, err := pgxpool.ParseConfig(url)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			db := stdlib.OpenDBFromPool(pool)
			defer db.Close()
			subFS, err := fs.Sub(FS, "platform")
			if err != nil {
				t.Fatal(err)
			}
			provider, err := goose.NewProvider(goose.DialectPostgres, db, subFS)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.UpTo(ctx, 7); err != nil {
				t.Fatalf("apply v7: %v", err)
			}

			for _, stmt := range []string{
				"INSERT INTO tenants (id, name, slug, db_name) VALUES ('t1', 'Test', 'test', 'test_db')",
				"INSERT INTO users (id, email, password_hash) VALUES ('u1', 'test@example.com', 'test')",
				"INSERT INTO tenant_members (tenant_id, user_id) VALUES ('t1', 'u1')",
				"INSERT INTO analyses (id, tenant_id, name) VALUES ('a1', 't1', 'Test')",
				"INSERT INTO reports (id, tenant_id, analysis_id) VALUES ('r1', 't1', 'a1')",
			} {
				if _, err := pool.Exec(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			if hotfix {
				for _, stmt := range []string{
					"ALTER TABLE analyses ADD COLUMN created_by TEXT NOT NULL DEFAULT ''",
					"ALTER TABLE reports ADD COLUMN created_by TEXT NOT NULL DEFAULT ''",
					"ALTER TABLE reports ADD COLUMN report_version TEXT NOT NULL DEFAULT 'v1'",
				} {
					if _, err := pool.Exec(ctx, stmt); err != nil {
						t.Fatal(err)
					}
				}
				// Only the hotfixed fixture supplies a verified historical owner.
				if _, err := pool.Exec(ctx, "UPDATE analyses SET created_by='u1' WHERE id='a1'"); err != nil {
					t.Fatal(err)
				}
			}
			for _, stmt := range []string{
				"INSERT INTO tenants (id, name, slug, db_name) VALUES ('t2', 'Orphan', 'orphan', 'orphan_db')",
				"INSERT INTO analyses (id, tenant_id, name) VALUES ('orphan_a', 't2', 'Keep')",
				"INSERT INTO reports (id, tenant_id, analysis_id) VALUES ('orphan_r', 't2', 'orphan_a')",
			} {
				if _, err := pool.Exec(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := provider.UpTo(ctx, 8); err == nil || !strings.Contains(err.Error(), "cannot backfill analyses.created_by") {
				t.Fatalf("v8 must explicitly reject tenant with no member: %v", err)
			}
			var gooseVersion int
			if err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&gooseVersion); err != nil || gooseVersion != 7 {
				t.Fatalf("failed migration must stay at v7, got %d: %v", gooseVersion, err)
			}
			var preserved int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM reports WHERE id='orphan_r'`).Scan(&preserved); err != nil || preserved != 1 {
				t.Fatalf("failed migration lost report: count=%d err=%v", preserved, err)
			}
			for _, stmt := range []string{"DELETE FROM reports WHERE id='orphan_r'", "DELETE FROM analyses WHERE id='orphan_a'"} {
				if _, err := pool.Exec(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			if !hotfix {
				if _, err := provider.UpTo(ctx, 8); err == nil || !strings.Contains(err.Error(), "cannot backfill analyses.created_by") {
					t.Fatalf("must not assign first member as historical creator: %v", err)
				}
				if err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&gooseVersion); err != nil || gooseVersion != 7 {
					t.Fatalf("ambiguous ownership must leave goose at v7, got %d: %v", gooseVersion, err)
				}
				for _, stmt := range []string{"DELETE FROM reports WHERE id='r1'", "DELETE FROM analyses WHERE id='a1'"} {
					if _, err := pool.Exec(ctx, stmt); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := provider.UpTo(ctx, 8); err != nil {
				t.Fatalf("apply v8 to %s: %v", name, err)
			}
			if !hotfix {
				for _, stmt := range []string{
					"INSERT INTO analyses (id, tenant_id, name, created_by) VALUES ('a1', 't1', 'Test', 'u1')",
					"INSERT INTO reports (id, tenant_id, analysis_id, created_by) VALUES ('r1', 't1', 'a1', 'u1')",
				} {
					if _, err := pool.Exec(ctx, stmt); err != nil {
						t.Fatal(err)
					}
				}
			}

			var owner, reportOwner, versionType string
			var version int
			if err := pool.QueryRow(ctx, `SELECT a.created_by, r.created_by, r.report_version, pg_typeof(r.report_version)::text
				FROM analyses a JOIN reports r ON r.analysis_id = a.id`).Scan(&owner, &reportOwner, &version, &versionType); err != nil {
				t.Fatal(err)
			}
			if owner != "u1" || reportOwner != "u1" || version != 1 || versionType != "integer" {
				t.Errorf("unexpected migrated report: %s %s %d %s", owner, reportOwner, version, versionType)
			}
			var constraints int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
				WHERE conname IN ('fk_analyses_created_by', 'fk_reports_created_by')
				AND connamespace = $1::regnamespace AND confdeltype = 'r'`, schema).Scan(&constraints); err != nil {
				t.Fatal(err)
			}
			if constraints != 2 {
				t.Errorf("want two RESTRICT ownership foreign keys, got %d", constraints)
			}
			var ownershipDefaults int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema = $1 AND table_name IN ('analyses', 'reports')
				AND column_name = 'created_by' AND column_default IS NOT NULL`, schema).Scan(&ownershipDefaults); err != nil {
				t.Fatal(err)
			}
			if ownershipDefaults != 0 {
				t.Errorf("ownership columns retain %d defaults", ownershipDefaults)
			}
			if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id='u1'"); err == nil {
				t.Fatal("deleting the owner silently destroyed owned analysis/report")
			} else {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
					t.Fatalf("user deletion must fail with foreign-key violation: %v", err)
				}
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM analyses a JOIN reports r ON r.analysis_id=a.id
				WHERE a.id='a1' AND a.created_by='u1' AND r.id='r1' AND r.created_by='u1'`).Scan(&preserved); err != nil || preserved != 1 {
				t.Fatalf("user deletion lost business data: count=%d err=%v", preserved, err)
			}
		})
	}
}
