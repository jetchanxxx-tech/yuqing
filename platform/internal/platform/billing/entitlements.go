package billing

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/llm"
	"time"
)

type Budget struct {
	PlanCode    string         `json:"plan_code"`
	PlanSource  string         `json:"effective_plan_source"`
	Mode        llm.BudgetMode `json:"budget_mode"`
	TokenQuota  int64          `json:"token_quota"`
	PeriodStart *time.Time     `json:"period_start"`
	PeriodEnd   *time.Time     `json:"period_end"`
	CycleStatus string         `json:"cycle_status"`
}
type EntitlementService struct{ pool *pgxpool.Pool }

func NewEntitlementService(pool *pgxpool.Pool) *EntitlementService {
	return &EntitlementService{pool: pool}
}
func BudgetForPlan(code, source string) (Budget, error) {
	if code == "" && source == "default_free" {
		code = "free"
	}
	plan := DefaultPlans()[code]
	if plan == nil {
		return Budget{}, pkgerrors.ErrServiceUnavailable
	}
	return Budget{PlanCode: code, PlanSource: source, Mode: plan.BudgetMode, TokenQuota: int64(plan.TokenQuotaM) * 1000000, CycleStatus: "not_configured"}, nil
}

type budgetQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *EntitlementService) EffectiveBudget(ctx context.Context, tenantID string) (Budget, error) {
	return effectiveBudget(ctx, s.pool, tenantID)
}
func (s *EntitlementService) EffectiveBudgetTx(ctx context.Context, tx pgx.Tx, tenantID string) (Budget, error) {
	return effectiveBudget(ctx, tx, tenantID)
}
func effectiveBudget(ctx context.Context, q budgetQuery, tenantID string) (Budget, error) {
	var code string
	source := "report_credits"
	err := q.QueryRow(ctx, `SELECT plan_code FROM report_credits WHERE tenant_id=$1 FOR SHARE`, tenantID).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		code = "free"
		source = "default_free"
	} else if err != nil {
		return Budget{}, err
	}
	budget, err := BudgetForPlan(code, source)
	if err != nil {
		return budget, err
	}
	var start, end time.Time
	err = q.QueryRow(ctx, `SELECT current_period_start,current_period_end FROM subscriptions WHERE tenant_id=$1 AND plan_code=$2 AND status='active' AND current_period_start<=now() AND current_period_end>now() ORDER BY current_period_start DESC,id DESC LIMIT 1`, tenantID, code).Scan(&start, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		return budget, nil
	}
	if err != nil {
		return budget, err
	}
	budget.PeriodStart = &start
	budget.PeriodEnd = &end
	budget.CycleStatus = "configured"
	return budget, nil
}
