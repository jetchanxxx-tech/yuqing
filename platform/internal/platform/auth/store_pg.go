package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuging/platform/internal/pkg/db"
	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

// PGStore is the PostgreSQL-backed auth Store against the platform database
// (tables: users, tenants, tenant_members — see migrations/platform/0001_init.sql).
//
// 与 MemoryStore 的行为契约一致（见 store_pg_test.go 中参数化的契约测试）：
// 重复行 → ErrConflict，未找到 → ErrNotFound。差别只在外键与 CITEXT 这类
// 内存版无法表达、由数据库强制的约束，逐条记在下面的方法注释里。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore creates an auth store over an existing platform pool.
// The pool is owned by the caller (the composition root), not by the store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

var _ Store = (*PGStore)(nil)

// CreateUser inserts a user row.
//
// 内存版按 email 判重，PG 版另有主键约束：重复 email 与重复 ID 都是
// ErrConflict，但错误消息区分得开（靠约束名，见下）。
func (s *PGStore) CreateUser(ctx context.Context, u User) error {
	const q = `INSERT INTO users (id, email, password_hash, name) VALUES ($1, $2, $3, $4)`

	if _, err := s.pool.Exec(ctx, q, u.ID, u.Email, u.PasswordHash, u.Name); err != nil {
		if db.IsUniqueViolation(err) {
			if db.ViolatedConstraint(err) == "users_email_key" {
				return pkgerrors.Wrap(pkgerrors.ErrConflict, "email already registered")
			}
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "user already exists")
		}
		return wrapDB(err, "create user")
	}
	return nil
}

// GetUserByEmail finds a user by email. email 是 CITEXT 列：查询天然大小写
// 不敏感（内存版是精确匹配，大小写规范化由 service 层完成）。
func (s *PGStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	const q = `SELECT id, email, password_hash, name FROM users WHERE email = $1`

	var u User
	err := s.pool.QueryRow(ctx, q, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, wrapDB(err, "get user by email")
	}
	return &u, nil
}

// CreateTenant inserts a tenant row. slug 与 db_name 也带唯一约束，
// 冲突一律 ErrConflict（内存版只能检查 ID）。
func (s *PGStore) CreateTenant(ctx context.Context, t Tenant) error {
	const q = `INSERT INTO tenants (id, name, slug, db_name, status, plan_code)
	           VALUES ($1, $2, $3, $4, $5, $6)`

	if _, err := s.pool.Exec(ctx, q, t.ID, t.Name, t.Slug, t.DBName, t.Status, t.PlanCode); err != nil {
		if db.IsUniqueViolation(err) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "tenant already exists")
		}
		return wrapDB(err, "create tenant")
	}
	return nil
}

// CreateMember binds a user to a tenant.
//
// 外键差异：tenant_members 对 tenants/users 都有外键，引用不存在的行时
// PostgreSQL 拒绝写入，这里映射为 ErrNotFound；内存版没有外键，会存下一个
// 永远解析不出租户的孤儿成员。PG 版更严格，是更好的行为。
func (s *PGStore) CreateMember(ctx context.Context, m Member) error {
	const q = `INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, $3)`

	if _, err := s.pool.Exec(ctx, q, m.TenantID, m.UserID, m.Role); err != nil {
		switch {
		case db.IsUniqueViolation(err):
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "membership already exists")
		case db.IsForeignKeyViolation(err):
			return pkgerrors.Wrap(pkgerrors.ErrNotFound, "tenant or user not found")
		}
		return wrapDB(err, "create member")
	}
	return nil
}

// GetUserTenant returns the tenant the user belongs to (login principal).
//
// 内存版记录「最后一次 CreateMember 的租户」；PG 版按租户建立时间取最早的
// 一个（created_at 并列时用 ULID 主键兜底），保证同一用户多租户时结果稳定
// 可复现 —— 注册流程只建一个成员关系，两者在此实际等价。
func (s *PGStore) GetUserTenant(ctx context.Context, userID string) (*Tenant, error) {
	const q = `SELECT t.id, t.name, t.slug, t.db_name, t.status, t.plan_code
	           FROM tenant_members tm
	           JOIN tenants t ON t.id = tm.tenant_id
	           WHERE tm.user_id = $1
	           ORDER BY t.created_at, t.id
	           LIMIT 1`

	var t Tenant
	err := s.pool.QueryRow(ctx, q, userID).
		Scan(&t.ID, &t.Name, &t.Slug, &t.DBName, &t.Status, &t.PlanCode)
	if errors.Is(err, pgx.ErrNoRows) {
		// 无成员关系，或成员关系指向的租户已被删除（INNER JOIN 两种情况
		// 都是零行）。内存版分别报 "membership not found"/"tenant not found"，
		// 登录链路对两者都返回 ErrUnauthorized，故此处统一。
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "membership not found")
	}
	if err != nil {
		return nil, wrapDB(err, "get user tenant")
	}
	return &t, nil
}

// GetUserRole returns the user's role inside a tenant.
func (s *PGStore) GetUserRole(ctx context.Context, tenantID, userID string) (string, error) {
	const q = `SELECT role FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`

	var role string
	err := s.pool.QueryRow(ctx, q, tenantID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", pkgerrors.Wrap(pkgerrors.ErrNotFound, "membership not found")
	}
	if err != nil {
		return "", wrapDB(err, "get user role")
	}
	return role, nil
}

// wrapDB 把非业务性的数据库错误（连接断开、超时、约束之外的 SQL 错误）
// 统一包成 ErrInternal，避免把驱动错误直接漏到 HTTP 信封里。
func wrapDB(err error, op string) error {
	return pkgerrors.Wrap(pkgerrors.ErrInternal, "auth: "+op+": "+err.Error())
}
