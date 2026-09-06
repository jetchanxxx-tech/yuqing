package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/id"
	"github.com/yuging/platform/internal/pkg/llm"
	ptenant "github.com/yuging/platform/internal/platform/tenant"
	"github.com/yuging/platform/internal/platform/usage"
)

// Fixed role granted to the self-registered tenant owner.
const roleTenantAdmin = "tenant_admin"

// Default free-plan token quota for new tenants: 1M tokens, hard cap.
const freePlanTokenQuota = 1_000_000

// emailRe is a deliberately simple structural email check.
var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

// User is a platform account row.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	Name         string
}

// Tenant is the platform tenant row created during registration.
type Tenant struct {
	ID       string
	Name     string
	Slug     string
	DBName   string
	Status   string
	PlanCode string
}

// Member binds a user to a tenant with a role.
type Member struct {
	TenantID string
	UserID   string
	Role     string
}

// Store persists the platform-side rows the auth flow needs.
type Store interface {
	CreateUser(ctx context.Context, u User) error
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	CreateTenant(ctx context.Context, t Tenant) error
	CreateMember(ctx context.Context, m Member) error
	// GetUserTenant returns the tenant the user belongs to (login principal).
	GetUserTenant(ctx context.Context, userID string) (*Tenant, error)
	// GetUserRole returns the user's role inside a tenant.
	GetUserRole(ctx context.Context, tenantID, userID string) (string, error)
}

// Service implements registration, login and token lifecycle.
type Service struct {
	store      Store
	secret     string
	accessTTL  string
	refreshTTL string
	meter      *usage.Meter
}

// NewService creates an auth service with its own usage meter.
// The meter is where Register applies the free-plan token quota.
func NewService(store Store, secret, accessTTL, refreshTTL string) *Service {
	return &Service{
		store:      store,
		secret:     secret,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		meter:      usage.NewMeter(),
	}
}

// Register validates credentials, provisions a user + tenant + membership,
// applies the free-plan quota and issues a token pair.
func (s *Service) Register(ctx context.Context, email, password, name string) (*Principal, *TokenPair, error) {
	email = normalizeEmail(email)
	if !emailRe.MatchString(email) {
		return nil, nil, fmt.Errorf("auth: invalid email format")
	}
	if len(password) < 8 {
		return nil, nil, fmt.Errorf("auth: password must be at least 8 characters")
	}
	if strings.TrimSpace(name) == "" {
		return nil, nil, fmt.Errorf("auth: name is required")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, nil, err
	}

	userID := id.New()
	u := User{ID: userID, Email: email, PasswordHash: hash, Name: name}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return nil, nil, err
	}

	tenantID := id.New()
	t := Tenant{
		ID:       tenantID,
		Name:     name + "的团队",
		Slug:     "t-" + strings.ToLower(tenantID),
		DBName:   ptenant.DBName(tenantID),
		Status:   string(ptenant.StatusActive),
		PlanCode: "free",
	}
	if err := s.store.CreateTenant(ctx, t); err != nil {
		return nil, nil, err
	}

	m := Member{TenantID: tenantID, UserID: userID, Role: roleTenantAdmin}
	if err := s.store.CreateMember(ctx, m); err != nil {
		return nil, nil, err
	}

	// Free plan: 1M token hard cap applied at provisioning time.
	s.meter.SetQuota(tenantID, freePlanTokenQuota, llm.BudgetHardCap)

	p := &Principal{
		UserID:       userID,
		TenantID:     tenantID,
		Email:        email,
		Roles:        []string{roleTenantAdmin},
		PlanCode:     t.PlanCode,
		TenantStatus: t.Status,
	}
	pair, err := GenerateTokenPair(*p, s.secret, s.accessTTL, s.refreshTTL)
	if err != nil {
		return nil, nil, err
	}
	return p, pair, nil
}

// Login verifies credentials and returns the principal plus a fresh token pair.
func (s *Service) Login(ctx context.Context, email, password string) (*Principal, *TokenPair, error) {
	email = normalizeEmail(email)

	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			return nil, nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid email or password")
		}
		return nil, nil, err
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return nil, nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid email or password")
	}

	t, err := s.store.GetUserTenant(ctx, u.ID)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			return nil, nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "user has no active tenant")
		}
		return nil, nil, err
	}
	role, err := s.store.GetUserRole(ctx, t.ID, u.ID)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			return nil, nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "user has no membership")
		}
		return nil, nil, err
	}

	p := &Principal{
		UserID:       u.ID,
		TenantID:     t.ID,
		Email:        u.Email,
		Roles:        []string{role},
		PlanCode:     t.PlanCode,
		TenantStatus: t.Status,
	}
	pair, err := GenerateTokenPair(*p, s.secret, s.accessTTL, s.refreshTTL)
	if err != nil {
		return nil, nil, err
	}
	return p, pair, nil
}

// Authenticate validates an access token and returns its principal.
func (s *Service) Authenticate(_ context.Context, token string) (*Principal, error) {
	p, err := ValidateAccessToken(token, s.secret)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid or expired access token")
	}
	return p, nil
}

// Refresh validates a refresh token (itself a JWT in the MVP) and reissues
// a fresh token pair for the same principal.
func (s *Service) Refresh(_ context.Context, refreshToken string) (*TokenPair, error) {
	p, err := ValidateAccessToken(refreshToken, s.secret)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid or expired refresh token")
	}
	return GenerateTokenPair(*p, s.secret, s.accessTTL, s.refreshTTL)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
