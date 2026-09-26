package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// validateSchema checks that critical database columns exist before starting the server.
// This provides fail-fast behavior when schema migrations haven't been applied.
func validateSchema(pool *pgxpool.Pool) error {
	if pool == nil {
		return nil
	}

	ctx := context.Background()

	// Check analyses.created_by column (added in migration 0008)
	var analysesCreatedBy bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'analyses'
			AND column_name = 'created_by'
		)
	`).Scan(&analysesCreatedBy)
	if err != nil {
		return fmt.Errorf("failed to check analyses.created_by: %w", err)
	}
	if !analysesCreatedBy {
		return fmt.Errorf("analyses.created_by column does not exist - run migration 0008")
	}

	// Check reports.created_by column (added in migration 0008)
	var reportsCreatedBy bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'reports'
			AND column_name = 'created_by'
		)
	`).Scan(&reportsCreatedBy)
	if err != nil {
		return fmt.Errorf("failed to check reports.created_by: %w", err)
	}
	if !reportsCreatedBy {
		return fmt.Errorf("reports.created_by column does not exist - run migration 0008")
	}

	return nil
}
