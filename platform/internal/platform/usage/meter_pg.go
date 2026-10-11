package usage

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/llm"
	"github.com/yuqing/platform/internal/platform/billing"
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

// PGMeter reads durable plan entitlements and quota facts. Process-local
// registration hints cannot override a paid plan or reset limits on restart.
type PGMeter struct{ pool *pgxpool.Pool }

func NewPGMeter(pool *pgxpool.Pool) *PGMeter { return &PGMeter{pool: pool} }

// SetQuota remains an interface compatibility hook for the memory meter only.
// PostgreSQL's authoritative source is report_credits plus the server catalog.
func (m *PGMeter) SetQuota(_ string, _ int64, _ llm.BudgetMode) {}
func (m *PGMeter) BudgetStatus(ctx context.Context, tenantID string) (llm.BudgetStatus, error) {
	budget, err := billing.NewEntitlementService(m.pool).EffectiveBudget(ctx, tenantID)
	if err != nil {
		return llm.BudgetStatus{}, err
	}
	var spent int64
	if err = m.pool.QueryRow(ctx, `SELECT COALESCE(SUM(quota_tokens),0) FROM usage_events WHERE tenant_id=$1 AND ($2::timestamptz IS NULL OR created_at >= $2) AND ($3::timestamptz IS NULL OR created_at < $3)`, tenantID, budget.PeriodStart, budget.PeriodEnd).Scan(&spent); err != nil {
		return llm.BudgetStatus{}, err
	}
	status := "ok"
	if budget.TokenQuota > 0 {
		if spent >= budget.TokenQuota {
			status = "exceeded"
		} else if float64(spent)/float64(budget.TokenQuota) >= 0.8 {
			status = "warn"
		}
	}
	return llm.BudgetStatus{SpentTokens: spent, QuotaTokens: budget.TokenQuota, Status: status}, nil
}

// Record preserves the legacy Go meter contract for explicit trusted facts.
// Actual provider attempts use CallService.Record and its call/event idempotency.
//
// model 列 NOT NULL：空 model 以空串写入，不会因缺字段而丢事件 ——
// 丢事件比多一条脏数据更难排查。
func (m *PGMeter) Record(ctx context.Context, e llm.UsageEvent) error {
	const q = `WITH policy AS (SELECT EXISTS(SELECT 1 FROM billing_exempt_principals WHERE user_id=$2) AS exempt)
 INSERT INTO usage_events
	           (tenant_id, user_id, model, analysis_id,
	            prompt_tokens, completion_tokens, cache_tokens,
	            cost_micro_cny, billed_micro_cny,quota_tokens,usage_status,cost_status,billing_exempt)
	           SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9,CASE WHEN exempt THEN 0::bigint ELSE $10::bigint END,'reported','known',exempt FROM policy`

	_, err := m.pool.Exec(ctx, q,
		e.TenantID, nullIfEmpty(e.UserID), e.Model, nullIfEmpty(e.AnalysisID),
		e.PromptTokens, e.CompletionTokens, e.CacheTokens,
		e.CostMicroCNY, e.BilledMicroCNY, int64(e.PromptTokens)+int64(e.CompletionTokens)+int64(e.CacheTokens),
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

	const q = `SELECT tenant_id,
	                  SUM(COALESCE(prompt_tokens, 0)
	                      + COALESCE(completion_tokens, 0)
	                      + CASE WHEN event_version IS NULL THEN COALESCE(cache_tokens,0) ELSE 0 END)
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
