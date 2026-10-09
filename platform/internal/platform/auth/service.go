package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/llm"
	ptenant "github.com/yuqing/platform/internal/platform/tenant"
	"github.com/yuqing/platform/internal/platform/usage"
)

// Fixed role granted to the self-registered tenant owner.
const roleTenantAdmin = "tenant_admin"

// rolePlatformAdmin is persisted independently from tenant membership roles.
const rolePlatformAdmin = "platform_admin"

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
	Status       string
	CreatedAt    time.Time
	LastLoginAt  *time.Time
	TokenVersion int64
	RowVersion   int64
	// ── 用户中心 P0 字段（迁移 0007；memory store 全量支持，pg store 渐进接线）──
	Phone             string
	AvatarURL         string
	Timezone          string
	EmailVerifiedAt   *time.Time
	PhoneVerifiedAt   *time.Time
	PasswordChangedAt *time.Time
	TrialAnalysisUsed int
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
	meter      usage.PlatformMeter

	// Optional initial registration seed; login never assigns roles by email.
	bootstrapAdminEmail string

	// postRegister 注册成功后的钩子（组合根接入 credit.Service：新租户赠
	// 试用额度）。失败不阻断注册（用户仍可购买），只降级为无试用额度。
	postRegister func(ctx context.Context, tenantID string) error

	// ── 用户中心 P0 依赖（组合根装配；nil = 功能未启用，fail-closed）──
	userStore     UserStore         // 用户中心存储
	verifications VerificationStore // 验证码/验证 token 存储
	smsSender     SMSProvider       // 短信发送
	emailSender   MailSender        // 邮件发送
	verifyBaseURL string            // 邮箱验证链接前缀（如 https://yuqing2.pangu-cloud.com）

	verificationLimits VerificationLimits
}

// EnableUserCenter 装配用户中心 P0 依赖（组合根调用）。
func (s *Service) EnableUserCenter(users UserStore, verifications VerificationStore, sms SMSProvider, mail MailSender, verifyBaseURL string) {
	s.userStore = users
	s.verifications = verifications
	s.smsSender = sms
	s.emailSender = mail
	s.verifyBaseURL = verifyBaseURL
	if memory, ok := verifications.(*MemoryVerificationStore); ok {
		if owner, ok := s.store.(interface{ verificationUsers() *MemoryStore }); ok {
			memory.users = owner.verificationUsers()
		}
	}

}

// SetPostRegister 挂接注册后回调。
func (s *Service) SetPostRegister(fn func(ctx context.Context, tenantID string) error) {
	s.postRegister = fn
}

// SetBootstrapAdminEmail allows a new matching account to seed the first
// persisted administrator. Existing accounts need an explicit administrative
// migration; login and token authentication never infer roles from this email.
func (s *Service) SetBootstrapAdminEmail(email string) {
	s.bootstrapAdminEmail = normalizeEmail(email)
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
	u := User{ID: userID, Email: email, PasswordHash: hash, Name: name, Status: "active", CreatedAt: time.Now()}

	tenantID := id.New()
	t := Tenant{
		ID:       tenantID,
		Name:     name + "的团队",
		Slug:     "t-" + strings.ToLower(tenantID),
		DBName:   ptenant.DBName(tenantID),
		Status:   string(ptenant.StatusActive),
		PlanCode: "free",
	}
	m := Member{TenantID: tenantID, UserID: userID, Role: roleTenantAdmin}
	registration, ok := s.store.(RegistrationStore)
	if !ok {
		return nil, nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "atomic registration unavailable")
	}
	// Check token configuration before committing a new account.
	if _, err := GenerateTokenPair(Principal{UserID: userID, TenantID: tenantID}, s.secret, s.accessTTL, s.refreshTTL); err != nil {
		return nil, nil, err
	}
	bootstrap := s.bootstrapAdminEmail != "" && email == s.bootstrapAdminEmail
	if err := registration.RegisterAccount(ctx, u, t, m, bootstrap); err != nil {
		return nil, nil, err
	}
	p, err := s.loadPrincipal(ctx, userID, tenantID, u.TokenVersion)
	if err != nil {
		return nil, nil, err
	}

	// 注册后钩子（试用额度发放等）。尽力而为：失败不阻断注册。
	if s.postRegister != nil {
		if err := s.postRegister(ctx, tenantID); err != nil {
			// 试用额度发放失败只影响体验，账号/租户已创建成功
			_ = err
		}
	}

	// Free plan: 1M token hard cap applied at provisioning time.
	s.meter.SetQuota(tenantID, freePlanTokenQuota, llm.BudgetHardCap)

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
	if u.Status != "active" || !VerifyPassword(u.PasswordHash, password) {
		return nil, nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid email or password")
	}

	var tenantID string
	t, err := s.store.GetUserTenant(ctx, u.ID)
	if err != nil && !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		return nil, nil, err
	}
	if t != nil {
		tenantID = t.ID
	}
	p, err := s.loadPrincipal(ctx, u.ID, tenantID, u.TokenVersion)
	if err != nil {
		return nil, nil, err
	}
	pair, err := GenerateTokenPair(*p, s.secret, s.accessTTL, s.refreshTTL)
	if err != nil {
		return nil, nil, err
	}
	recorder, ok := s.store.(LoginRecorder)
	if !ok {
		return nil, nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "login recorder unavailable")
	}
	if err := recorder.RecordSuccessfulLogin(ctx, u.ID, u.TokenVersion); err != nil {
		return nil, nil, err
	}
	return p, pair, nil
}

// Authenticate validates an access token and returns its principal.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	p, err := ValidateAccessToken(token, s.secret)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid or expired access token")
	}
	return s.loadPrincipal(ctx, p.UserID, p.TenantID, p.TokenVersion)
}

// Refresh validates a refresh token and reissues tokens from current stored
// authorization, preserving the tenant explicitly bound to the original token.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	identity, err := validateToken(refreshToken, s.secret, "refresh")
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid or expired refresh token")
	}
	p, err := s.loadPrincipal(ctx, identity.UserID, identity.TenantID, identity.TokenVersion)
	if err != nil {
		return nil, err
	}
	return GenerateTokenPair(*p, s.secret, s.accessTTL, s.refreshTTL)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// SetMeter shares production metering; PG entitlements remain durable catalog facts.
func (s *Service) SetMeter(m usage.PlatformMeter) {
	if m != nil {
		s.meter = m
	}
}
