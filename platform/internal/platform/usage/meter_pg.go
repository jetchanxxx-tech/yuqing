package usage

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/llm"
)

// PlatformMeter 是平台侧计量视图（预算检查 + 全租户汇总）。
//
// 内存版 Meter 与 PG 版 PGMeter 都满足它：composition root 把
// v1.Services.Usage 从 *usage.Meter 换成这个接口即可在两种模式间切换，
// /admin/usage 与 LLM 计量链路无需改动。
type PlatformMeter interface {
	// SetQuota configures a tenant's token budget.
	SetQuota(tenantID string, quota int64, mode llm.BudgetMode)
	// BudgetStatus returns current token usage vs quota.
	BudgetStatus(ctx context.Context, tenantID string) (llm.BudgetStatus, error)
	// Record records a usage event.
	Record(ctx context.Context, e llm.UsageEvent) error
	// Aggregate returns spent tokens per tenant (platform-wide rollup).
	Aggregate() map[string]int64
}

var (
	_ PlatformMeter = (*Meter)(nil)
	_ llm.Meter     = (*PGMeter)(nil)
	_ PlatformMeter = (*PGMeter)(nil)
)

// PGMeter 把 LLM 用量落到 PostgreSQL（表 usage_events），
// 替代「重启即清零」的内存计数器。
//
// 配额仍在内存：配额来自套餐（plans.token_quota_m / tenants.quota_json），
// 由 auth 在注册时通过 SetQuota 下发，本轮不改变下发链路。因此 PGMeter 的
// 语义是「已用 token 持久化，配额进程内配置」：
//
//   - BudgetStatus/Record 跨重启一致（这是内存版最严重的问题：
//     重启后 SpentTokens 归零，hard-cap 租户立刻又能白烧一轮预算）；
//   - 重启后 SetQuota 必须被重新调用一次，否则该租户视为无配额
//     （QuotaTokens=0 → 状态恒为 ok）。
//
// 查询走 idx_usage_events_tenant_created 索引；BudgetStatus 在每次 LLM 调用
// 前触发一次聚合，用量表变大后应改为增量计数或 usage_daily 汇总表。
type PGMeter struct {
	pool *pgxpool.Pool

	mu     sync.RWMutex
	quotas map[string]quotaSetting // keyed by tenantID
}

type quotaSetting struct {
	quotaTokens int64
	budgetMode  llm.BudgetMode
}

// NewPGMeter creates a metered provider backend over an existing platform pool.
// The pool is owned by the caller (the composition root), not by the meter.
func NewPGMeter(pool *pgxpool.Pool) *PGMeter {
	return &PGMeter{pool: pool, quotas: make(map[string]quotaSetting)}
}

// SetQuota configures a tenant's token budget (process-local, see type doc).
func (m *PGMeter) SetQuota(tenantID string, quota int64, mode llm.BudgetMode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.quotas[tenantID] = quotaSetting{quotaTokens: quota, budgetMode: mode}
}

// BudgetStatus returns spent tokens (summed from usage_events) against the
// tenant's in-memory quota.
//
// 阈值与内存版一致：ratio >= 1.0 → exceeded，>= 0.8 → warn，否则 ok。
// 无配额（QuotaTokens == 0）时 ratio 视为 0 —— 与内存版对未知租户的
// 处理相同，也表示「该租户本轮尚未下发配额」。
func (m *PGMeter) BudgetStatus(ctx context.Context, tenantID string) (llm.BudgetStatus, error) {
	// 逐列 COALESCE：token 列可为 NULL（只有 DEFAULT，没有 NOT NULL），
	// 一行里任何一个 NULL 都会让整个加法表达式变 NULL 并被 SUM 丢掉 ——
	// 那会静默少算用量、把已超额租户放行。外层 COALESCE 兜「零行」。
	const q = `SELECT COALESCE(SUM(COALESCE(prompt_tokens, 0)
	                              + COALESCE(completion_tokens, 0)
	                              + COALESCE(cache_tokens, 0)), 0)
	           FROM usage_events WHERE tenant_id = $1`

	var spent int64
	if err := m.pool.QueryRow(ctx, q, tenantID).Scan(&spent); err != nil {
		return llm.BudgetStatus{}, pkgerrors.Wrap(pkgerrors.ErrInternal, "usage: budget status: "+err.Error())
	}

	m.mu.RLock()
	setting := m.quotas[tenantID]
	m.mu.RUnlock()

	status := "ok"
	ratio := float64(0)
	if setting.quotaTokens > 0 {
		ratio = float64(spent) / float64(setting.quotaTokens)
	}
	switch {
	case ratio >= 1.0:
		status = "exceeded"
	case ratio >= 0.8:
		status = "warn"
	}

	return llm.BudgetStatus{
		SpentTokens: spent,
		QuotaTokens: setting.quotaTokens,
		Status:      status,
	}, nil
}

// Record appends a usage event. 流水表不去重：与内存版一样，重复投递同一事件
// 会重复计数（usage_events 没有可去重的唯一键，且 BIGSERIAL id 不构成幂等键）。
//
// model 列 NOT NULL：空 model 以空串写入，不会因缺字段而丢事件 ——
// 丢事件比多一条脏数据更难排查。
func (m *PGMeter) Record(ctx context.Context, e llm.UsageEvent) error {
	const q = `INSERT INTO usage_events
	           (tenant_id, user_id, model, analysis_id,
	            prompt_tokens, completion_tokens, cache_tokens,
	            cost_micro_cny, billed_micro_cny)
	           VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := m.pool.Exec(ctx, q,
		e.TenantID, nullIfEmpty(e.UserID), e.Model, nullIfEmpty(e.AnalysisID),
		e.PromptTokens, e.CompletionTokens, e.CacheTokens,
		e.CostMicroCNY, e.BilledMicroCNY,
	)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "usage: record: "+err.Error())
	}
	return nil
}

// nullIfEmpty 把空字符串写成 SQL NULL：user_id / analysis_id 是「没有就为空」
// 的可空列，写成空串会让 `IS NULL` 之类的查询漏行。
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Aggregate returns spent tokens per tenant — the rollup view for /admin/usage.
//
// 结果 = usage_events 的按租户汇总 ∪ 本进程已知配额租户（缺省 0）。
// 后一半是为了与内存版一致：注册即 SetQuota，租户在还没产生用量时就已
// 出现在聚合视图里（前端显示 0 而不是缺行）。返回值始终是副本。
func (m *PGMeter) Aggregate() map[string]int64 {
	out := make(map[string]int64)

	m.mu.RLock()
	for tenantID := range m.quotas {
		out[tenantID] = 0
	}
	m.mu.RUnlock()

	const q = `SELECT tenant_id,
	                  SUM(COALESCE(prompt_tokens, 0)
	                      + COALESCE(completion_tokens, 0)
	                      + COALESCE(cache_tokens, 0))
	           FROM usage_events GROUP BY tenant_id`

	rows, err := m.pool.Query(context.Background(), q)
	if err != nil {
		// 接口与内存版一致，没有返回错误的通道（调用方 /admin/usage 也不查错）。
		// 因此这里退回「已知配额租户 + 0」并显式打日志：静默返回 0 会让管理员
		// 在数据库抖动时误以为「本月没有用量」。
		slog.Default().Error("usage: 聚合 usage_events 失败，返回的用量可能不完整", "err", err)
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var (
			tenantID string
			spent    int64
		)
		if err := rows.Scan(&tenantID, &spent); err != nil {
			slog.Default().Error("usage: 读取 usage_events 行失败，返回的用量可能不完整", "err", err)
			return out
		}
		out[tenantID] = spent
	}
	if err := rows.Err(); err != nil {
		slog.Default().Error("usage: usage_events 结果集出错，返回的用量可能不完整", "err", err)
	}
	return out
}
