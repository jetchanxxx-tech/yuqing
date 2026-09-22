package auth

// 用户中心 P0：修改密码、邮箱验证（方案 B 试用 1 次）、个人资料、手机号绑定。
// 全部方法对未装配的依赖 fail-closed（返回明确错误，不 panic）。

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// passwordStrength 用户决策 2026-09-22：≥8 位 + 字母 + 数字。
// Go RE2 不支持 (?=) 前瞻，拆成三条正则组合判断。
var (
	pwdMinLength = regexp.MustCompile(`^\S{8,}$`)
	pwdHasLetter = regexp.MustCompile(`[A-Za-z]`)
	pwdHasDigit  = regexp.MustCompile(`\d`)
)

func isStrongPassword(p string) bool {
	return pwdMinLength.MatchString(p) && pwdHasLetter.MatchString(p) && pwdHasDigit.MatchString(p)
}

// chinaPhoneRe 中国大陆手机号。
var chinaPhoneRe = regexp.MustCompile(`^1[3-9]\d{9}$`)

// 验证码/token 有效期。
const (
	smsCodeTTL    = 5 * time.Minute
	emailTokenTTL = 24 * time.Hour
)

// requireDeps 返回用户中心依赖，未装配时报错。
func (s *Service) requireDeps() error {
	if s.userStore == nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "user center store not configured")
	}
	return nil
}

// ChangePassword 修改密码：验证旧密码 → 强度校验（≥8 位字母+数字）→ 更新哈希。
// 调用方（handler）负责在成功后撤销 refresh token 强制重登。
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	if err := s.requireDeps(); err != nil {
		return err
	}
	if !isStrongPassword(newPassword) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "password must be 8+ characters with letters and digits")
	}
	u, err := s.userStore.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !VerifyPassword(u.PasswordHash, oldPassword) {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "old password incorrect")
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.userStore.UpdatePassword(ctx, userID, newHash)
}

// SendVerificationEmail 发送邮箱验证邮件。防刷依赖 VerificationStore 的
// key 覆盖语义（同 key 5 分钟窗口内由 handler 层限流）。
func (s *Service) SendVerificationEmail(ctx context.Context, userID, verifyBaseURL string) error {
	if err := s.requireDeps(); err != nil {
		return err
	}
	if s.emailSender == nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "email service not configured")
	}
	u, err := s.userByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt != nil {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "email already verified")
	}
	token := newToken()
	if err := s.verifications.SaveEmailToken(ctx, token, userID, emailTokenTTL); err != nil {
		return err
	}
	link := fmt.Sprintf("%s/verify-email?token=%s", strings.TrimRight(verifyBaseURL, "/"), token)
	html := buildVerifyEmailHTML(u.Name, link)
	return s.emailSender.Send(ctx, u.Email, "验证您的邮箱 - 盘古舆情", html)
}

// VerifyEmail 消费验证 token，标记邮箱已验证。token 一次性。
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	if err := s.requireDeps(); err != nil {
		return err
	}
	userID, err := s.verifications.LoadEmailToken(ctx, token)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "invalid or expired verification link")
	}
	if err := s.userStore.MarkEmailVerified(ctx, userID); err != nil {
		return err
	}
	return s.verifications.ConsumeEmailToken(ctx, token)
}

// EmailVerified 查询邮箱是否已验证（创建分析前的试用额度判断用）。
func (s *Service) EmailVerified(ctx context.Context, userID string) (bool, error) {
	if err := s.requireDeps(); err != nil {
		return false, err
	}
	u, err := s.userByID(ctx, userID)
	if err != nil {
		return false, err
	}
	return u.EmailVerifiedAt != nil, nil
}

// ConsumeTrialAnalysis 方案 B：邮箱未验证用户允许试用 1 次。
// 原子扣减 trial_analysis_used（0→1），已用完返回 false。
func (s *Service) ConsumeTrialAnalysis(ctx context.Context, userID string) (bool, error) {
	if err := s.requireDeps(); err != nil {
		return false, err
	}
	return s.userStore.ConsumeTrialAnalysis(ctx, userID)
}

// GetProfile 返回当前用户资料（脱敏：不含密码哈希）。
func (s *Service) GetProfile(ctx context.Context, userID string) (map[string]any, error) {
	if err := s.requireDeps(); err != nil {
		return nil, err
	}
	u, err := s.userByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":                  u.ID,
		"email":               u.Email,
		"name":                u.Name,
		"avatar_url":          u.AvatarURL,
		"timezone":            u.Timezone,
		"phone":               maskPhone(u.Phone),
		"email_verified":      u.EmailVerifiedAt != nil,
		"phone_verified":      u.PhoneVerifiedAt != nil,
		"trial_used":          u.TrialAnalysisUsed,
		"password_changed_at": formatTime(u.PasswordChangedAt),
	}, nil
}

// UpdateProfile 更新昵称/时区（头像走 UpdateAvatar）。
func (s *Service) UpdateProfile(ctx context.Context, userID, name, timezone string) error {
	if err := s.requireDeps(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name != "" && (len(name) < 2 || len(name) > 20) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "name must be 2-20 characters")
	}
	return s.userStore.UpdateProfile(ctx, userID, name, "", timezone)
}

// SendPhoneCode 发送手机验证码（绑定场景）。60 秒防刷由 handler 层限流。
func (s *Service) SendPhoneCode(ctx context.Context, userID, phone string) error {
	if err := s.requireDeps(); err != nil {
		return err
	}
	if s.smsSender == nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "sms service not configured")
	}
	if !chinaPhoneRe.MatchString(phone) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "invalid phone number")
	}
	// 手机号被其他账号占用检查
	if existing, err := s.userByPhone(ctx, phone); err == nil && existing != nil && existing.ID != userID {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "phone already bound to another account")
	}
	code := newSMSCode()
	if err := s.verifications.SaveSMSCode(ctx, phone, "bind", code, smsCodeTTL); err != nil {
		return err
	}
	return s.smsSender.Send(ctx, phone, "SMS_BIND_PHONE", map[string]string{"code": code})
}

// BindPhone 校验验证码并绑定手机号。
func (s *Service) BindPhone(ctx context.Context, userID, phone, code string) error {
	if err := s.requireDeps(); err != nil {
		return err
	}
	stored, err := s.verifications.LoadSMSCode(ctx, phone, "bind")
	if err != nil || stored != code {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid or expired code")
	}
	if err := s.userStore.SetPhone(ctx, userID, phone); err != nil {
		return err
	}
	return s.verifications.ConsumeSMSCode(ctx, phone, "bind")
}

// UnbindPhone 解绑手机号：需验证登录密码（防会话劫持）。
// 唯一登录方式保护：邮箱未验证时不允许解绑。
func (s *Service) UnbindPhone(ctx context.Context, userID, password string) error {
	if err := s.requireDeps(); err != nil {
		return err
	}
	u, err := s.userByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Phone == "" {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "no phone bound")
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "password incorrect")
	}
	if u.EmailVerifiedAt == nil {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "verify your email first: phone is your only login method")
	}
	return s.userStore.ClearPhone(ctx, userID)
}

// ─── 内部辅助 ───────────────────────────────────────────────

func (s *Service) userByID(ctx context.Context, userID string) (*User, error) {
	return s.userStore.GetByID(ctx, userID)
}

func (s *Service) userByPhone(ctx context.Context, phone string) (*User, error) {
	return s.userStore.GetByPhone(ctx, phone)
}

func newToken() string {
	return newSMSCode() + newSMSCode() + newSMSCode() + newSMSCode()
}

func newSMSCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

func maskPhone(phone string) string {
	if len(phone) != 11 {
		return phone
	}
	return phone[:3] + "****" + phone[7:]
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func buildVerifyEmailHTML(name, link string) string {
	if name == "" {
		name = "用户"
	}
	return fmt.Sprintf(`<!DOCTYPE html><html><body style="font-family:sans-serif;line-height:1.6">
<div style="max-width:560px;margin:0 auto;padding:24px">
<h2 style="color:#4f46e5">盘古舆情</h2>
<p>您好，%s！</p>
<p>请点击下方按钮验证您的邮箱地址：</p>
<p><a href="%s" style="display:inline-block;background:#4f46e5;color:#fff;padding:12px 32px;border-radius:6px;text-decoration:none">验证邮箱</a></p>
<p style="font-size:13px;color:#64748b">或复制链接到浏览器：<br><code>%s</code></p>
<p style="font-size:13px;color:#64748b">此链接 24 小时内有效。如果您没有注册盘古舆情，请忽略此邮件。</p>
</div></body></html>`, name, link, link)
}
