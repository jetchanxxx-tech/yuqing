package accountadmin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/yuqing/platform/internal/platform/credit"
)

func (s *PGStore) ChangePlan(ctx context.Context, c PlanChange) (*credit.Snapshot, error) {
	if err := validatePlanChange(&c); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, internal(err)
	}
	defer tx.Rollback(context.Background())
	if err = lockAdministrator(ctx, tx, c.ActorID, c.ActorTokenVersion); err != nil {
		return nil, err
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1 FOR UPDATE`, c.TenantID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, missing()
	}
	if err != nil {
		return nil, internal(err)
	}
	if status != "active" && status != "suspended" {
		return nil, conflict()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO report_credits(tenant_id,balance) VALUES($1,0) ON CONFLICT DO NOTHING`, c.TenantID); err != nil {
		return nil, internal(err)
	}
	before := &credit.Snapshot{}
	if err = tx.QueryRow(ctx, `SELECT balance,plan_code,plan_source,version FROM report_credits WHERE tenant_id=$1 FOR UPDATE`, c.TenantID).Scan(&before.Balance, &before.PlanCode, &before.PlanSource, &before.Version); err != nil {
		return nil, internal(err)
	}
	if before.Version != c.ExpectedVersion {
		return nil, conflict()
	}
	after := &credit.Snapshot{}
	if err = tx.QueryRow(ctx, `UPDATE report_credits SET plan_code=$2,plan_source='manual',updated_at=now() WHERE tenant_id=$1 RETURNING balance,plan_code,plan_source,version`, c.TenantID, c.PlanCode).Scan(&after.Balance, &after.PlanCode, &after.PlanSource, &after.Version); err != nil {
		return nil, internal(err)
	}
	details, _ := json.Marshal(map[string]any{"target_type": "tenant", "target_id": c.TenantID, "reason": c.Reason, "request_id": c.RequestID, "before": before, "after": after})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,tenant_id,action,resource,details_json) VALUES($1,$2,'tenant.plan_change','tenant',$3::jsonb)`, c.ActorID, c.TenantID, details); err != nil {
		return nil, internal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, internal(err)
	}
	return after, nil
}
