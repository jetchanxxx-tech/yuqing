package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"os"
	"strings"
	"time"
)

func handleBindBillingExemptAdmin(args []string) {
	if (len(args) != 2 && len(args) != 3) || args[0] != "--expected-user-id" || strings.TrimSpace(args[1]) == "" || (len(args) == 3 && args[2] != "--apply") {
		fmt.Fprintln(os.Stderr, "usage: yuqing-cli bind-billing-exempt-admin --expected-user-id <verified immutable user ID> [--apply]")
		os.Exit(1)
	}
	cfg, err := config.Load(configPath())
	if err != nil || cfg.Store.Driver != "postgres" {
		fmt.Fprintln(os.Stderr, "billing binding: PostgreSQL configuration required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DB.Primary)
	if err != nil {
		fmt.Fprintln(os.Stderr, "billing binding: database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	apply := len(args) == 3
	changed, err := billingpolicy.NewService(pool).Bind(ctx, args[1], apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "billing binding: verification failed or immutable policy conflicts")
		os.Exit(1)
	}
	if !apply {
		fmt.Println("billing binding precheck passed; no changes made")
	} else if changed {
		fmt.Println("billing exemption bound to verified immutable user ID")
	} else {
		fmt.Println("billing exemption already bound to this immutable user ID")
	}
}
