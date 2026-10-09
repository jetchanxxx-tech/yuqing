package auth

// 用户中心 P0：修改密码、邮箱验证（方案 B 试用 1 次）、个人资料、手机号绑定。
// 全部方法对未装配的依赖 fail-closed（返回明确错误，不 panic）。

import (
	"context"
	"fmt"
	"html"
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

// 验证码/token 有效期与防刷窗口。
const (
	smsCodeTTL    = 5 * time.Minute
	emailTokenTTL = 24 * time.Hour
	sendWindow    = 60 * time.Second // 发码节流：同目标 60s 一次
	maxCodeTries  = 5                // 单验证码最多尝试次数，超限作废
)

// requireDeps 返回用户中心依赖，未装配时报错。
func (s *Service) requireDeps() error {
	if s.userStore == nil {
		return pkgerrors.Wrap(pkgerrors.ErrInternal, "user center store not configured")
	}
	return nil
}

// VerifyBaseURL 返回组合根注入的站点基地址（邮箱验证链接用）；
// 必须为受信任配置；禁止回退到请求 Host。
func (s *Service) VerifyBaseURL() string { return s.verifyBaseURL }

// ChangePassword 修改密码：验证旧密码 → 强度校验（≥8 位字母+数字）→ 更新哈希。
// Store 更新哈希时原子增加账号版本，旧 access/refresh 在服务端失效。
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string, versions ...int64) error {
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
	if u.Status != "active" || (len(versions) > 0 && u.TokenVersion != versions[0]) {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "account unavailable")
	}
	if !VerifyPassword(u.PasswordHash, oldPassword) {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "old password incorrect")
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.userStore.UpdatePassword(ctx, userID, newHash, u.TokenVersion)
}

// SendVerificationEmail uses only the configured public origin. The retained
// baseURL argument is ignored so request headers cannot choose credential links.
func (s *Service) SendVerificationEmail(ctx context.Context, userID, _ string, versions ...int64) error {
	if err := s.verificationReady(ctx, EmailVerify); err != nil {
		return err
	}
	u, err := s.verificationUser(ctx, userID, versions)
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt != nil {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "email already verified")
	}
	target, err := verificationTarget(EmailVerify, u.Email)
	if err != nil {
		return err
	}
	if err = s.verifications.ReserveSend(ctx, EmailVerify, target, "", s.verificationLimits); err != nil {
		return err
	}
	return s.issueVerification(ctx, u, EmailVerify, target)
}
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	_, err := s.ConfirmVerification(ctx, EmailVerify, "", token, "", nil)
	if pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "invalid or expired verification link")
	}
	return err
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
	// 按 rune 计数（中文一字 = 一符），避免多字节误判长度
	if n := len([]rune(name)); name != "" && (n < 2 || n > 20) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "name must be 2-20 characters")
	}
	return s.userStore.UpdateProfile(ctx, userID, name, "", timezone)
}

// SendPhoneCode preserves the existing first-bind service entry point.
func (s *Service) SendPhoneCode(ctx context.Context, userID, phone string, versions ...int64) error {
	return s.sendPhoneCode(ctx, userID, phone, "", versions)
}

// SendPhoneCodeWithPassword permits changing an existing binding only after
// checking the password and retaining the original authenticated version.
func (s *Service) SendPhoneCodeWithPassword(ctx context.Context, userID, phone, password string, version int64) error {
	return s.sendPhoneCode(ctx, userID, phone, password, []int64{version})
}
func (s *Service) sendPhoneCode(ctx context.Context, userID, phone, password string, versions []int64) error {
	if err := s.verificationReady(ctx, PhoneBind); err != nil {
		return err
	}
	phone, err := verificationTarget(PhoneBind, phone)
	if err != nil {
		return err
	}
	u, err := s.verificationUser(ctx, userID, versions)
	if err != nil {
		return err
	}
	if u.Phone != "" && !VerifyPassword(u.PasswordHash, password) {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "current password required to change phone")
	}
	if existing, err := s.userByPhone(ctx, phone); err == nil && existing.ID != userID {
		return pkgerrors.ErrConflict
	} else if err != nil && !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		return err
	}
	if err = s.verifications.ReserveSend(ctx, PhoneBind, phone, "", s.verificationLimits); err != nil {
		return err
	}
	return s.issueVerification(ctx, u, PhoneBind, phone)
}
func (s *Service) BindPhone(ctx context.Context, userID, phone, code string, versions ...int64) error {
	u, err := s.verificationUser(ctx, userID, versions)
	if err != nil {
		return err
	}
	_, err = s.ConfirmVerification(ctx, PhoneBind, phone, code, "", &Principal{UserID: userID, TokenVersion: u.TokenVersion})
	return err
}
func (s *Service) UnbindPhone(ctx context.Context, userID, password string, versions ...int64) error {
	u, err := s.verificationUser(ctx, userID, versions)
	if err != nil {
		return err
	}
	if u.Phone == "" {
		return pkgerrors.ErrNotFound
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "password incorrect")
	}
	if u.EmailVerifiedAt == nil {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "verify your email before removing phone")
	}
	if s.verifications == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	return s.verifications.UnbindPhone(ctx, userID, u.TokenVersion)
}

// RequestEmailChange is the K7 authenticated issuance port. Confirmation uses
// ConfirmVerification with the same authenticated immutable user identity.
func (s *Service) RequestEmailChange(ctx context.Context, actor Principal, password, target string) error {
	if err := s.verificationReady(ctx, EmailChange); err != nil {
		return err
	}
	target, err := verificationTarget(EmailChange, target)
	if err != nil {
		return err
	}
	u, err := s.verificationUser(ctx, actor.UserID, []int64{actor.TokenVersion})
	if err != nil {
		return err
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return pkgerrors.ErrUnauthorized
	}
	if strings.EqualFold(u.Email, target) {
		return pkgerrors.ErrConflict
	}
	if err = s.verifications.ReserveSend(ctx, EmailChange, target, "", s.verificationLimits); err != nil {
		return err
	}
	return s.issueVerification(ctx, u, EmailChange, target)
}

// ─── 内部辅助 ───────────────────────────────────────────────

func (s *Service) userByID(ctx context.Context, userID string) (*User, error) {
	return s.userStore.GetByID(ctx, userID)
}

func (s *Service) userByPhone(ctx context.Context, phone string) (*User, error) {
	return s.userStore.GetByPhone(ctx, phone)
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
	// 昵称来自用户输入，插 HTML 前转义（防 HTML 注入）；link 由服务端拼装
	safeName := html.EscapeString(name)
	return fmt.Sprintf(`<!DOCTYPE html><html><body style="font-family:sans-serif;line-height:1.6">
<div style="max-width:560px;margin:0 auto;padding:24px">
<h2 style="color:#4f46e5">盘古舆情</h2>
<p>您好，%s！</p>
<p>请点击下方按钮验证您的邮箱地址：</p>
<p><a href="%s" style="display:inline-block;background:#4f46e5;color:#fff;padding:12px 32px;border-radius:6px;text-decoration:none">验证邮箱</a></p>
<p style="font-size:13px;color:#64748b">或复制链接到浏览器：<br><code>%s</code></p>
<p style="font-size:13px;color:#64748b">此链接 24 小时内有效。如果您没有注册盘古舆情，请忽略此邮件。</p>
</div></body></html>`, safeName, link, link)
}
