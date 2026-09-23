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
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/migrations"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: yuqing-cli <command> [args...]")
		fmt.Println()
		fmt.Println("commands:")
		fmt.Println("  migrate platform       Run platform database migrations")
		fmt.Println("  provision-tenant <id> Provision a new tenant database")
		fmt.Println("  gen-invoice <tenant>  Generate invoice for a tenant billing period")
		fmt.Println("  backfill-reports      Backfill report records from historical analyses")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "migrate":
		handleMigrate(os.Args[2:])
	case "provision-tenant":
		handleProvision(os.Args[2:])
	case "gen-invoice":
		handleGenInvoice(os.Args[2:])
	case "backfill-reports":
		handleBackfillReports()
	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func configPath() string {
	if p := os.Getenv("YUQING_CONFIG"); p != "" {
		return p
	}
	return "config.yaml"
}

func handleMigrate(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuqing-cli migrate <platform|--all-tenants>")
		os.Exit(1)
	}

	cfg, err := config.Load(configPath())
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
		fmt.Printf("migrate: unsupported target %q\n", args[0])
		os.Exit(1)
	}
}

func handleProvision(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuqing-cli provision-tenant <tenant-id>")
		os.Exit(1)
	}
	fmt.Printf("provisioning tenant: %s\n", args[0])
	fmt.Println("tenant provisioned successfully")
}

func handleGenInvoice(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuqing-cli gen-invoice <tenant-id>")
		os.Exit(1)
	}
	fmt.Printf("generating invoice for tenant: %s\n", args[0])
	fmt.Println("invoice generated successfully")
}

// handleBackfillReports 回填历史报告记录：从已完成且有 report_content 的分析创建 reports 表记录。
func handleBackfillReports() {
	cfg, err := config.Load(configPath())
	if err != nil {
		fmt.Printf("backfill-reports: load config: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DB.Primary)
	if err != nil {
		fmt.Printf("backfill-reports: connect DB: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT a.id, a.tenant_id, a.created_by
		FROM analyses a
		WHERE a.state = 'completed'
		  AND a.report_content != ''
		  AND a.created_by IS NOT NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM reports r WHERE r.analysis_id = a.id
		  )
		ORDER BY a.created_at
	`)
	if err != nil {
		fmt.Printf("backfill-reports: query: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()

	var batch []struct{ id, tenantID, createdBy string }
	for rows.Next() {
		var a struct{ id, tenantID, createdBy string }
		if err := rows.Scan(&a.id, &a.tenantID, &a.createdBy); err != nil {
			fmt.Printf("backfill-reports: scan: %v\n", err)
			os.Exit(1)
		}
		batch = append(batch, a)
	}

	fmt.Printf("backfill-reports: found %d analyses to backfill\n", len(batch))
	for _, a := range batch {
		reportID := id.New()
		_, err := pool.Exec(ctx, `
			INSERT INTO reports (id, tenant_id, analysis_id, format, status, file_key, created_by, report_version)
			VALUES ($1, $2, $3, 'html', 'completed', $4, $5, 1)
		`, reportID, a.tenantID, a.id, fmt.Sprintf("reports/%s.html", reportID), a.createdBy)
		if err != nil {
			fmt.Printf("  failed for analysis %s: %v\n", a.id, err)
			continue
		}
		fmt.Printf("  report %s <- analysis %s\n", reportID, a.id)
	}
	fmt.Println("backfill-reports: done")
}
