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

// rolePlatformAdmin 授予平台级管理权限（/admin/* 全部端点）。
// MVP 内存 store 下无法通过 CLI/DB 直接写入，只能由引导邮箱注册时获得。
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
	meter      *usage.Meter

	// bootstrapAdminEmail: 用该邮箱注册的用户额外获得 platform_admin 角色。
	// 空值表示不启用（默认）。见 SetBootstrapAdminEmail。
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

	// 防刷（进程内；单实例部署语义，多实例时换 Redis）
	sendGate  senderThrottle // 发码节流：同目标 60s 一次
	codeTries codeTries      // 验证码错误尝试计数（≥5 次作废）
}

// EnableUserCenter 装配用户中心 P0 依赖（组合根调用）。
func (s *Service) EnableUserCenter(users UserStore, verifications VerificationStore, sms SMSProvider, mail MailSender, verifyBaseURL string) {
	s.userStore = users
	s.verifications = verifications
	s.smsSender = sms
	s.emailSender = mail
	s.verifyBaseURL = verifyBaseURL
}

// SetPostRegister 挂接注册后回调。
func (s *Service) SetPostRegister(fn func(ctx context.Context, tenantID string) error) {
	s.postRegister = fn
}

// SetBootstrapAdminEmail 配置引导管理员邮箱（大小写不敏感）。
// 用于解决「内存 store 下无法创建平台管理员」的引导问题：设好后，
// 用该邮箱注册的账号即为平台管理员，可访问 /admin/* 全部端点。
func (s *Service) SetBootstrapAdminEmail(email string) {
	s.bootstrapAdminEmail = normalizeEmail(email)
}

// rolesFor 返回用户的完整角色集：基础角色 + 命中引导邮箱时的 platform_admin。
//
// Register 与 Login 必须共用此逻辑。成员表只存基础角色，若 Login 仅回显该
// 角色，用户重新登录后 platform_admin 会丢失、管理后台再次 403（实测踩过）。
func (s *Service) rolesFor(email, baseRole string) []string {
	roles := []string{baseRole}
	if s.bootstrapAdminEmail != "" && normalizeEmail(email) == s.bootstrapAdminEmail {
		roles = append(roles, rolePlatformAdmin)
	}
	return roles
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

	// 注册后钩子（试用额度发放等）。尽力而为：失败不阻断注册。
	if s.postRegister != nil {
		if err := s.postRegister(ctx, tenantID); err != nil {
			// 试用额度发放失败只影响体验，账号/租户已创建成功
			_ = err
		}
	}

	// Free plan: 1M token hard cap applied at provisioning time.
	s.meter.SetQuota(tenantID, freePlanTokenQuota, llm.BudgetHardCap)

	// 引导管理员：邮箱命中配置时额外授予 platform_admin
	roles := s.rolesFor(email, roleTenantAdmin)

	p := &Principal{
		UserID:       userID,
		TenantID:     tenantID,
		Email:        email,
		Roles:        roles,
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
		Roles:        s.rolesFor(u.Email, role),
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
