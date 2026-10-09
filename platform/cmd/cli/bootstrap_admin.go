package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/platform/accountadmin"
)

func handleBootstrapPlatformAdmin(args []string) {
	initialized, err := bootstrapPlatformAdmin(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if initialized {
		fmt.Println("platform administrator initialized; sign in again to use the new role")
	} else {
		fmt.Println("platform administrator already initialized; no account changes made")
	}
}

func bootstrapPlatformAdmin(args []string) (bool, error) {
	if len(args) != 2 || args[0] != "--user-id" || strings.TrimSpace(args[1]) == "" || args[1] != strings.TrimSpace(args[1]) {
		return false, errors.New("usage: yuqing-cli bootstrap-platform-admin --user-id <verified immutable user ID>")
	}
	cfg, err := config.Load(configPath())
	if err != nil {
		return false, errors.New("bootstrap-platform-admin: unable to load configuration")
	}
	if cfg.Store.Driver != "postgres" || strings.TrimSpace(cfg.DB.Primary) == "" {
		return false, errors.New("bootstrap-platform-admin: PostgreSQL configuration is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DB.Primary)
	if err != nil {
		return false, errors.New("bootstrap-platform-admin: unable to connect to the platform database")
	}
	defer pool.Close()
	initialized, err := accountadmin.NewPGStore(pool).BootstrapPlatformAdmin(ctx, args[1])
	if err != nil {
		// Persistence errors may contain connection details or raw SQL. The CLI
		// only exposes this fixed message, including for missing/inactive users.
		return false, errors.New("bootstrap-platform-admin: initialization failed; verify the active user ID and initial administrator state")
	}
	return initialized, nil
}
