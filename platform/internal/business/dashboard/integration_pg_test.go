package dashboard

import (
	"context"
	"os"
	"testing"

	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/id"
)

// TestDashboard_PostgreSQL_CreatedByColumn tests that dashboard queries work
// correctly with the created_by column added in migration 0008.
//
// This integration test verifies schema compatibility by executing queries
// that reference created_by, ensuring the column exists and is queryable.
//
// Run with: YUQING_TEST_PG_URL=postgres://... go test -run TestDashboard_PostgreSQL_CreatedByColumn
func TestDashboard_PostgreSQL_CreatedByColumn(t *testing.T) {
	pgURL := os.Getenv("YUQING_TEST_PG_URL")
	if pgURL == "" {
		t.Skip("YUQING_TEST_PG_URL not set, skipping PostgreSQL integration test")
	}

	pool := pgtest.Pool(t, "dashboard_integration", pgtest.PlatformMigrations)
	ctx := context.Background()

	// Create test tenant and user
	tenantID := "t-" + id.New()
	userID := "u-" + id.New()

	// Insert test user
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash)
		VALUES ($1, $2, $3)
	`, userID, "test@example.com", "hash")
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	// Insert test tenant
	_, err = pool.Exec(ctx, `
		INSERT INTO tenants (id, name, plan_code, status)
		VALUES ($1, $2, $3, $4)
	`, tenantID, "Test Tenant", "free", "active")
	if err != nil {
		t.Fatalf("failed to create test tenant: %v", err)
	}

	// Insert test analysis with created_by
	analysisID := "a-" + id.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO analyses (id, tenant_id, keywords, type, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, analysisID, tenantID, `["test"]`, "quick_scan", "completed", userID)
	if err != nil {
		t.Fatalf("failed to create analysis: %v", err)
	}

	// Test: Query analyses with created_by (dashboard overview uses this)
	var count int
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM analyses WHERE tenant_id = $1 AND created_by = $2
	`, tenantID, userID).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query analyses by created_by: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count=1, got %d", count)
	}

	// Test: Read created_by column
	var retrievedUserID string
	err = pool.QueryRow(ctx, `
		SELECT created_by FROM analyses WHERE id = $1
	`, analysisID).Scan(&retrievedUserID)
	if err != nil {
		t.Fatalf("failed to read created_by: %v", err)
	}
	if retrievedUserID != userID {
		t.Errorf("expected created_by=%s, got %s", userID, retrievedUserID)
	}

	// Cleanup
	_, err = pool.Exec(ctx, `DELETE FROM analyses WHERE tenant_id = $1`, tenantID)
	if err != nil {
		t.Errorf("cleanup failed: %v", err)
	}
}
