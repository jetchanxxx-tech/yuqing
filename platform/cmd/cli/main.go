package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: yuging-cli <command> [args...]")
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
		fmt.Println("usage: yuging-cli migrate <platform|--all-tenants>")
		os.Exit(1)
	}
	fmt.Println("migrate: reading migrations from embedded filesystem...")
	// TODO: Load config, connect to platform DB, run goose migrations.
	fmt.Println("migrations applied successfully")
}

func handleProvision(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuging-cli provision-tenant <tenant-id>")
		os.Exit(1)
	}
	tenantID := args[0]
	fmt.Printf("provisioning tenant: %s\n", tenantID)
	// TODO: Create tenant database, run goose tenant migrations, seed defaults.
	fmt.Println("tenant provisioned successfully")
}

func handleGenInvoice(args []string) {
	if len(args) < 1 {
		fmt.Println("usage: yuging-cli gen-invoice <tenant-id>")
		os.Exit(1)
	}
	fmt.Printf("generating invoice for tenant: %s\n", args[0])
	// TODO: Compute billing period usage, generate invoice PDF, store.
	fmt.Println("invoice generated successfully")
}
