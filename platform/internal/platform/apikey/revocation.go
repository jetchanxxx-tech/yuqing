package apikey

import (
	"context"
	"strings"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

func (s *Service) SetRevocationGuard(guard billingpolicy.MutationGuard) { s.revocationGuard = guard }

// RevokeKeyAs retains the original public JWT actor until the key write commits.
// RevokeKey remains the trusted internal/store-level revocation contract.
func (s *Service) RevokeKeyAs(ctx context.Context, tid, kid string, actor billingpolicy.Actor) error {
	if actor.UserID == "" || actor.TokenVersion == nil || actor.APIKeyID != "" {
		return pkgerrors.ErrForbidden
	}
	if strings.TrimSpace(tid) == "" || strings.TrimSpace(kid) == "" {
		return pkgerrors.ErrNotFound
	}
	actor.Permission = "apikeys:manage"
	if pg, ok := s.store.(*PGStore); ok {
		return pg.revokeAs(ctx, tid, kid, actor)
	}
	if s.revocationGuard == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	return s.revocationGuard(ctx, tid, actor, func() error { return s.store.Revoke(ctx, tid, kid) })
}
func (s *PGStore) revokeAs(ctx context.Context, tid, kid string, actor billingpolicy.Actor) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	defer tx.Rollback(context.Background())
	if err = billingpolicy.CheckActorTx(ctx, tx, tid, actor); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND tenant_id=$2`, kid, tid)
	if err != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	return nil
}
