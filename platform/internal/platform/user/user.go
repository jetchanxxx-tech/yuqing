// Package user manages tenant members, invitations, and roles.
package user

import (
	"context"
	"fmt"
)

// Service defines the user/member management interface.
type Service interface {
	ListMembers(ctx context.Context, tenantID string) ([]Member, error)
	Invite(ctx context.Context, tenantID, email, role string) error
	AcceptInvite(ctx context.Context, token string) error
	UpdateRole(ctx context.Context, tenantID, userID, role string) error
	RemoveMember(ctx context.Context, tenantID, userID string) error
}

// Member represents a user within a tenant.
type Member struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	JoinedAt string `json:"joined_at,omitempty"`
}

// Store defines the persistence layer for user data.
type Store interface {
	ListMembers(ctx context.Context, tenantID string) ([]Member, error)
	GetMember(ctx context.Context, tenantID, userID string) (*Member, error)
	AddMember(ctx context.Context, tenantID string, m Member) error
	UpdateRole(ctx context.Context, tenantID, userID, role string) error
	RemoveMember(ctx context.Context, tenantID, userID string) error
}

// Common user errors.
var (
	ErrAlreadyMember  = fmt.Errorf("user: already a member")
	ErrNotMember      = fmt.Errorf("user: not a member of this tenant")
	ErrInviteExpired  = fmt.Errorf("user: invitation expired")
	ErrInviteAccepted = fmt.Errorf("user: invitation already accepted")
)
