package v1_test

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"

	"github.com/yuqing/platform/internal/app"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestPGPublicRegistrationCannotBootstrapPlatformAdministrator(t *testing.T) {
	pool := pgtest.Pool(t, "public_registration_admin")
	u, err := url.Parse(os.Getenv(pgtest.EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	u.RawQuery = query.Encode()
	t.Setenv("YUQING_BETA_SKIP_CREDITS", "true")
	t.Setenv("YUQING_BOOTSTRAP_ADMIN_EMAIL", "operator@example.com")
	cfg := &config.Config{Store: config.StoreConfig{Driver: "postgres"}, Queue: config.QueueConfig{Driver: "postgres"}}
	cfg.DB.Primary = u.String()
	cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL = testJWTSecret, "15m", "720h"
	deps := app.Build(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer deps.PGPool.Close()
	p, _, err := deps.Auth.Register(context.Background(), "operator@example.com", "Password123", "Anonymous registrant")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range p.Roles {
		if role == "platform_admin" {
			t.Error("an unverified public registration acquired platform_admin in PostgreSQL mode")
		}
	}
	var roles int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM platform_user_roles`).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if roles != 0 {
		t.Errorf("public registration persisted %d platform roles, want 0", roles)
	}
}
