package accountadmin

import (
	"context"
	"time"

	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/tenant"
)

func (s *MemoryStore) ChangePlan(ctx context.Context, c PlanChange) (*credit.Snapshot, error) {
	if err := validatePlanChange(&c); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var result *credit.Snapshot
	err := s.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		if err := memoryAdmin(state, c.ActorID, c.ActorTokenVersion); err != nil {
			return err
		}
		return s.tenants.ChangeAdministration(ctx, c.TenantID, func(t *tenant.Tenant) error {
			if t.Status != tenant.StatusActive && t.Status != tenant.StatusSuspended {
				return conflict()
			}
			before, after, err := s.credits.ChangeMemoryPlan(ctx, c.TenantID, c.PlanCode, c.ExpectedVersion)
			if err != nil {
				return err
			}
			// No fallible operation follows the credit commit while all three
			// administration/account/tenant boundaries remain held.
			s.audits = append(s.audits, Audit{ID: int64(len(s.audits) + 1), ActorID: c.ActorID, Action: "tenant.plan_change", TargetType: "tenant", TargetID: c.TenantID, TenantID: c.TenantID, Reason: c.Reason, RequestID: c.RequestID, CreatedAt: time.Now().UTC(), Before: map[string]any{"balance": before.Balance, "plan_code": before.PlanCode, "plan_source": before.PlanSource, "version": before.Version}, After: map[string]any{"balance": after.Balance, "plan_code": after.PlanCode, "plan_source": after.PlanSource, "version": after.Version}})
			result = after
			return nil
		})
	})
	return result, err
}
