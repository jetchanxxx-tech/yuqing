package auth

import (
	"context"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// AuthorizationState is a current store snapshot for one user and the explicit
// tenant bound to their token. A missing membership never selects another tenant.
type AuthorizationState struct {
	UserID        string
	Email         string
	UserStatus    string
	TokenVersion  int64
	PlatformRoles []string
	TenantID      string
	TenantStatus  string
	PlanCode      string
	PlanStatus    string
	PlanSource    string
	MemberRole    string
	MemberExists  bool
}

type AuthorizationStateStore interface {
	LoadAuthorizationState(ctx context.Context, userID, tenantID string) (*AuthorizationState, error)
}

// RegistrationStore commits all account rows and the optional initial admin
// role together. bootstrapInitialAdmin is used only when no platform admin has
// yet been persisted; it is never an authorization fallback for existing users.
type RegistrationStore interface {
	RegisterAccount(ctx context.Context, user User, tenant Tenant, member Member, bootstrapInitialAdmin bool) error
}

// LoginRecorder checks the credential version before recording a successful
// password login, so concurrent revocation cannot mint tokens for the new version.
type LoginRecorder interface {
	RecordSuccessfulLogin(ctx context.Context, userID string, tokenVersion int64) error
}

func (s *Service) loadPrincipal(ctx context.Context, userID, tenantID string, version int64) (*Principal, error) {
	store, ok := s.store.(AuthorizationStateStore)
	if !ok {
		return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "authorization store unavailable")
	}
	state, err := store.LoadAuthorizationState(ctx, userID, tenantID)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "account unavailable")
		}
		return nil, err
	}
	if state == nil || state.UserID != userID || state.TenantID != tenantID ||
		(state.UserStatus != "active" && state.UserStatus != "closure_pending") || state.TokenVersion != version {
		return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "account or credential revoked")
	}
	roles := make([]string, 0, len(state.PlatformRoles)+1)
	if state.MemberExists && state.UserStatus == "active" {
		switch state.MemberRole {
		case "tenant_admin", "analyst", "viewer":
			roles = append(roles, state.MemberRole)
		}
	}
	for _, role := range state.PlatformRoles {
		if state.UserStatus != "active" {
			break
		}
		if role == rolePlatformAdmin {
			roles = append(roles, role)
		}
	}
	return &Principal{
		UserID: state.UserID, Email: state.Email, UserStatus: state.UserStatus,
		TokenVersion: state.TokenVersion, TenantID: state.TenantID,
		TenantStatus: state.TenantStatus, PlanCode: state.PlanCode, PlanStatus: state.PlanStatus, PlanSource: state.PlanSource,
		MemberExists: state.MemberExists, Roles: roles, AuthType: "jwt",
	}, nil
}
