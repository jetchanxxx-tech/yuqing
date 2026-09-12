package tenant

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuging/platform/internal/pkg/db"
	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

// PGStore is the PostgreSQL-backed tenant Store against the platform database
// (table: tenants — see migrations/platform/0001_init.sql).
//
// 只做 CRUD：状态机校验（Suspend/Resume 允许的迁移）留在 Service，
// 与内存版的职责划分一致 —— 状态机是业务规则，不该下沉到存储。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore creates a tenant store over an existing platform pool.
// The pool is owned by the caller (the composition root), not by the store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

var _ Store = (*PGStore)(nil)

// Create inserts a tenant; duplicate ID (or slug/db_name) conflict.
func (s *PGStore) Create(ctx context.Context, t Tenant) error {
	const q = `INSERT INTO tenants (id, name, slug, db_name, status, plan_code)
	           VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := s.pool.Exec(ctx, q, t.ID, t.Name, t.Slug, t.DBName, string(t.Status), t.PlanCode)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "tenant already exists")
		}
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "tenant: create: "+err.Error())
	}
	return nil
}

// Get returns one tenant.
func (s *PGStore) Get(ctx context.Context, id string) (*Tenant, error) {
	const q = `SELECT id, name, slug, db_name, status, plan_code FROM tenants WHERE id = $1`

	var (
		t      Tenant
		status string
	)
	err := s.pool.QueryRow(ctx, q, id).Scan(&t.ID, &t.Name, &t.Slug, &t.DBName, &status, &t.PlanCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "tenant not found")
	}
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "tenant: get: "+err.Error())
	}
	t.Status = Status(status)
	return &t, nil
}

// List returns all tenants in creation order.
//
// 内存版按插入顺序；PG 版按 created_at，同一事务/毫秒内建的行用 ULID 主键
// 兜底。ULID 高位即时间戳，所以 (created_at, id) 与插入顺序一致，分页与
// 前端渲染都稳定可复现（不带 ORDER BY 的 SELECT 无顺序保证）。
func (s *PGStore) List(ctx context.Context) ([]Tenant, error) {
	const q = `SELECT id, name, slug, db_name, status, plan_code
	           FROM tenants ORDER BY created_at, id`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "tenant: list: "+err.Error())
	}
	defer rows.Close()

	// 永远返回非 nil 切片：JSON 里是 [] 而不是 null。
	out := make([]Tenant, 0)
	for rows.Next() {
		var (
			t      Tenant
			status string
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.DBName, &status, &t.PlanCode); err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "tenant: list scan: "+err.Error())
		}
		t.Status = Status(status)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "tenant: list rows: "+err.Error())
	}
	return out, nil
}

// UpdateStatus changes a tenant's lifecycle status.
//
// 影响的不是 0 行就是 1 行（主键），因此 RowsAffected 为 0 即「租户不存在」——
// 不必先 SELECT 再 UPDATE，省一次往返且天然原子。
func (s *PGStore) UpdateStatus(ctx context.Context, id string, status Status) error {
	const q = `UPDATE tenants SET status = $2 WHERE id = $1`

	tag, err := s.pool.Exec(ctx, q, id, string(status))
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "tenant: update status: "+err.Error())
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "tenant not found")
	}
	return nil
}
