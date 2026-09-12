package alert

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// PGStore 是 alert.Store 的 PostgreSQL 实现。
//
// 位置：alerts 表在**租户库**（migrations/tenant/0001_init.sql），
// 平台库没有这张表 —— 本平台是 database-per-tenant，一个租户一个物理库。
//
// 因此租户隔离是「物理」的：每个租户库各自持有自己的 alerts 表。
// 但 Store 接口按 tenantID 传参，若参数与库不匹配（调用方传错租户 ID），
// 直接查表会把本库的行当成别人的行返回。所以 PGStore 绑定一个租户 ID，
// 任何一次调用的 tenantID 与它不一致时一律 ErrNotFound：
// 拿别家的 ID 既查不到数据，也问不出「它是否存在」。
//
// 装配方式（composition root）二选一：
//   - 每个租户一个 store：NewPGStore(dbManager.Tenant(ctx, tenantID) 拿到的池, tenantID)；
//   - 单例 + 路由：用 ResolverStore 按 tenantID 动态取池（alert.Service 可全局单例）。
type PGStore struct {
	pool     *pgxpool.Pool
	tenantID string
}

// NewPGStore 绑定一个租户库连接池与它的租户 ID。
// 池由调用方（组合根）持有并负责关闭。
func NewPGStore(pool *pgxpool.Pool, tenantID string) *PGStore {
	return &PGStore{pool: pool, tenantID: tenantID}
}

var _ Store = (*PGStore)(nil)

// PoolResolver 按租户解析其独立数据库的连接池（database-per-tenant）。
type PoolResolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error)

// ResolverStore 让 alert.Service 保持全局单例：每次调用按 tenantID 取池，
// 再委托给绑定该租户的 PGStore。
type ResolverStore struct {
	resolve PoolResolver
}

// NewResolverStore 构造按租户路由的 store。
func NewResolverStore(resolve PoolResolver) *ResolverStore {
	return &ResolverStore{resolve: resolve}
}

var _ Store = (*ResolverStore)(nil)

func (r *ResolverStore) with(ctx context.Context, tenantID string, fn func(*PGStore) error) error {
	pool, err := r.resolve(ctx, tenantID)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: 解析租户数据库失败: "+err.Error())
	}
	return fn(NewPGStore(pool, tenantID))
}

func (r *ResolverStore) Create(ctx context.Context, tenantID string, a Alert) error {
	return r.with(ctx, tenantID, func(s *PGStore) error { return s.Create(ctx, tenantID, a) })
}

func (r *ResolverStore) Get(ctx context.Context, tenantID, alertID string) (*Alert, error) {
	var out *Alert
	err := r.with(ctx, tenantID, func(s *PGStore) error {
		got, err := s.Get(ctx, tenantID, alertID)
		out = got
		return err
	})
	return out, err
}

func (r *ResolverStore) List(ctx context.Context, tenantID string) ([]Alert, error) {
	var out []Alert
	err := r.with(ctx, tenantID, func(s *PGStore) error {
		got, err := s.List(ctx, tenantID)
		out = got
		return err
	})
	return out, err
}

func (r *ResolverStore) UpdateTrigger(ctx context.Context, tenantID, alertID string, at time.Time) error {
	return r.with(ctx, tenantID, func(s *PGStore) error { return s.UpdateTrigger(ctx, tenantID, alertID, at) })
}

// Create 插入一条告警规则。rule_json 原样落库（业务校验在 Service 层），
// Threshold/RecipientEmail 读的时候再从它解析出来，不做冗余列。
func (s *PGStore) Create(ctx context.Context, tenantID string, a Alert) error {
	if tenantID != s.tenantID {
		return errForeignTenant()
	}
	const q = `INSERT INTO alerts (id, tenant_id, name, rule_json, last_triggered_at, enabled, created_at)
	           VALUES ($1, $2, $3, $4::jsonb, $5, true, now())`

	rule := a.RuleJSON
	if rule == "" {
		rule = "{}"
	}
	var triggered any
	if !a.LastTriggeredAt.IsZero() {
		triggered = a.LastTriggeredAt
	}

	_, err := s.pool.Exec(ctx, q, a.ID, tenantID, a.Name, rule, triggered)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "alert already exists")
		}
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: create: "+err.Error())
	}
	return nil
}

// Get 返回一条告警。
func (s *PGStore) Get(ctx context.Context, tenantID, alertID string) (*Alert, error) {
	if tenantID != s.tenantID {
		return nil, errForeignTenant()
	}

	a, err := scanAlert(s.pool.QueryRow(ctx, alertColumns+` WHERE id = $1 AND tenant_id = $2`, alertID, tenantID), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "alert not found")
	}
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: get: "+err.Error())
	}
	return a, nil
}

// List 返回本租户的全部告警（按创建顺序）。
func (s *PGStore) List(ctx context.Context, tenantID string) ([]Alert, error) {
	if tenantID != s.tenantID {
		return nil, errForeignTenant()
	}

	rows, err := s.pool.Query(ctx, alertColumns+` WHERE tenant_id = $1 ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: list: "+err.Error())
	}
	defer rows.Close()

	// 永远返回非 nil 切片：JSON 里是 [] 而不是 null。
	out := make([]Alert, 0)
	for rows.Next() {
		a, err := scanAlert(rows, tenantID)
		if err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: list scan: "+err.Error())
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: list rows: "+err.Error())
	}
	return out, nil
}

// UpdateTrigger 记录最近一次触发时间（RowsAffected == 0 即不存在，
// 不需要先 SELECT：主键 + 租户过滤，影响的只会是 0 或 1 行）。
func (s *PGStore) UpdateTrigger(ctx context.Context, tenantID, alertID string, at time.Time) error {
	if tenantID != s.tenantID {
		return errForeignTenant()
	}
	const q = `UPDATE alerts SET last_triggered_at = $3 WHERE id = $1 AND tenant_id = $2`

	tag, err := s.pool.Exec(ctx, q, alertID, tenantID, at)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: update trigger: "+err.Error())
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "alert not found")
	}
	return nil
}

// alertColumns 是读取路径共用的列清单。tenant_id 由 WHERE 条件过滤。
const alertColumns = `SELECT id, name, rule_json, last_triggered_at FROM alerts`

type rowScanner interface {
	Scan(dest ...any) error
}

// scanAlert 读一行并把 rule_json 解析成 Threshold / RecipientEmail：
// 表里没有独立的阈值与收件人列，它们是 rule_json 的投影。
//
// TenantID 表里也没有（库即边界），由 Store 的绑定值回填 —— 这不是猜测，
// 而是「本连接池就是该租户的库」这一事实的显式化。
//
// CreatedAt 同理没有列可存，只能是零值（见交付说明：需要一条 ALTER TABLE）；
// service.Create 返回的对象带真实时间，但从库里重新读出来的告警没有。
func scanAlert(row rowScanner, tenantID string) (*Alert, error) {
	var (
		a         Alert
		ruleJSON  string
		triggered *time.Time
	)
	if err := row.Scan(&a.ID, &a.Name, &ruleJSON, &triggered); err != nil {
		return nil, err
	}

	a.RuleJSON = ruleJSON
	a.TenantID = tenantID
	if triggered != nil {
		a.LastTriggeredAt = *triggered
	}

	var r rule
	if err := json.Unmarshal([]byte(ruleJSON), &r); err != nil {
		// 脏数据不该让整条 List 失败：名称/ID 仍可用，
		// 只是阈值缺失（不会被 Check 命中）。与「读失败就报错」相比，
		// 保留可见性更利于排查。
		return &a, nil
	}
	a.Threshold = r.Threshold
	a.RecipientEmail = r.Email
	return &a, nil
}

// errForeignTenant 与「该租户下没有这条告警」不可区分：
// 跨租户探测拿不到任何额外的存在性信息。
func errForeignTenant() error {
	return pkgerrors.Wrap(pkgerrors.ErrNotFound, "alert not found")
}
