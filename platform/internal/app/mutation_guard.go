package app

import (
	"context"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"github.com/yuqing/platform/internal/platform/tenant"
)

// Normal account/member/tenant administration and closure all take this user
// boundary exclusively before changing their rows. Hold its shared side through
// the local public mutation; no supplier call or auth read occurs in commit.
func MemoryMutationGuard(users *auth.SharedTenantStore, tenants *tenant.MemoryStore) billingpolicy.MutationGuard {
	return func(ctx context.Context, tid string, actor billingpolicy.Actor, commit func() error) error {
		return users.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
			if ctx.Err() != nil {
				return pkgerrors.ErrServiceUnavailable
			}
			u, exists := state.Users[actor.UserID]
			if !exists || u.Status != "active" || actor.TokenVersion == nil || u.TokenVersion != *actor.TokenVersion || actor.APIKeyID != "" {
				return pkgerrors.ErrForbidden
			}
			member, allowed := false, false
			for _, m := range state.Members {
				if m.UserID == actor.UserID && m.TenantID == tid {
					member = true
					allowed = auth.RoleHasPermission(m.Role, actor.Permission)
					break
				}
			}
			if !member {
				return pkgerrors.ErrForbidden
			}
			for _, role := range state.PlatformRoles[actor.UserID] {
				allowed = allowed || auth.RoleHasPermission(role, actor.Permission)
			}
			if !allowed {
				return pkgerrors.ErrForbidden
			}
			team, err := tenants.Get(ctx, tid)
			if err != nil {
				return err
			}
			if team.Status != tenant.StatusActive {
				return pkgerrors.ErrTenantSuspended
			}
			return commit()
		})
	}
}
