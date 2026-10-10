package accountadmin

import (
	"context"
	"strings"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/payment"
)

// Query is validated by the HTTP boundary before reaching a store. Stores apply
// filters and pagination before returning data, in created_at/id order.
type Query struct {
	CreatedFrom, CreatedTo                      *time.Time
	Page, PageSize                              int
	Q, Status, PlatformRole, Verified, PlanCode string
}

type UserRow struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Email         string     `json:"email"`
	PhoneMasked   string     `json:"phone_masked"`
	Status        string     `json:"status"`
	EmailVerified bool       `json:"email_verified"`
	PhoneVerified bool       `json:"phone_verified"`
	PlatformRoles []string   `json:"platform_roles"`
	TenantCount   int        `json:"tenant_count"`
	CreatedAt     time.Time  `json:"created_at"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	RowVersion    int64      `json:"row_version"`
}

type Membership struct {
	TenantID     string `json:"tenant_id"`
	TenantName   string `json:"tenant_name"`
	TenantStatus string `json:"tenant_status"`
	Role         string `json:"role"`
	RowVersion   int64  `json:"row_version"`
}

type Audit struct {
	ID         int64          `json:"id"`
	ActorID    string         `json:"actor_id"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	TenantID   string         `json:"tenant_id"`
	Reason     string         `json:"reason"`
	Before     map[string]any `json:"before"`
	After      map[string]any `json:"after"`
	RequestID  string         `json:"request_id"`
	CreatedAt  time.Time      `json:"created_at"`
}

type UserDetail struct {
	UserRow
	Memberships []Membership `json:"memberships"`
	AuditLogs   []Audit      `json:"audit_logs"`
}

type TenantRow struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Slug              string    `json:"slug"`
	PlanCode          string    `json:"plan_code"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	RowVersion        int64     `json:"row_version"`
	UserCount         int       `json:"user_count"`
	EffectivePlanCode string    `json:"effective_plan_code"`
	PlanSource        string    `json:"plan_source"`
}

type TenantMember struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	RowVersion int64  `json:"row_version"`
}

type TenantDetail struct {
	TenantRow
	Members   []TenantMember   `json:"members"`
	Credit    *credit.Snapshot `json:"credit"`
	Orders    []*payment.Order `json:"orders"`
	AuditLogs []Audit          `json:"audit_logs"`
}

// Mutation carries only explicitly validated administrative intent. ActorID
// and ActorTokenVersion come from the original authenticated principal, never
// from request JSON; stores revalidate that identity through their commit.
type Mutation struct {
	ActorID, TargetID, TenantID, Action, Reason, RequestID string
	ExpectedVersion                                        int64
	ActorTokenVersion                                      int64
	PlatformAdmin                                          bool
	Role                                                   string
}

type CreateRequest struct {
	ActorID, Email, Name, TenantName string
	ActorTokenVersion int64
}
type CreateResult struct {
	UserID, TenantID, Status string
	RowVersion int64 `json:"row_version"`
}

type Result struct {
	ID            string    `json:"id,omitempty"`
	TenantID      string    `json:"tenant_id,omitempty"`
	UserID        string    `json:"user_id,omitempty"`
	Status        string    `json:"status,omitempty"`
	PlatformRoles *[]string `json:"platform_roles,omitempty"`
	Role          string    `json:"role,omitempty"`
	RowVersion    int64     `json:"row_version"`
}

type Store interface {
	ListUsers(context.Context, Query) ([]UserRow, int, error)
	User(context.Context, string) (*UserDetail, error)
	ListTenants(context.Context, Query) ([]TenantRow, int, error)
	Tenant(context.Context, string) (*TenantDetail, error)
	Change(context.Context, Mutation) (*Result, error)
	CreatePending(context.Context, CreateRequest) (*CreateResult, error)
}

type Service struct {
	store Store
	verificationSender func(context.Context, string, string, string, int64) error
}

func NewService(store Store) *Service { return &Service{store: store} }
func (s *Service) SetVerificationSender(sender func(context.Context, string, string, string, int64) error) { s.verificationSender = sender }
func (s *Service) Create(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if req.ActorID == "" || req.ActorTokenVersion < 0 || strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.Name) == "" { return nil, pkgerrors.ErrBadRequest }
	result, err := s.store.CreatePending(ctx, req)
	if err != nil { return nil, err }
	if s.verificationSender == nil { return result, pkgerrors.ErrServiceUnavailable }
	if err := s.verificationSender(ctx, req.ActorID, result.UserID, auth.SetPassword, req.ActorTokenVersion); err != nil { return result, err }
	return result, nil
}
func (s *Service) ResendActivation(ctx context.Context, actorID, targetID string, actorVersion int64) error {
	if s.verificationSender == nil { return pkgerrors.ErrServiceUnavailable }
	return s.verificationSender(ctx, actorID, targetID, auth.SetPassword, actorVersion)
}
func (s *Service) SendVerification(ctx context.Context, actorID, targetID, purpose string, actorVersion int64) error {
	if purpose != auth.SetPassword && purpose != auth.PasswordReset { return pkgerrors.ErrBadRequest }
	if s.verificationSender == nil { return pkgerrors.ErrServiceUnavailable }
	return s.verificationSender(ctx, actorID, targetID, purpose, actorVersion)
}
func (s *Service) ListUsers(ctx context.Context, q Query) ([]UserRow, int, error) {
	return s.store.ListUsers(ctx, q)
}
func (s *Service) User(ctx context.Context, id string) (*UserDetail, error) {
	return s.store.User(ctx, id)
}
func (s *Service) ListTenants(ctx context.Context, q Query) ([]TenantRow, int, error) {
	return s.store.ListTenants(ctx, q)
}
func (s *Service) Tenant(ctx context.Context, id string) (*TenantDetail, error) {
	return s.store.Tenant(ctx, id)
}
func (s *Service) Change(ctx context.Context, m Mutation) (*Result, error) {
	return s.store.Change(ctx, m)
}

func conflict() error {
	return pkgerrors.Wrap(pkgerrors.ErrConflict, "version, state or administrator responsibility changed")
}
func missing() error {
	return pkgerrors.Wrap(pkgerrors.ErrNotFound, "account, tenant or membership not found")
}
func internal(err error) error {
	return pkgerrors.Wrap(pkgerrors.ErrInternal, "account administration persistence failed: "+err.Error())
}
func hasAdmin(roles []string) bool {
	for _, r := range roles {
		if r == "platform_admin" {
			return true
		}
	}
	return false
}
func MaskPhone(phone string) string {
	r := []rune(phone)
	if len(r) == 0 {
		return ""
	}
	if len(r) == 11 {
		return string(r[:3]) + "****" + string(r[7:])
	}
	return strings.Repeat("*", len(r))
}
func userMatches(u UserRow, q Query) bool {
	if (q.CreatedFrom != nil && u.CreatedAt.Before(*q.CreatedFrom)) || (q.CreatedTo != nil && !u.CreatedAt.Before(*q.CreatedTo)) {
		return false
	}
	needle := strings.ToLower(q.Q)
	if needle != "" && !strings.Contains(strings.ToLower(u.ID), needle) && !strings.Contains(strings.ToLower(u.Name), needle) && !strings.Contains(strings.ToLower(u.Email), needle) {
		return false
	}
	if q.Status != "" && u.Status != q.Status || q.PlatformRole != "" && !hasAdmin(u.PlatformRoles) {
		return false
	}
	switch q.Verified {
	case "email":
		return u.EmailVerified
	case "phone":
		return u.PhoneVerified
	case "none":
		return !u.EmailVerified && !u.PhoneVerified
	}
	return true
}
func tenantMatches(t TenantRow, q Query) bool {
	needle := strings.ToLower(q.Q)
	return (needle == "" || strings.Contains(strings.ToLower(t.Name), needle) || strings.Contains(strings.ToLower(t.ID), needle)) && (q.Status == "" || q.Status == t.Status) && (q.PlanCode == "" || q.PlanCode == t.EffectivePlanCode)
}
func pageBounds(total int, q Query) (int, int) {
	// Division avoids overflow when an untrusted page is near MaxInt.
	if total == 0 || q.Page-1 > (total-1)/q.PageSize {
		return total, total
	}
	start := (q.Page - 1) * q.PageSize
	end := start + q.PageSize
	if end > total {
		end = total
	}
	return start, end
}
