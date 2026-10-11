package accountadmin

import (
	"context"
	"strings"
	"unicode/utf8"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/credit"
)

type PlanChange struct {
	ActorID, TenantID, PlanCode, Reason, RequestID string
	ActorTokenVersion, ExpectedVersion             int64
}

func validatePlanChange(change *PlanChange) error {
	change.Reason = strings.TrimSpace(change.Reason)
	if change.ActorID == "" || change.TenantID == "" || change.ActorTokenVersion < 0 || change.ExpectedVersion < 0 || change.Reason == "" || utf8.RuneCountInString(change.Reason) > 1000 || billing.DefaultPlans()[change.PlanCode] == nil {
		return pkgerrors.ErrBadRequest
	}
	return nil
}

func (s *Service) ChangePlan(ctx context.Context, change PlanChange) (*credit.Snapshot, error) {
	if err := validatePlanChange(&change); err != nil {
		return nil, err
	}
	store, ok := s.store.(interface {
		ChangePlan(context.Context, PlanChange) (*credit.Snapshot, error)
	})
	if !ok {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	return store.ChangePlan(ctx, change)
}
