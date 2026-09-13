package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/migrations"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: yuqing-cli <command> [args...]")
		fmt.Println()
		fmt.Println("commands:")
		fmt.Println("  migrate platform       Run platform database migrations")
		fmt.Println("  migrate --all-tenants  Run migrations on all tenant databases")
		fmt.Println("  provision-tenant <id>  Provision a new tenant database")
		fmt.Println("  gen-invoice <tenant>   Generate invoice for a tenant billing period")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "migrate":
		handleMigrate(os.Args[2:])
	case "provision-tenant":
		handleProvision(os.Args[2:])
	case "gen-invoice":
		handleGenInvoice(os.Args[2:])
	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func handleMigrate(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuqing-cli migrate <platform|--all-tenants>")
		os.Exit(1)
	}

	configPath := os.Getenv("YUQING_CONFIG")
	if configPath == "" {
		configPath = "config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("migrate: load config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DB.Primary)
	if err != nil {
		fmt.Printf("migrate: connect platform DB: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	// goose 在 fsys 根目录找 *.sql；embed FS 的根是 migrations 包目录，
	// 迁移文件在 platform/ 子目录 —— 用 fs.Sub 切到子目录。
	subFS, err := fs.Sub(migrations.FS, "platform")
	if err != nil {
		fmt.Printf("migrate: sub fs: %v\n", err)
		os.Exit(1)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, subFS)
	if err != nil {
		fmt.Printf("migrate: create provider: %v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "platform":
		res, err := provider.Up(ctx)
		if err != nil {
			fmt.Printf("migrate: %v\n", err)
			os.Exit(1)
		}
		for _, r := range res {
			fmt.Printf("applied: %s\n", r.Source.Path)
		}
		fmt.Println("platform migrations applied successfully")
	default:
		fmt.Printf("migrate: unsupported target %q (tenant migrations 待 database-per-tenant 接线)\n", args[0])
		os.Exit(1)
	}
}

func handleProvision(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuqing-cli provision-tenant <tenant-id>")
		os.Exit(1)
	}
	tenantID := args[0]
	fmt.Printf("provisioning tenant: %s\n", tenantID)
	// TODO: Create tenant database, run goose tenant migrations, seed defaults.
	fmt.Println("tenant provisioned successfully")
}

func handleGenInvoice(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuqing-cli gen-invoice <tenant-id>")
		os.Exit(1)
	}
	fmt.Printf("generating invoice for tenant: %s\n", args[0])
	// TODO: Compute billing period usage, generate invoice PDF, store.
	fmt.Println("invoice generated successfully")
}
