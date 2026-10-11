package migrations

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestAccountAdminSecurityMigration(t *testing.T) {
	url := os.Getenv("YUQING_TEST_PG_URL")
	if url == "" {
		t.Skip("YUQING_TEST_PG_URL is required")
	}

	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "account_admin_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	if _, err := provider.UpTo(ctx, 13); err != nil {
		t.Fatalf("apply v13: %v", err)
	}

	for _, stmt := range []string{
		`INSERT INTO tenants (id, name, slug, db_name, status, plan_code, quota_json, settings_json, created_at)
		 VALUES ('historical_active_tenant', 'Historical active', 'historical-active', 'historical_active_db',
		         'active', 'pro', '{"limit":12}', '{"keep":true}', '2026-01-02T03:04:05Z'),
		        ('historical_suspended_tenant', 'Historical suspended', 'historical-suspended', 'historical_suspended_db',
		         'suspended', 'free', '{"limit":3}', '{"keep":false}', '2026-02-03T04:05:06Z')`,
		`INSERT INTO users (id, email, password_hash, name, status, last_login_at, created_at,
		                   password_changed_at, email_verified_at, phone, phone_verified_at, timezone,
		                   avatar_url, trial_analysis_used, notification_prefs)
		 VALUES ('historical_active_user', 'historical-active@example.com', 'historical-active-hash',
		         'Historical active user', 'active', '2026-03-04T05:06:07Z', '2026-01-02T03:04:05Z',
		         '2026-02-01T00:00:00Z', '2026-01-03T00:00:00Z', '+8613800000001',
		         '2026-01-04T00:00:00Z', 'UTC', '/historical-avatar.png', 1, '{"task_completed":false}'),
		        ('historical_suspended_user', 'historical-suspended@example.com', 'historical-suspended-hash',
		         'Historical suspended user', 'suspended', NULL, '2026-02-03T04:05:06Z',
		         NULL, NULL, NULL, NULL, 'Asia/Shanghai', NULL, 0, '{"billing_reminder":true}')`,
		`INSERT INTO tenant_members (tenant_id, user_id, role, invited_at, accepted_at)
		 VALUES ('historical_active_tenant', 'historical_active_user', 'tenant_admin',
		         '2026-01-02T03:04:05Z', '2026-01-03T03:04:05Z'),
		        ('historical_active_tenant', 'historical_suspended_user', 'viewer', NULL, NULL),
		        ('historical_suspended_tenant', 'historical_active_user', 'analyst',
		         '2026-02-03T04:05:06Z', NULL)`,
		`INSERT INTO analyses (id, tenant_id, name, created_by, summary)
		 VALUES ('historical_analysis', 'historical_active_tenant', 'Historical analysis',
		         'historical_active_user', 'Keep historical analysis')`,
		`INSERT INTO reports (id, tenant_id, analysis_id, created_by, report_version, file_key)
		 VALUES ('historical_report', 'historical_active_tenant', 'historical_analysis',
		         'historical_active_user', 3, 'historical/report.html')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("insert v13 fixture: %v", err)
		}
	}

	// Subtracting absent JSON keys is harmless at v13 and permits a full comparison at v14.
	historicalSnapshot := func(t *testing.T) string {
		t.Helper()
		var snapshot string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
			'users', (SELECT jsonb_agg(to_jsonb(u) - ARRAY['token_version', 'row_version'] ORDER BY u.id)
			          FROM users u WHERE u.id LIKE 'historical_%'),
			'tenants', (SELECT jsonb_agg(to_jsonb(t) - 'row_version' ORDER BY t.id)
			            FROM tenants t WHERE t.id LIKE 'historical_%'),
			'members', (SELECT jsonb_agg(to_jsonb(m) - 'row_version' ORDER BY m.tenant_id, m.user_id)
			            FROM tenant_members m WHERE m.tenant_id LIKE 'historical_%'),
			'analyses', (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM analyses a),
			'reports', (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM reports r)
		)::text`).Scan(&snapshot); err != nil {
			t.Fatalf("read historical snapshot: %v", err)
		}
		return snapshot
	}
	before := historicalSnapshot(t)
	if _, err := provider.UpTo(ctx, 14); err != nil {
		t.Fatalf("apply v14: %v", err)
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 14 {
		t.Fatalf("want goose v14, got %d: %v", version, err)
	}
	for _, stmt := range []string{
		`INSERT INTO users (id, email, password_hash) VALUES ('new_user', 'new-user@example.com', 'new-hash')`,
		`INSERT INTO tenants (id, name, slug, db_name) VALUES ('new_tenant', 'New', 'new-tenant', 'new_db')`,
		`INSERT INTO tenant_members (tenant_id, user_id) VALUES ('new_tenant', 'new_user')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("insert v14 fixture: %v", err)
		}
	}

	t.Run("historical_data_and_version_defaults", func(t *testing.T) {
		if after := historicalSnapshot(t); after != before {
			t.Fatalf("v14 changed historical data\nbefore: %s\nafter: %s", before, after)
		}
		var users, tenants, members, roles int
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM users WHERE id LIKE 'historical_%' AND token_version = 0 AND row_version = 0),
			(SELECT count(*) FROM tenants WHERE id LIKE 'historical_%' AND row_version = 0),
			(SELECT count(*) FROM tenant_members WHERE tenant_id LIKE 'historical_%' AND row_version = 0),
			(SELECT count(*) FROM platform_user_roles)`).Scan(&users, &tenants, &members, &roles); err != nil {
			t.Fatal(err)
		}
		if users != 2 || tenants != 2 || members != 3 || roles != 0 {
			t.Fatalf("unexpected v14 defaults: users=%d tenants=%d members=%d roles=%d", users, tenants, members, roles)
		}
	})

	t.Run("new_rows_start_at_version_zero", func(t *testing.T) {
		var tokenVersion, userVersion, tenantVersion, memberVersion int64
		if err := pool.QueryRow(ctx, `SELECT u.token_version, u.row_version, t.row_version, m.row_version
			FROM users u JOIN tenant_members m ON m.user_id = u.id JOIN tenants t ON t.id = m.tenant_id
			WHERE u.id = 'new_user' AND t.id = 'new_tenant'`).Scan(&tokenVersion, &userVersion, &tenantVersion, &memberVersion); err != nil {
			t.Fatal(err)
		}
		if tokenVersion != 0 || userVersion != 0 || tenantVersion != 0 || memberVersion != 0 {
			t.Fatalf("new rows must start at zero: token=%d user=%d tenant=%d member=%d", tokenVersion, userVersion, tenantVersion, memberVersion)
		}
		var bigintColumns int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = $1 AND data_type = 'bigint' AND is_nullable = 'NO'
			AND ((table_name = 'users' AND column_name IN ('token_version', 'row_version'))
			  OR (table_name IN ('tenants', 'tenant_members') AND column_name = 'row_version'))`, schema).Scan(&bigintColumns); err != nil {
			t.Fatal(err)
		}
		if bigintColumns != 4 {
			t.Fatalf("want four non-null bigint version columns, got %d", bigintColumns)
		}
	})

	t.Run("versions_reject_negative_or_null", func(t *testing.T) {
		for _, column := range []struct {
			name string
			stmt string
		}{
			{"user_token", `UPDATE users SET token_version = $1 WHERE id = 'new_user'`},
			{"user_row", `UPDATE users SET row_version = $1 WHERE id = 'new_user'`},
			{"tenant_row", `UPDATE tenants SET row_version = $1 WHERE id = 'new_tenant'`},
			{"member_row", `UPDATE tenant_members SET row_version = $1 WHERE tenant_id = 'new_tenant' AND user_id = 'new_user'`},
		} {
			t.Run(column.name, func(t *testing.T) {
				for _, invalid := range []struct {
					value any
					code  string
				}{
					{int64(-1), "23514"},
					{nil, "23502"},
				} {
					_, err := pool.Exec(ctx, column.stmt, invalid.value)
					var pgErr *pgconn.PgError
					if !errors.As(err, &pgErr) || pgErr.Code != invalid.code {
						t.Fatalf("version value %v must fail with %s: %v", invalid.value, invalid.code, err)
					}
				}
			})
		}
	})

	t.Run("platform_roles_constraints", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash)
			VALUES ('new_grantor', 'new-grantor@example.com', 'grantor-hash')`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO platform_user_roles (user_id, role, granted_by)
			VALUES ('historical_active_user', 'platform_admin', 'new_grantor'),
			       ('historical_suspended_user', 'platform_admin', NULL)`); err != nil {
			t.Fatalf("insert valid platform roles: %v", err)
		}
		var grantedBy string
		var grantedAt time.Time
		if err := pool.QueryRow(ctx, `SELECT granted_by, granted_at FROM platform_user_roles
			WHERE user_id = 'historical_active_user' AND role = 'platform_admin'`).Scan(&grantedBy, &grantedAt); err != nil {
			t.Fatal(err)
		}
		if grantedBy != "new_grantor" || grantedAt.IsZero() {
			t.Fatalf("role grant metadata lost: granted_by=%q granted_at=%v", grantedBy, grantedAt)
		}
		var nullableGrantor bool
		if err := pool.QueryRow(ctx, `SELECT granted_by IS NULL FROM platform_user_roles
			WHERE user_id = 'historical_suspended_user' AND role = 'platform_admin'`).Scan(&nullableGrantor); err != nil || !nullableGrantor {
			t.Fatalf("bootstrap role must permit a null grantor: null=%t err=%v", nullableGrantor, err)
		}
		for _, invalid := range []struct {
			name string
			stmt string
			code string
		}{
			{"duplicate_role", `INSERT INTO platform_user_roles (user_id, role) VALUES ('historical_active_user', 'platform_admin')`, "23505"},
			{"tenant_role", `INSERT INTO platform_user_roles (user_id, role) VALUES ('new_user', 'tenant_admin')`, "23514"},
			{"empty_role", `INSERT INTO platform_user_roles (user_id, role) VALUES ('new_user', '')`, "23514"},
			{"missing_user", `INSERT INTO platform_user_roles (user_id, role) VALUES ('missing_user', 'platform_admin')`, "23503"},
			{"missing_grantor", `INSERT INTO platform_user_roles (user_id, role, granted_by) VALUES ('new_user', 'platform_admin', 'missing_user')`, "23503"},
			{"delete_role_user", `DELETE FROM users WHERE id = 'historical_active_user'`, "23503"},
			{"delete_grantor", `DELETE FROM users WHERE id = 'new_grantor'`, "23503"},
		} {
			t.Run(invalid.name, func(t *testing.T) {
				_, err := pool.Exec(ctx, invalid.stmt)
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != invalid.code {
					t.Fatalf("role operation must fail with %s: %v", invalid.code, err)
				}
			})
		}
		var restrictForeignKeys int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
			WHERE conrelid = $1::regclass AND contype = 'f' AND confdeltype = 'r'`,
			schema+".platform_user_roles").Scan(&restrictForeignKeys); err != nil {
			t.Fatal(err)
		}
		if restrictForeignKeys != 2 {
			t.Fatalf("want two RESTRICT platform-role foreign keys, got %d", restrictForeignKeys)
		}
		if after := historicalSnapshot(t); after != before {
			t.Fatal("rejected role operations changed historical account or business data")
		}
	})

	indexes := []struct {
		name    string
		columns string
	}{
		{"idx_users_status_created_id", "status,created_at,id"},
		{"idx_tenants_status_created_id", "status,created_at,id"},
		{"idx_tenant_members_user_tenant", "user_id,tenant_id"},
		{"idx_platform_user_roles_role_user", "role,user_id"},
	}
	t.Run("admin_query_indexes", func(t *testing.T) {
		for _, index := range indexes {
			var columns string
			if err := pool.QueryRow(ctx, `SELECT string_agg(a.attname, ',' ORDER BY k.ordinality)
				FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
				CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ordinality)
				JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
				WHERE c.relnamespace = $1::regnamespace AND c.relname = $2`, schema, index.name).Scan(&columns); err != nil {
				t.Fatalf("read index %s: %v", index.name, err)
			}
			if columns != index.columns {
				t.Errorf("index %s has columns %q, want %q", index.name, columns, index.columns)
			}
		}
	})

	t.Run("down_restores_v13_schema_and_history", func(t *testing.T) {
		if _, err := provider.DownTo(ctx, 13); err != nil {
			t.Fatalf("rollback v14: %v", err)
		}
		if version, err := provider.GetDBVersion(ctx); err != nil || version != 13 {
			t.Fatalf("want goose v13 after rollback, got %d: %v", version, err)
		}
		var versions int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = $1 AND table_name IN ('users', 'tenants', 'tenant_members')
			AND column_name IN ('token_version', 'row_version')`, schema).Scan(&versions); err != nil {
			t.Fatal(err)
		}
		if versions != 0 {
			t.Errorf("rollback retained %d version columns", versions)
		}
		for _, name := range append([]string{"platform_user_roles"}, indexes[0].name, indexes[1].name, indexes[2].name, indexes[3].name) {
			var absent bool
			if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, schema+"."+name).Scan(&absent); err != nil {
				t.Fatal(err)
			}
			if !absent {
				t.Errorf("rollback retained %s", name)
			}
		}
		if after := historicalSnapshot(t); after != before {
			t.Fatal("rollback changed historical account or business data")
		}
	})
}
