package analysis

import (
	"context"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

// CancelAs is the public mutation port; trusted worker settlement keeps using
// Cancel/TransitionRun and does not depend on the original actor still being active.
func (s *Service) CancelAs(ctx context.Context, tid, aid string, actor billingpolicy.Actor) error {
	if actor.UserID == "" || actor.TokenVersion == nil || actor.APIKeyID != "" {
		return pkgerrors.ErrForbidden
	}
	if _, err := s.Get(ctx, tid, aid); err != nil {
		return err
	}
	actor.Permission = "analyses:cancel"
	if pg, ok := s.store.(*pgStore); ok {
		return pg.mutateAs(ctx, tid, aid, &actor, func(a *AnalysisResult) error {
			if !CanTransition(string(a.State), string(StateCanceled)) {
				return pkgerrors.ErrConflict
			}
			a.State = StateCanceled
			a.FinishedAt = time.Now().UTC()
			return nil
		})
	}
	if s.cancellationGuard == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	return s.cancellationGuard(ctx, tid, actor, func() error { return s.Cancel(ctx, tid, aid) })
}
func (s *Service) SetCancellationGuard(guard billingpolicy.MutationGuard) {
	s.cancellationGuard = guard
}
