package auth

import (
	"context"
	"time"
)

// UserStore 用户中心存储接口（修改密码/邮箱验证/手机号绑定/资料编辑）。
// 与主 Store 分离：主流程（注册/登录）不依赖这些方法，用户中心功能
// 未装配时（nil）相关 Service 方法返回明确错误。
type UserStore interface {
	// GetByID 按 ID 查用户（含用户中心字段）。
	GetByID(ctx context.Context, userID string) (*User, error)
	// GetByPhone 按手机号查用户（不存在返回 ErrNotFound）。
	GetByPhone(ctx context.Context, phone string) (*User, error)
	// UpdatePassword 更新密码哈希并记录 password_changed_at。
	UpdatePassword(ctx context.Context, userID, newHash string) error
	// MarkEmailVerified 标记邮箱已验证。
	MarkEmailVerified(ctx context.Context, userID string) error
	// UpdateProfile 更新昵称/头像/时区（空串字段跳过）。
	UpdateProfile(ctx context.Context, userID, name, avatarURL, timezone string) error
	// SetPhone 绑定手机号（其他账号已绑定时返回 ErrConflict）。
	SetPhone(ctx context.Context, userID, phone string) error
	// ClearPhone 解绑手机号。
	ClearPhone(ctx context.Context, userID string) error
	// ConsumeTrialAnalysis 原子消耗 1 次试用（0→1），返回是否成功。
	ConsumeTrialAnalysis(ctx context.Context, userID string) (bool, error)
}

// VerificationStore 验证凭据存储（邮箱 token + 短信验证码），语义化接口。
// MVP：内存实现；生产：PG 实现（verification_tokens / sms_verification_codes 表），
// 长期可换 Redis（TTL 原生）。过期或不存在一律返回 ErrNotFound。
type VerificationStore interface {
	// SaveEmailToken 保存邮箱验证 token → userID 映射（同 token 覆盖）。
	SaveEmailToken(ctx context.Context, token, userID string, ttl time.Duration) error
	// LoadEmailToken 读取 token 对应的 userID。
	LoadEmailToken(ctx context.Context, token string) (string, error)
	// ConsumeEmailToken 消费 token（一次性）。
	ConsumeEmailToken(ctx context.Context, token string) error

	// SaveSMSCode 保存某手机号某用途的验证码（同手机号覆盖旧码）。
	SaveSMSCode(ctx context.Context, phone, purpose, code string, ttl time.Duration) error
	// LoadSMSCode 读取验证码（供 service 比对）。
	LoadSMSCode(ctx context.Context, phone, purpose string) (string, error)
	// ConsumeSMSCode 消费验证码（绑定成功后删除）。
	ConsumeSMSCode(ctx context.Context, phone, purpose string) error
}

// SMSProvider 短信发送接口（pkg/sms 的 Provider 已满足此签名）。
type SMSProvider interface {
	Send(ctx context.Context, to, templateCode string, params map[string]string) error
}

// MailSender 邮件发送接口（pkg/email 的 Provider 适配后注入）。
type MailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}
