package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestBillingBindingCLIProcess(t *testing.T) {
	if os.Getenv("K4_BINDING_CLI_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"yuqing-cli"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("missing CLI arguments")
}

func TestFixedAdminBindingIsImmutableAndIdempotent(t *testing.T) {
	pool := pgtest.Pool(t, "billing_binding_cli")
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO users(id,email,password_hash,status) VALUES('fixed','admin@pangu.com','fixture','active'),('other','other@example.invalid','fixture','active')`,
		`INSERT INTO platform_user_roles(user_id,role) VALUES('fixed','platform_admin'),('other','platform_admin')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	dsn, err := url.Parse(os.Getenv(pgtest.EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = query.Encode()
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("auth:\n  jwt_secret: fixture-only-not-a-real-secret\nstore:\n  driver: postgres\ndb:\n  primary: %q\n", dsn.String())), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(wantSuccess bool, args ...string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestBillingBindingCLIProcess$", "--", "bind-billing-exempt-admin"}, args...)...)
		cmd.Env = append(os.Environ(), "K4_BINDING_CLI_PROCESS=1", "YUQING_CONFIG="+config)
		output, err := cmd.CombinedOutput()
		if (err == nil) != wantSuccess {
			t.Fatalf("binding command success=%v, want %v; output: %s", err == nil, wantSuccess, output)
		}
	}
	invoke(false, "--expected-user-id", "other", "--apply")
	invoke(true, "--expected-user-id", "fixed")
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM billing_exempt_principals`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("precheck changed policy: %d, %v", count, err)
	}
	invoke(true, "--expected-user-id", "fixed", "--apply")
	invoke(true, "--expected-user-id", "fixed", "--apply")
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='billing.exempt.bind'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("binding audit count=%d, %v", count, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE billing_exempt_principals SET user_id='other'`); err == nil {
		t.Fatal("policy owner transferable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM billing_exempt_principals`); err == nil {
		t.Fatal("policy deletable")
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email='renamed@example.invalid' WHERE id='fixed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email='admin@pangu.com' WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	invoke(true, "--expected-user-id", "fixed", "--apply")
	invoke(false, "--expected-user-id", "other", "--apply")
	var bound string
	if err := pool.QueryRow(ctx, `SELECT user_id FROM billing_exempt_principals`).Scan(&bound); err != nil || bound != "fixed" {
		t.Fatalf("binding=%s, %v", bound, err)
	}
}
