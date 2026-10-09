package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
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
var _ RegistrationStore = (*PGStore)(nil)
var _ AuthorizationStateStore = (*PGStore)(nil)
var _ LoginRecorder = (*PGStore)(nil)

// PlatformAdminLockID is shared with account administration transactions so
// initial seeding cannot race an explicit grant, revocation or suspension.
const PlatformAdminLockID int64 = 741914

type authExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func (s *PGStore) RegisterAccount(ctx context.Context, user User, tenant Tenant, member Member, bootstrap bool) error {
	if err := validateRegistration(user, tenant, member); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "begin registration")
	}
	defer tx.Rollback(context.Background())
	if err := insertUser(ctx, tx, user); err != nil {
		return err
	}
	if err := insertTenant(ctx, tx, tenant); err != nil {
		return err
	}
	if err := insertMember(ctx, tx, member); err != nil {
		return err
	}
	if bootstrap {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, PlatformAdminLockID); err != nil {
			return wrapDB(err, "lock initial administrator")
		}
		// This one-time registration seed is the only use of the configured
		// bootstrap email. Existing accounts are migrated by explicit user ID.
		if _, err := tx.Exec(ctx, `INSERT INTO platform_user_roles (user_id, role)
			SELECT $1, 'platform_admin' WHERE NOT EXISTS (SELECT 1 FROM platform_user_roles)`, user.ID); err != nil {
			return wrapDB(err, "persist initial administrator")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "commit registration")
	}
	return nil
}

// LoadAuthorizationState uses one PostgreSQL statement and one MVCC snapshot.
// The tenant join is fixed to the token's tid even when membership was removed.
func (s *PGStore) LoadAuthorizationState(ctx context.Context, userID, tenantID string) (*AuthorizationState, error) {
	const q = `SELECT u.id, u.email, u.status, u.token_version,
		ARRAY(SELECT pur.role FROM platform_user_roles pur WHERE pur.user_id = u.id ORDER BY pur.role),
		COALESCE(t.status, ''), COALESCE(t.plan_code, ''),
		COALESCE(tm.role, ''), tm.user_id IS NOT NULL
		FROM users u
		LEFT JOIN tenants t ON t.id = $2
		LEFT JOIN tenant_members tm ON tm.tenant_id = t.id AND tm.user_id = u.id
		WHERE u.id = $1`
	state := &AuthorizationState{TenantID: tenantID}
	err := s.pool.QueryRow(ctx, q, userID, tenantID).Scan(
		&state.UserID, &state.Email, &state.UserStatus, &state.TokenVersion,
		&state.PlatformRoles, &state.TenantStatus, &state.PlanCode, &state.MemberRole, &state.MemberExists)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, wrapDB(err, "load authorization state")
	}
	return state, nil
}

func (s *PGStore) RecordSuccessfulLogin(ctx context.Context, userID string, version int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at = now()
		WHERE id = $1 AND status = 'active' AND token_version = $2`, userID, version)
	if err != nil {
		return wrapDB(err, "record successful login")
	}
	if tag.RowsAffected() != 1 {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "account or credential revoked")
	}
	return nil
}

// CreateUser inserts a user row.
//
// 内存版按 email 判重，PG 版另有主键约束：重复 email 与重复 ID 都是
// ErrConflict，但错误消息区分得开（靠约束名，见下）。
func (s *PGStore) CreateUser(ctx context.Context, u User) error {
	return insertUser(ctx, s.pool, u)
}

func insertUser(ctx context.Context, executor authExecutor, u User) error {
	u = accountUserDefaults(u)
	const q = `INSERT INTO users (id, email, password_hash, name, status, created_at, token_version, row_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	if _, err := executor.Exec(ctx, q, u.ID, u.Email, u.PasswordHash, u.Name, u.Status, u.CreatedAt, u.TokenVersion, u.RowVersion); err != nil {
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
	const q = `SELECT ` + userCenterColumns + ` FROM users WHERE email = $1`
	u, err := scanUserCenter(s.pool.QueryRow(ctx, q, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, wrapDB(err, "get user by email")
	}
	return u, nil
}

// CreateTenant inserts a tenant row. slug 与 db_name 也带唯一约束，
// 冲突一律 ErrConflict（内存版只能检查 ID）。
func (s *PGStore) CreateTenant(ctx context.Context, t Tenant) error {
	return insertTenant(ctx, s.pool, t)
}

func insertTenant(ctx context.Context, executor authExecutor, t Tenant) error {
	const q = `INSERT INTO tenants (id, name, slug, db_name, status, plan_code)
	           VALUES ($1, $2, $3, $4, $5, $6)`

	if _, err := executor.Exec(ctx, q, t.ID, t.Name, t.Slug, t.DBName, t.Status, t.PlanCode); err != nil {
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
	return insertMember(ctx, s.pool, m)
}

func insertMember(ctx context.Context, executor authExecutor, m Member) error {
	const q = `INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, $3)`

	if _, err := executor.Exec(ctx, q, m.TenantID, m.UserID, m.Role); err != nil {
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
		// Login can still resolve an active account without a tenant; tenant
		// routes separately require an existing membership and active tenant.
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
// 身份存储不可用时采用 503；驱动细节保留在内部错误，HTTP 信封统一屏蔽。
func wrapDB(err error, op string) error {
	return pkgerrors.Wrap(pkgerrors.ErrServiceUnavailable, "auth: "+op+": "+err.Error())
}
