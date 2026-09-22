package auth

// 用户中心 P0 单元测试：修改密码 / 邮箱验证（方案 B）/ 个人资料 / 手机号绑定。

import (
	"context"
	"strings"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type fakeMailSender struct {
	to      string
	subject string
	body    string
	calls   int
}

func (f *fakeMailSender) Send(_ context.Context, to, subject, htmlBody string) error {
	f.to, f.subject, f.body = to, subject, htmlBody
	f.calls++
	return nil
}

type fakeSMS struct {
	to    string
	code  string
	calls int
}

func (f *fakeSMS) Send(_ context.Context, to, _ string, params map[string]string) error {
	f.to = to
	f.code = params["code"]
	f.calls++
	return nil
}

func newUCService(t *testing.T) (*Service, *MemoryStore, *MemoryVerificationStore) {
	t.Helper()
	users := NewMemoryStore()
	svc := NewService(NewMemoryStore(), "test-secret", "15m", "720h")
	verifs := NewMemoryVerificationStore()
	svc.EnableUserCenter(users, verifs, nil, nil, "https://test.example.com")
	return svc, users, verifs
}

func seedUser(t *testing.T, users *MemoryStore, id, email, password string) {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.CreateUser(context.Background(), User{ID: id, Email: email, PasswordHash: hash, Name: "Tester"}); err != nil {
		t.Fatal(err)
	}
}

func TestChangePasswordHappyPath(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "oldpass1")

	if err := svc.ChangePassword(context.Background(), "u1", "oldpass1", "newpass99"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	u, _ := users.GetByID(context.Background(), "u1")
	if VerifyPassword(u.PasswordHash, "oldpass1") {
		t.Error("旧密码仍可验证通过")
	}
	if !VerifyPassword(u.PasswordHash, "newpass99") {
		t.Error("新密码验证失败")
	}
	if u.PasswordChangedAt == nil {
		t.Error("password_changed_at 未记录")
	}
}

func TestChangePasswordRejectsWrongOldPassword(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "oldpass1")

	err := svc.ChangePassword(context.Background(), "u1", "wrongpass", "newpass99")
	if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Fatalf("期望 401，得到 %v", err)
	}
}

func TestChangePasswordRejectsWeakPasswords(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "oldpass1")

	cases := map[string]string{
		"太短无数字":   "ab12",     // <8 位
		"纯数字":     "12345678", // 无字母
		"纯字母":     "abcdefgh", // 无数字
		"含空白的强密码": "abcd 123", // 含空格
	}
	for name, pwd := range cases {
		if err := svc.ChangePassword(context.Background(), "u1", "oldpass1", pwd); err == nil {
			t.Errorf("%s: 弱密码 %q 应被拒绝", name, pwd)
		}
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")
	mail := &fakeMailSender{}
	svc.emailSender = mail

	// 1. 发送验证邮件
	if err := svc.SendVerificationEmail(context.Background(), "u1", "https://test.example.com"); err != nil {
		t.Fatalf("SendVerificationEmail: %v", err)
	}
	if mail.to != "a@test.com" || !strings.Contains(mail.body, "https://test.example.com/verify-email?token=") {
		t.Fatalf("邮件内容不符：to=%s body contains link=%v", mail.to, strings.Contains(mail.body, "verify-email?token="))
	}

	// 2. 从 fake 存储取出 token（重新生成一次不可行——从 verification store 枚举不了，
	//    所以改用 VerifyEmail 走完整链路：直接从 body 提取）
	start := strings.Index(mail.body, "token=") + len("token=")
	token := mail.body[start:]
	if end := strings.Index(token, "\""); end > 0 {
		token = token[:end]
	}
	if end := strings.Index(token, "<"); end > 0 {
		token = token[:end]
	}
	if token == "" {
		t.Fatal("未能从邮件中提取 token")
	}

	// 3. 验证
	if err := svc.VerifyEmail(context.Background(), token); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	u, _ := users.GetByID(context.Background(), "u1")
	if u.EmailVerifiedAt == nil {
		t.Error("email_verified_at 未标记")
	}

	// 4. token 一次性：重复使用应 404
	if err := svc.VerifyEmail(context.Background(), token); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Fatalf("重复使用 token 应 404，得到 %v", err)
	}
}

func TestSendVerificationEmailRejectsAlreadyVerified(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")
	users.MarkEmailVerified(context.Background(), "u1")
	svc.emailSender = &fakeMailSender{}

	if err := svc.SendVerificationEmail(context.Background(), "u1", "https://t.com"); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Fatalf("已验证账号应 409，得到 %v", err)
	}
}

func TestSendVerificationEmailFailsClosedWithoutMailSender(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")

	if err := svc.SendVerificationEmail(context.Background(), "u1", "https://t.com"); err == nil {
		t.Fatal("未配置邮件服务应报错（fail-closed）")
	}
}

func TestTrialAnalysisConsumedOnce(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")

	first, err := svc.ConsumeTrialAnalysis(context.Background(), "u1")
	if err != nil || !first {
		t.Fatalf("首次试用应成功，got %v %v", first, err)
	}
	second, err := svc.ConsumeTrialAnalysis(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("方案 B：试用只有 1 次，第二次应失败")
	}
}

func TestPhoneBindUnbindFlow(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")
	sms := &fakeSMS{}
	svc.smsSender = sms

	// 1. 发送验证码
	if err := svc.SendPhoneCode(context.Background(), "u1", "13800138000"); err != nil {
		t.Fatalf("SendPhoneCode: %v", err)
	}

	// 2. 错误验证码
	if err := svc.BindPhone(context.Background(), "u1", "13800138000", "000000"); err == nil {
		t.Fatal("错误验证码应失败")
	}

	// 3. 正确验证码绑定
	if err := svc.BindPhone(context.Background(), "u1", "13800138000", sms.code); err != nil {
		t.Fatalf("BindPhone: %v", err)
	}
	u, _ := users.GetByID(context.Background(), "u1")
	if u.Phone != "13800138000" || u.PhoneVerifiedAt == nil {
		t.Fatalf("手机号未绑定：phone=%q", u.Phone)
	}

	// 4. 解绑需要密码
	if err := svc.UnbindPhone(context.Background(), "u1", "wrongpass"); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Fatalf("错误密码解绑应 401，得到 %v", err)
	}

	// 5. 邮箱未验证时禁止解绑（唯一登录方式保护）
	if err := svc.UnbindPhone(context.Background(), "u1", "pass1234"); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Fatalf("邮箱未验证时解绑应 409，得到 %v", err)
	}

	// 6. 验证邮箱后解绑成功
	users.MarkEmailVerified(context.Background(), "u1")
	if err := svc.UnbindPhone(context.Background(), "u1", "pass1234"); err != nil {
		t.Fatalf("验证邮箱后解绑应成功: %v", err)
	}
}

func TestSendPhoneCodeRejectsInvalidPhone(t *testing.T) {
	svc, _, _ := newUCService(t)
	seedUser(t, svc.userStore.(*MemoryStore), "u1", "a@test.com", "pass1234")
	svc.smsSender = &fakeSMS{}

	if err := svc.SendPhoneCode(context.Background(), "u1", "12345"); err == nil {
		t.Fatal("非法手机号应被拒绝")
	}
}

func TestPhoneCannotBeBoundByTwoAccounts(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")
	seedUser(t, users, "u2", "b@test.com", "pass1234")
	sms := &fakeSMS{}
	svc.smsSender = sms

	svc.SendPhoneCode(context.Background(), "u1", "13800138000")
	if err := svc.BindPhone(context.Background(), "u1", "13800138000", sms.code); err != nil {
		t.Fatal(err)
	}

	// u2 尝试绑定同一手机号
	sms2 := &fakeSMS{}
	svc.smsSender = sms2
	err := svc.SendPhoneCode(context.Background(), "u2", "13800138000")
	if err == nil {
		t.Fatal("手机号已被占用，发送验证码应被拒绝")
	}
}

func TestProfileMaskingAndRules(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")

	// 更新昵称
	if err := svc.UpdateProfile(context.Background(), "u1", "新昵称", "Asia/Shanghai"); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	// 昵称过短
	if err := svc.UpdateProfile(context.Background(), "u1", "x", ""); err == nil {
		t.Fatal("过短昵称应被拒绝")
	}

	// 资料脱敏
	prof, err := svc.GetProfile(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if prof["password_hash"] != nil {
		t.Error("profile 不得泄露密码哈希")
	}
	if prof["email_verified"] != false {
		t.Error("email_verified 应为 false")
	}
}

func TestUserCenterFailsClosedWhenNotConfigured(t *testing.T) {
	// 未调用 EnableUserCenter：所有用户中心方法必须报错而非 panic
	svc := NewService(NewMemoryStore(), "secret", "15m", "720h")
	ctx := context.Background()

	if err := svc.ChangePassword(ctx, "u1", "a", "b"); err == nil {
		t.Error("ChangePassword 应 fail-closed")
	}
	if _, err := svc.ConsumeTrialAnalysis(ctx, "u1"); err == nil {
		t.Error("ConsumeTrialAnalysis 应 fail-closed")
	}
	if _, err := svc.GetProfile(ctx, "u1"); err == nil {
		t.Error("GetProfile 应 fail-closed")
	}
}

func TestTokenIsTwentyFourDigits(t *testing.T) {
	tok := newToken()
	if len(tok) != 24 {
		t.Fatalf("邮箱验证 token 应 24 位数字，得到 %d 位", len(tok))
	}
	_ = time.Now() // 保持 time 导入（ smsCodeTTL 使用）
}

// ─── 补充覆盖（2026-09-22 测试验证轮） ──────────────────────────

func TestBindPhoneConsumesCodeAfterSuccess(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")
	sms := &fakeSMS{}
	svc.smsSender = sms

	if err := svc.SendPhoneCode(context.Background(), "u1", "13800138000"); err != nil {
		t.Fatalf("SendPhoneCode: %v", err)
	}
	if err := svc.BindPhone(context.Background(), "u1", "13800138000", sms.code); err != nil {
		t.Fatalf("BindPhone: %v", err)
	}
	// 同码二次绑定必须失败：验证码已消费（防重放）
	if err := svc.BindPhone(context.Background(), "u1", "13800138000", sms.code); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		t.Fatalf("绑定成功后同码二次 bind 应 401，得到 %v", err)
	}
}

func TestVerificationExpiryBoundaries(t *testing.T) {
	_, _, verifs := newUCService(t)
	ctx := context.Background()

	// 邮箱 token 过期边界：过期条目 Load 视为不存在（MemoryVerificationStore expiresAt 判断）
	if err := verifs.SaveEmailToken(ctx, "expired-tok", "u1", -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := verifs.LoadEmailToken(ctx, "expired-tok"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Fatalf("过期 email token 应 404，得到 %v", err)
	}
	if err := verifs.SaveEmailToken(ctx, "live-tok", "u1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if uid, err := verifs.LoadEmailToken(ctx, "live-tok"); err != nil || uid != "u1" {
		t.Fatalf("未过期 email token 应可读取，got %q %v", uid, err)
	}

	// 短信验证码过期边界
	if err := verifs.SaveSMSCode(ctx, "13800138000", "bind", "123456", -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := verifs.LoadSMSCode(ctx, "13800138000", "bind"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Fatalf("过期短信验证码应 404，得到 %v", err)
	}
}

func TestGetProfilePhoneMasking(t *testing.T) {
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "pass1234")
	ctx := context.Background()

	// 未绑定手机号：phone 原样输出空串（maskPhone 对非 11 位不加工），phone_verified=false
	prof, err := svc.GetProfile(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if prof["phone"] != "" {
		t.Errorf("未绑定手机号时 phone 应为空串，得到 %q", prof["phone"])
	}
	if prof["phone_verified"] != false {
		t.Error("未绑定时 phone_verified 应为 false")
	}

	// 绑定后脱敏输出：3+4+4 掩码
	if err := users.SetPhone(ctx, "u1", "13800138000"); err != nil {
		t.Fatal(err)
	}
	prof, err = svc.GetProfile(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if prof["phone"] != "138****8000" {
		t.Errorf("phone 应脱敏为 138****8000，得到 %q", prof["phone"])
	}
	if prof["phone_verified"] != true {
		t.Error("绑定后 phone_verified 应为 true")
	}
}

func TestChangePasswordTokenLifecycle(t *testing.T) {
	// MVP 不撤销旧 token（无服务端会话表可撤销）——锁定现状语义：
	// 旧密码立即失效、新密码可登录；旧 refresh token 因 JWT 无状态仍可换新
	// （不 crash），强制重登由客户端丢弃 token 实现（handler 注释承诺）。
	svc, users, _ := newUCService(t)
	seedUser(t, users, "u1", "a@test.com", "oldpass1")
	ctx := context.Background()

	// 构造改密前签发的旧 token 对（Refresh 仅验签不查 store，与主 store 数据无关）
	pair, err := GenerateTokenPair(Principal{UserID: "u1", Email: "a@test.com"}, "test-secret", "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, pair.AccessToken); err != nil {
		t.Fatalf("改密前旧 access token 应有效: %v", err)
	}

	if err := svc.ChangePassword(ctx, "u1", "oldpass1", "newpass99"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// 旧 refresh token 仍可换新：MVP 不撤销（现状锁定，实现撤销时更新本测试）
	if _, err := svc.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("旧 refresh token 在 MVP 下不应 crash: %v", err)
	}
	// 改密后旧 access token 仍验签通过（同上，无 crash）
	if _, err := svc.Authenticate(ctx, pair.AccessToken); err != nil {
		t.Fatalf("旧 access token 在 MVP 下不应 crash: %v", err)
	}
}
