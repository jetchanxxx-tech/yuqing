package db

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// mockPoolFactory creates pools that don't connect to a real database.
// pgxpool.New won't work without PG running, so we use a helper config
// that delays connection or use a test helper.
//
// For unit tests we validate config, naming, caching, and split logic —
// integration tests with a real PG are in a separate file (manager_integration_test.go).

func TestTenantDBName_mapsTenantToPhysicalDB(t *testing.T) {
	if got := TenantDBName("abc123"); got != "yuqing_t_abc123" {
		t.Errorf("TenantDBName = %q, want yuqing_t_abc123", got)
	}
}

func TestApplyDefaults_setsAll(t *testing.T) {
	cfg := &ManagerConfig{PlatformDSN: "postgres://x"}
	applyDefaults(cfg)

	if cfg.MaxConns != 20 {
		t.Errorf("MaxConns = %d, want 20", cfg.MaxConns)
	}
	if cfg.TenantPoolMaxConns != 8 {
		t.Errorf("TenantPoolMaxConns = %d, want 8", cfg.TenantPoolMaxConns)
	}
	if cfg.TenantMaxPools != 50 {
		t.Errorf("TenantMaxPools = %d, want 50", cfg.TenantMaxPools)
	}
}

func TestManagerConfig_nonZeroValues_preserved(t *testing.T) {
	cfg := &ManagerConfig{
		PlatformDSN:        "postgres://x",
		MaxConns:           99,
		TenantPoolMaxConns: 16,
		TenantMaxPools:     5,
	}
	applyDefaults(cfg)

	if cfg.MaxConns != 99 {
		t.Errorf("non-zero MaxConns was overwritten: got %d", cfg.MaxConns)
	}
	if cfg.TenantPoolMaxConns != 16 {
		t.Errorf("non-zero TenantPoolMaxConns was overwritten")
	}
	if cfg.TenantMaxPools != 5 {
		t.Errorf("non-zero TenantMaxPools was overwritten")
	}
}

func TestManager_Platform(t *testing.T) {
	// Use a mock factory that returns a pool from a DSN that
	// never actually connects (empty pool config still creates the object).
	// pgxpool.New with empty config will fail, so we use a known-failing DSN
	// and expect the error to propagate through the factory.
	//
	// For the unit test: test that Platform() returns the pool passed at construction.

	// Since we can't create a real pgxpool without PG, we verify the error path.
	cfg := ManagerConfig{
		PlatformDSN: "postgres://nonexistent:5432/db",
		PoolFactory: func(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
			return nil, fmt.Errorf("mock: cannot connect")
		},
	}
	_, err := NewManager(cfg)
	if err == nil {
		t.Fatal("expected error from mock factory")
	}
}

func TestManager_TenantIDIsolation(t *testing.T) {
	// Test that tenant IDs produce different DB names.
	alice := TenantDBName("alice")
	bob := TenantDBName("bob")
	if alice == bob {
		t.Fatal("different tenant IDs must produce different DB names")
	}
	if alice != "yuqing_t_alice" {
		t.Errorf("alice = %q", alice)
	}
	if bob != "yuqing_t_bob" {
		t.Errorf("bob = %q", bob)
	}
}

// TestPoolFactory is a helper that creates a pool from a test DSN.
type testFactory struct {
	pools map[string]*pgxpool.Pool
}

func TestReadWriteSplit_disabled(t *testing.T) {
	// When ReadWriteSplit=false, Read() must return the primary pool.
	// Test this at the config level: pickReplica returns nil.
	m := &manager{
		cfg: ManagerConfig{ReadWriteSplit: false},
	}
	if p := m.pickReplica(); p != nil {
		t.Error("pickReplica should return nil when ReadWriteSplit=false")
	}
}

func TestReadWriteSplit_enabled_noReplicas(t *testing.T) {
	m := &manager{
		cfg:          ManagerConfig{ReadWriteSplit: true},
		replicaPools: nil,
	}
	if p := m.pickReplica(); p != nil {
		t.Error("pickReplica should return nil when no replicas configured")
	}
}

func TestManagerConfig_MaxPoolsEviction(t *testing.T) {
	// Test eviction behavior: when max pools reached, oldest is removed.
	// We verify the config is set correctly; eviction is tested in integration.
	cfg := &ManagerConfig{
		PlatformDSN:    "postgres://x",
		TenantMaxPools: 1,
	}
	applyDefaults(cfg)
	if cfg.TenantMaxPools != 1 {
		t.Errorf("TenantMaxPools = %d, want 1", cfg.TenantMaxPools)
	}
}

func TestMain(m *testing.M) {
	// Skip tests that require PG when no DB is available.
	// Individual tests handle this via mock factory.
	os.Exit(m.Run())
}
