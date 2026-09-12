package apikey

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuging/platform/internal/pkg/db"
	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/id"
)

// PGStore is the PostgreSQL-backed API key store against the platform
// database (table: api_keys — see migrations/platform/0001_init.sql).
//
// 三个必须知道的表结构限制（迁移文件非本轮改动范围，均在代码层处理）：
//
//  1. 没有 created_at 列 —— CreatedAt 按 ULID 主键的时间戳还原（id.Time）。
//     写入时调用方传入的 CreatedAt 被忽略；主键不是 ULID 的历史行降级为零值。
//  2. key_hash 没有唯一约束 —— 无法靠数据库拒绝重复哈希，改在 Create 里
//     先查后插（见该方法注释里的并发窗口说明）。
//  3. 没有显示用前缀列 —— 见 migrations/platform/0002_pg_store_support.sql
//     新增的 api_keys.prefix；缺失时下面的查询会直接报列不存在（故意不静默降级，
//     让漏跑迁移立刻暴露，而不是让前端悄悄显示不出前缀）。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore creates an API key store over an existing platform pool.
// The pool is owned by the caller (the composition root), not by the store.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

var _ Store = (*PGStore)(nil)

// apiKeyColumns 是全部读取路径共用的列清单。
//
// scopes / prefix 用 COALESCE 兜底，让扫描目标永远不是数据库 NULL：
// 标量 null 的 JSONB 反序列化后恰好得到 nil 切片，与内存版「未设置 scopes
// 就是 nil」一致；同时也避免依赖驱动对 NULL→[]byte 的处理细节。
const apiKeyColumns = `id, tenant_id, name, key_hash,
	COALESCE(scopes, 'null'::jsonb), COALESCE(prefix, ''), last_used_at, revoked_at`

// Create inserts a key.
//
// 重复 ID 由主键约束拒绝；重复 key_hash 表上没有约束，因此用一条
// 带 NOT EXISTS 守卫的 INSERT 在代码层判重 —— 同一把钥匙不允许对应两行，
// 否则 GetByHash 二义，认证链路会出现「同一密钥映射到两个租户」。
//
// 并发窗口：两个事务同时插入同一哈希时都可能通过 NOT EXISTS 检查
// （READ COMMITTED 下彼此不可见）。彻底修法是在 api_keys.key_hash 上加唯一
// 索引；本轮不改迁移，故保留该窗口 —— 实际触发需要两个进程同时用同一把
// 明文 key 建密钥，而 key 由服务端生成（raw = pangu_ + ULID），碰撞概率可忽略。
func (s *PGStore) Create(ctx context.Context, k *APIKey) error {
	// 参数一律显式转型：INSERT ... SELECT 的目标列类型不会反向推断出
	// SELECT 里裸参数的型别，显式 ::text / ::jsonb 让语句在任何 PostgreSQL
	// 版本上都能通过 Parse 阶段的参数类型检查。
	const q = `INSERT INTO api_keys (id, tenant_id, name, key_hash, scopes, prefix)
	           SELECT $1::text, $2::text, $3::text, $4::text, $5::jsonb, $6::text
	           WHERE NOT EXISTS (SELECT 1 FROM api_keys WHERE key_hash = $4::text)
	             AND NOT EXISTS (SELECT 1 FROM api_keys WHERE id = $1::text)
	           RETURNING id`

	scopes, err := marshalScopes(k.Scopes)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: create: "+err.Error())
	}

	var inserted string
	err = s.pool.QueryRow(ctx, q, k.ID, k.TenantID, k.Name, k.keyHash, scopes, k.Prefix).Scan(&inserted)
	if err == nil {
		return nil
	}
	// 并发插入同一 ID 时守卫查不到、主键约束兜住 —— 仍是 ErrConflict。
	if db.IsUniqueViolation(err) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "api key already exists")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: create: "+err.Error())
	}

	// 零行 = 被守卫拦下，回查一次区分是 ID 冲突还是哈希冲突（只在错误路径上多发一次查询）。
	var exists bool
	if qerr := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM api_keys WHERE key_hash = $1)`, k.keyHash).Scan(&exists); qerr != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: create: "+qerr.Error())
	}
	if exists {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "api key hash already exists")
	}
	return pkgerrors.Wrap(pkgerrors.ErrConflict, "api key already exists")
}

// List returns the tenant's keys in creation order. 没有 created_at 列，
// 用 ULID 主键排序等价于创建顺序（高位即时间戳），且结果确定可复现。
func (s *PGStore) List(ctx context.Context, tenantID string) ([]*APIKey, error) {
	q := `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE tenant_id = $1 ORDER BY id`

	rows, err := s.pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: list: "+err.Error())
	}
	defer rows.Close()

	// 永远返回非 nil 切片：JSON 里是 [] 而不是 null。
	out := make([]*APIKey, 0)
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: list scan: "+err.Error())
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: list rows: "+err.Error())
	}
	return out, nil
}

// Revoke stamps revoked_at, scoped to the tenant: foreign or unknown ids are
// indistinguishable ErrNotFound (no cross-tenant existence leak).
//
// COALESCE 保证幂等：重复撤销保留首次撤销时间，与内存版一致
// （内存版仅在 RevokedAt == nil 时赋值）。
func (s *PGStore) Revoke(ctx context.Context, tenantID, id string) error {
	const q = `UPDATE api_keys SET revoked_at = COALESCE(revoked_at, now())
	           WHERE id = $1 AND tenant_id = $2`

	tag, err := s.pool.Exec(ctx, q, id, tenantID)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: revoke: "+err.Error())
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	return nil
}

// GetByHash returns the key whose hash matches (any tenant), or ErrNotFound.
//
// 哈希在 Create 处已判重，正常数据下最多一行；ORDER BY id DESC 只是让
// 「迁移前已存在的重复哈希行」确定性地取最新一条，而不是随机取。
func (s *PGStore) GetByHash(ctx context.Context, hash string) (*APIKey, error) {
	q := `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE key_hash = $1 ORDER BY id DESC LIMIT 1`

	k, err := scanAPIKey(s.pool.QueryRow(ctx, q, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: get by hash: "+err.Error())
	}
	return k, nil
}

// Touch records a last-use timestamp on the key.
func (s *PGStore) Touch(ctx context.Context, id string, at time.Time) error {
	const q = `UPDATE api_keys SET last_used_at = $2 WHERE id = $1`

	tag, err := s.pool.Exec(ctx, q, id, at)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "apikey: touch: "+err.Error())
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	return nil
}

// rowScanner 覆盖 QueryRow 与 Rows 两种取值方式。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanAPIKey 读一行 key，并把 key_hash 回填到不导出的 keyHash 字段：
// 结构体在内存里始终带着自己的哈希（不参与 JSON 序列化），
// 内存版与 PG 版因此可以逐字段比对（见契约测试）。
func scanAPIKey(row rowScanner) (*APIKey, error) {
	var (
		k      APIKey
		scopes string // JSONB 文本（COALESCE 保证非 NULL）
		hashed string
		prefix string
	)

	if err := row.Scan(&k.ID, &k.TenantID, &k.Name, &hashed, &scopes, &prefix, &k.LastUsedAt, &k.RevokedAt); err != nil {
		return nil, err
	}

	k.Prefix = prefix
	k.keyHash = hashed

	if scopes != "" {
		if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
			return nil, err
		}
	}
	if ts, ok := id.Time(k.ID); ok {
		k.CreatedAt = ts
	}
	return &k, nil
}

// marshalScopes 把 scopes 转成 JSONB 参数；空切片写 NULL，
// 与内存版「nil scopes 保持 nil」一致，读回时也不会有 [] 与 nil 的差别。
func marshalScopes(scopes []string) ([]byte, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	return json.Marshal(scopes)
}
