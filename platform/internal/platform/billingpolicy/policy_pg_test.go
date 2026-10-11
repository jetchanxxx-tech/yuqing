package billingpolicy

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"testing"
)

func TestExemptionDoesNotFollowRoleTenantOrTokenEmail(t *testing.T) {
	pool := pgtest.Pool(t, "billingpolicy")
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO tenants(id,name,slug,db_name,status) VALUES('team','team','team','team','active')`,
		`INSERT INTO users(id,email,password_hash,status) VALUES('fixed','admin@pangu.com','fixture','active'),('other','other@example.invalid','fixture','active')`,
		`INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('team','fixed','tenant_admin'),('team','other','tenant_admin')`,
		`INSERT INTO platform_user_roles(user_id,role) VALUES('fixed','platform_admin'),('other','platform_admin')`,
		`INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('team',0,'lite')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(pool)
	resolve := func(user string, want bool) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		d, err := svc.ResolveTx(ctx, tx, "team", Actor{UserID: user})
		if err != nil {
			t.Fatal(err)
		}
		if d.Exempt != want || d.PlanCode != "lite" {
			t.Fatalf("decision for %s=%+v", user, d)
		}
	}
	resolve("fixed", false)
	resolve("other", false)
	if _, err := svc.Bind(ctx, "fixed", true); err != nil {
		t.Fatal(err)
	}
	resolve("fixed", true)
	resolve("other", false)
	if _, err := pool.Exec(ctx, `UPDATE users SET email='changed@example.invalid' WHERE id='fixed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email='admin@pangu.com' WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	resolve("fixed", true)
	resolve("other", false)
	if _, err := pool.Exec(ctx, `UPDATE users SET token_version=1 WHERE id='fixed'`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	version := int64(0)
	if _, err := svc.ResolveTx(ctx, tx, "team", Actor{UserID: "fixed", TokenVersion: &version}); !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
		t.Fatalf("revoked JWT accepted: %v", err)
	}
}

func TestFixedAdminBindingPGRejectsInvalidIdentityAndRollsBackAuditFailure(t *testing.T) {
	for _, kind := range []string{"missing", "inactive", "no_admin_role", "audit_failure"} {
		t.Run(kind, func(t *testing.T) {
			pool := pgtest.Pool(t, "binding_preconditions")
			ctx := context.Background()
			if kind != "missing" {
				if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,status) VALUES('fixed','admin@pangu.com','fixture','active')`); err != nil {
					t.Fatal(err)
				}
				if kind != "no_admin_role" {
					if _, err := pool.Exec(ctx, `INSERT INTO platform_user_roles(user_id,role) VALUES('fixed','platform_admin')`); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "inactive" {
					if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id='fixed'`); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "audit_failure" {
				if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_binding_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$`); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_binding_audit BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION reject_binding_audit()`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NewService(pool).Bind(ctx, "fixed", true); err == nil {
				t.Fatal("invalid binding committed")
			}
			var policies, audits int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM billing_exempt_principals),(SELECT count(*) FROM audit_logs)`).Scan(&policies, &audits); err != nil {
				t.Fatal(err)
			}
			if policies != 0 || audits != 0 {
				t.Fatalf("binding failure left policy/audit=%d/%d", policies, audits)
			}
		})
	}
}
