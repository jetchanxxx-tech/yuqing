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

func TestIdentityVerificationMigrationInvalidatesPlaintextPreservesUsers(t *testing.T) {
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
	schema := "verification_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
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
	if _, err = provider.UpTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO users(id,email,password_hash,name,phone,timezone,trial_analysis_used) VALUES('historic','historic@example.com','original-hash','Original','13800138000','Asia/Tokyo',1)`,
		`INSERT INTO verification_tokens(id,user_id,token,type,expires_at) VALUES('old-email','historic','old-plaintext-link','email_verify',now()+interval '1 day')`,
		`INSERT INTO sms_verification_codes(id,phone,code,purpose,expires_at) VALUES('old-sms','13800138000','123456','bind',now()+interval '5 minutes')`,
	} {
		if _, err = pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = provider.UpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	var intact, invalidated bool
	if err = pool.QueryRow(ctx, `SELECT email='historic@example.com' AND password_hash='original-hash' AND name='Original' AND phone='13800138000' AND timezone='Asia/Tokyo' AND trial_analysis_used=1 AND token_version=0 AND row_version=0 FROM users WHERE id='historic'`).Scan(&intact); err != nil || !intact {
		t.Fatal("migration changed existing personal/security fields")
	}
	if err = pool.QueryRow(ctx, `SELECT (SELECT token IS NULL AND used_at IS NOT NULL FROM verification_tokens WHERE id='old-email') AND (SELECT code IS NULL AND used_at IS NOT NULL FROM sms_verification_codes WHERE id='old-sms')`).Scan(&invalidated); err != nil || !invalidated {
		t.Fatal("legacy plaintext remained usable")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,phone) VALUES('collision','collision@example.com','hash','13800138000')`); err == nil {
		t.Fatal("migration removed users phone uniqueness")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO verification_tokens(id,user_id,token,type,expires_at) VALUES('unsafe','historic','plaintext','email_verify',now()+interval '1 day')`); err == nil {
		t.Fatal("legacy application can still store plaintext")
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 16 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}
