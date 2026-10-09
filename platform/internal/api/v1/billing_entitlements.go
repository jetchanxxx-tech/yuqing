package v1

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

func (s *Services) billingExempt(c *gin.Context) (bool, error) {
	principal := middleware.GetPrincipal(c)
	if principal == nil || s.PGPool == nil {
		return false, nil
	}
	userID := principal.UserID
	if principal.AuthType == "api_key" {
		key := middleware.GetAPIKey(c)
		if key == nil || key.CreatorUserID == "" {
			return false, nil
		}
		userID = key.CreatorUserID
	}
	return billingpolicy.NewService(s.PGPool).IsExempt(c.Request.Context(), userID)
}
func (s *Services) effectiveBudget(ctx context.Context, tenantID string) (billing.Budget, error) {
	if s.PGPool != nil {
		return billing.NewEntitlementService(s.PGPool).EffectiveBudget(ctx, tenantID)
	}
	snapshot, err := s.Credits.Snapshot(ctx, tenantID)
	if err != nil {
		return billing.Budget{}, err
	}
	if snapshot == nil {
		return billing.BudgetForPlan("free", "default_free")
	}
	return billing.BudgetForPlan(snapshot.PlanCode, "report_credits")
}
