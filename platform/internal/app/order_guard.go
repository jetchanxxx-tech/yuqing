package app

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/apikey"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"github.com/yuqing/platform/internal/platform/payment"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// Admission encloses only the local order commit. Supplier dispatch starts
// after the durable financial blocker exists, outside identity/key locks.
func MemoryOrderGuard(users *auth.SharedTenantStore, tenants *tenant.MemoryStore, keys *apikey.Service) payment.OrderGuard {
	return func(ctx context.Context, tid string, actor billingpolicy.Actor, commit func() error) error {
		return users.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
			u, ok := state.Users[actor.UserID]
			if !ok || u.Status != "active" || actor.TokenVersion != nil && u.TokenVersion != *actor.TokenVersion {
				return pkgerrors.ErrConflict
			}
			member := false
			for _, m := range state.Members {
				if m.UserID == actor.UserID && m.TenantID == tid {
					member = true
					break
				}
			}
			if !member {
				return pkgerrors.ErrConflict
			}
			t, err := tenants.Get(ctx, tid)
			if err != nil {
				return err
			}
			if t.Status != tenant.StatusActive {
				return pkgerrors.ErrConflict
			}
			if actor.APIKeyID != "" {
				return keys.WithOwnedKey(ctx, actor.APIKeyID, tid, actor.UserID, commit)
			}
			return commit()
		})
	}
}
