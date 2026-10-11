package auth

import (
	"context"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestAuthorizationSecurityPGDisabledUserCannotLoginAuthenticateOrRefresh(t *testing.T) {
	pool := pgtest.Pool(t, "auth_security_disabled")
	ctx := context.Background()
	svc := NewService(NewPGStore(pool), testSecret, "15m", "720h")
	p, pair := mustRegister(t, svc, "disabled-user@example.com", testPassword, "DisabledUser")

	updated, err := pool.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, p.UserID)
	if err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if updated.RowsAffected() != 1 {
		t.Fatalf("disabled %d users, want 1", updated.RowsAffected())
	}

	t.Run("login", func(t *testing.T) {
		if _, _, err := svc.Login(ctx, p.Email, testPassword); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			t.Errorf("Login(disabled user) error = %v, want ErrUnauthorized", err)
		}
	})
	t.Run("authenticate", func(t *testing.T) {
		if _, err := svc.Authenticate(ctx, pair.AccessToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			t.Errorf("Authenticate(disabled user) error = %v, want ErrUnauthorized", err)
		}
	})
	t.Run("refresh", func(t *testing.T) {
		if _, err := svc.Refresh(ctx, pair.RefreshToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			t.Errorf("Refresh(disabled user) error = %v, want ErrUnauthorized", err)
		}
	})
}

func TestAuthorizationSecurityPGAuthenticateUsesCurrentMemberRole(t *testing.T) {
	pool := pgtest.Pool(t, "auth_security_current_role")
	ctx := context.Background()
	svc := NewService(NewPGStore(pool), testSecret, "15m", "720h")
	p, pair := mustRegister(t, svc, "current-role@example.com", testPassword, "CurrentRole")

	updated, err := pool.Exec(ctx, `UPDATE tenant_members SET role = 'viewer' WHERE tenant_id = $1 AND user_id = $2`, p.TenantID, p.UserID)
	if err != nil {
		t.Fatalf("change member role: %v", err)
	}
	if updated.RowsAffected() != 1 {
		t.Fatalf("changed %d memberships, want 1", updated.RowsAffected())
	}

	current, err := svc.Authenticate(ctx, pair.AccessToken)
	if err != nil {
		t.Fatalf("Authenticate after member role update: %v", err)
	}
	if len(current.Roles) != 1 || current.Roles[0] != "viewer" {
		t.Errorf("authenticated roles = %v, want [viewer] from current membership", current.Roles)
	}
}

func TestAuthorizationSecurityPGLoginRecordsLastLoginAt(t *testing.T) {
	pool := pgtest.Pool(t, "auth_security_last_login")
	ctx := context.Background()
	svc := NewService(NewPGStore(pool), testSecret, "15m", "720h")
	p, _ := mustRegister(t, svc, "last-login@example.com", testPassword, "LastLogin")

	if _, err := pool.Exec(ctx, `UPDATE users SET last_login_at = NULL WHERE id = $1`, p.UserID); err != nil {
		t.Fatalf("clear last login timestamp: %v", err)
	}
	if _, _, err := svc.Login(ctx, p.Email, testPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}
	var recorded bool
	if err := pool.QueryRow(ctx, `SELECT last_login_at IS NOT NULL FROM users WHERE id = $1`, p.UserID).Scan(&recorded); err != nil {
		t.Fatalf("read last login timestamp: %v", err)
	}
	if !recorded {
		t.Error("successful Login left last_login_at NULL")
	}
}

func TestAuthorizationSecurityPGRegistrationRollsBackTenantFailure(t *testing.T) {
	pool := pgtest.Pool(t, "auth_security_register_tenant_failure")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE tenants ADD CONSTRAINT auth_security_reject_tenant_name CHECK (name <> 'TenantFailure的团队')`); err != nil {
		t.Fatalf("add tenant failure constraint: %v", err)
	}
	svc := NewService(NewPGStore(pool), testSecret, "15m", "720h")
	if _, _, err := svc.Register(ctx, "tenant-failure@example.com", testPassword, "TenantFailure"); err == nil {
		t.Fatal("Register must fail when the tenant write is rejected")
	}

	var users, tenants, members int
	if err := pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM tenants), (SELECT COUNT(*) FROM tenant_members)`).Scan(&users, &tenants, &members); err != nil {
		t.Fatalf("count registration rows: %v", err)
	}
	if users != 0 || tenants != 0 || members != 0 {
		t.Errorf("failed tenant write left users=%d tenants=%d members=%d, want all 0", users, tenants, members)
	}
}

func TestAuthorizationSecurityPGRegistrationRollsBackMemberFailure(t *testing.T) {
	pool := pgtest.Pool(t, "auth_security_register_member_failure")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE tenant_members ADD CONSTRAINT auth_security_reject_owner_role CHECK (role <> 'tenant_admin')`); err != nil {
		t.Fatalf("add member failure constraint: %v", err)
	}
	svc := NewService(NewPGStore(pool), testSecret, "15m", "720h")
	if _, _, err := svc.Register(ctx, "member-failure@example.com", testPassword, "MemberFailure"); err == nil {
		t.Fatal("Register must fail when the membership write is rejected")
	}

	var users, tenants, members int
	if err := pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM tenants), (SELECT COUNT(*) FROM tenant_members)`).Scan(&users, &tenants, &members); err != nil {
		t.Fatalf("count registration rows: %v", err)
	}
	if users != 0 || tenants != 0 || members != 0 {
		t.Errorf("failed membership write left users=%d tenants=%d members=%d, want all 0", users, tenants, members)
	}
}
