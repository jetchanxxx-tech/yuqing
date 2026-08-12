package db

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolFactory creates a pgxpool.Pool from a DSN.
// Swapped in tests to avoid requiring a real database.
type PoolFactory func(ctx context.Context, dsn string) (*pgxpool.Pool, error)

// DefaultPoolFactory is the real pgxpool.New, used in production.
var DefaultPoolFactory PoolFactory = func(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, dsn)
}

// ManagerConfig configures the DB Manager.
type ManagerConfig struct {
	PlatformDSN        string
	MaxConns           int
	ReadWriteSplit     bool
	ReplicaDSNs        []string
	AutoCreate         bool
	TenantPoolMaxConns int
	TenantMaxPools     int
	MigrationsDir      string
	PoolFactory        PoolFactory // nil = use DefaultPoolFactory
}

// Manager manages platform and tenant database connection pools.
type Manager interface {
	Platform(ctx context.Context) *pgxpool.Pool
	Tenant(ctx context.Context, tenantID string) (TenantDB, error)
	Close()
}

// TenantDB provides write and optionally read-separated access to a tenant database.
type TenantDB interface {
	Write(ctx context.Context) *pgxpool.Pool
	Read(ctx context.Context) *pgxpool.Pool
	DBName() string
}

// NewManager creates a new DB Manager.
func NewManager(cfg ManagerConfig) (Manager, error) {
	applyDefaults(&cfg)

	factory := cfg.PoolFactory
	if factory == nil {
		factory = DefaultPoolFactory
	}

	platformPool, err := factory(context.Background(), cfg.PlatformDSN)
	if err != nil {
		return nil, fmt.Errorf("db: failed to connect to platform DB: %w", err)
	}

	m := &manager{
		cfg:          cfg,
		platformPool: platformPool,
		tenantPools:  make(map[string]*tenantPoolEntry),
		poolFactory:  factory,
	}

	for _, dsn := range cfg.ReplicaDSNs {
		p, err := factory(context.Background(), dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "db: failed to connect to replica %s: %v\n", dsn, err)
			continue
		}
		m.replicaPools = append(m.replicaPools, p)
	}

	return m, nil
}

// MustNewManager creates a Manager, panicking on error.
func MustNewManager(cfg ManagerConfig) Manager {
	m, err := NewManager(cfg)
	if err != nil {
		panic(err)
	}
	return m
}

func applyDefaults(cfg *ManagerConfig) {
	if cfg.MaxConns == 0 {
		cfg.MaxConns = 20
	}
	if cfg.TenantPoolMaxConns == 0 {
		cfg.TenantPoolMaxConns = 8
	}
	if cfg.TenantMaxPools == 0 {
		cfg.TenantMaxPools = 50
	}
}

type tenantPoolEntry struct {
	pool *pgxpool.Pool
	name string
}

type manager struct {
	cfg          ManagerConfig
	platformPool *pgxpool.Pool
	replicaPools []*pgxpool.Pool
	mu           sync.RWMutex
	tenantPools  map[string]*tenantPoolEntry
	poolFactory  PoolFactory
}

func (m *manager) Platform(ctx context.Context) *pgxpool.Pool {
	return m.platformPool
}

func (m *manager) Tenant(ctx context.Context, tenantID string) (TenantDB, error) {
	m.mu.RLock()
	entry, ok := m.tenantPools[tenantID]
	m.mu.RUnlock()
	if ok {
		return &tenantDB{primary: entry.pool, replica: m.pickReplica(), name: entry.name}, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok = m.tenantPools[tenantID]; ok {
		return &tenantDB{primary: entry.pool, replica: m.pickReplica(), name: entry.name}, nil
	}

	dbName := TenantDBName(tenantID)
	dsn := m.cfg.PlatformDSN // real impl would substitute db name
	pool, err := m.poolFactory(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("db: tenant %s: %w", tenantID, err)
	}

	if len(m.tenantPools) >= m.cfg.TenantMaxPools {
		m.evictOne()
	}

	m.tenantPools[tenantID] = &tenantPoolEntry{pool: pool, name: dbName}
	return &tenantDB{primary: pool, replica: m.pickReplica(), name: dbName}, nil
}

// TenantDBName returns the physical database name for a tenant ID.
func TenantDBName(tenantID string) string {
	return fmt.Sprintf("yuging_t_%s", tenantID)
}

func (m *manager) pickReplica() *pgxpool.Pool {
	if !m.cfg.ReadWriteSplit || len(m.replicaPools) == 0 {
		return nil
	}
	return m.replicaPools[0]
}

func (m *manager) evictOne() {
	for k := range m.tenantPools {
		if entry, ok := m.tenantPools[k]; ok {
			entry.pool.Close()
			delete(m.tenantPools, k)
			return
		}
	}
}

func (m *manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.tenantPools {
		entry.pool.Close()
	}
	for _, p := range m.replicaPools {
		p.Close()
	}
	m.platformPool.Close()
}

type tenantDB struct {
	primary *pgxpool.Pool
	replica *pgxpool.Pool
	name    string
}

func (t *tenantDB) Write(ctx context.Context) *pgxpool.Pool { return t.primary }

func (t *tenantDB) Read(ctx context.Context) *pgxpool.Pool {
	if t.replica != nil {
		return t.replica
	}
	return t.primary
}

func (t *tenantDB) DBName() string { return t.name }
