package settings

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// PGStore is the PostgreSQL-backed platform settings store
// (table: platform_settings — created by migrations/platform/0002_pg_store_support.sql).
//
// 语义与 MemoryStore 一致，外加一条内存版不需要的规则：
// **种子只在键不存在时写入**（INSERT ... ON CONFLICT DO NOTHING）。
// 内存版每次启动都从环境变量重建，覆盖与否无所谓；而落库后，
// 环境变量里那个「部署时写死的旧值」不能把管理员在线改过的值顶回去。
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore creates a settings store and applies the environment seed.
//
// 与 NewMemoryStore(envDefaults) 同构，唯一区别是返回 error：种子要落库，
// 写失败必须让启动流程知道（否则 BOCHA_API_KEY 会静默丢失，采集链路变成
// 「没有 key」而没人发现）。不想在构造期报错的调用方可以改用 Seed。
func NewPGStore(pool *pgxpool.Pool, seed map[string]string) (*PGStore, error) {
	s := &PGStore{pool: pool}
	if err := s.Seed(context.Background(), seed); err != nil {
		return nil, err
	}
	return s, nil
}

var _ Store = (*PGStore)(nil)

// Seed 把种子键写入表，已存在的键一律保持库里的值（幂等，可反复调用）。
func (s *PGStore) Seed(ctx context.Context, seed map[string]string) error {
	if len(seed) == 0 {
		return nil
	}

	// 一条语句批量 upsert；键排序只为让语句与测试可复现。
	keys := make([]string, 0, len(seed))
	for k := range seed {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var (
		placeholders []string
		args         []any
	)
	for i, k := range keys {
		placeholders = append(placeholders, "($"+strconv.Itoa(2*i+1)+", $"+strconv.Itoa(2*i+2)+")")
		args = append(args, k, seed[k])
	}

	// DO NOTHING 而不是 DO UPDATE：种子是「兜底默认值」，不是「部署真相」。
	q := `INSERT INTO platform_settings (key, value) VALUES ` +
		strings.Join(placeholders, ", ") +
		` ON CONFLICT (key) DO NOTHING`

	if _, err := s.pool.Exec(ctx, q, args...); err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "settings: seed: "+err.Error())
	}
	return nil
}

// Get returns the value for key. 未设置的键返回空串 + nil 错误 ——
// 与内存版一致（内存版读 map 未命中即零值），调用方靠空串判定「没配」。
func (s *PGStore) Get(ctx context.Context, key string) (string, error) {
	const q = `SELECT value FROM platform_settings WHERE key = $1`

	var value string
	err := s.pool.QueryRow(ctx, q, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", pkgerrors.Wrap(pkgerrors.ErrInternal, "settings: get: "+err.Error())
	}
	return value, nil
}

// Set 写入或覆盖一个键（管理员在线修改的唯一写路径）。
func (s *PGStore) Set(ctx context.Context, key string, value string) error {
	const q = `INSERT INTO platform_settings (key, value, updated_at) VALUES ($1, $2, now())
	           ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`

	if _, err := s.pool.Exec(ctx, q, key, value); err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "settings: set: "+err.Error())
	}
	return nil
}

// All returns every setting as a detached map (admin GET hands it straight to JSON).
func (s *PGStore) All(ctx context.Context) (map[string]string, error) {
	const q = `SELECT key, value FROM platform_settings`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "settings: all: "+err.Error())
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "settings: all scan: "+err.Error())
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "settings: all rows: "+err.Error())
	}
	return out, nil
}
