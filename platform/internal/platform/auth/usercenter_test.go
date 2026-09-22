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
