package auth

import (
	"context"
)

// UserStore 用户中心存储接口（修改密码/邮箱验证/手机号绑定/资料编辑）。
// 与主 Store 分离：主流程（注册/登录）不依赖这些方法，用户中心功能
// 未装配时（nil）相关 Service 方法返回明确错误。
type UserStore interface {
	// GetByID 按 ID 查用户（含用户中心字段）。
	GetByID(ctx context.Context, userID string) (*User, error)
	// GetByPhone 按手机号查用户（不存在返回 ErrNotFound）。
	GetByPhone(ctx context.Context, phone string) (*User, error)
	// UpdatePassword requires the active account and credential version read
	// during password verification. It atomically updates the hash/timestamp
	// and increments token_version/row_version; stale versions return ErrConflict.
	UpdatePassword(ctx context.Context, userID, newHash string, expectedVersion int64) error
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

// VerificationStore owns durable send admission and atomic identity mutation.
// Implementations lock users before credentials. No plaintext read API exists.
type VerificationStore interface {
	ReserveSend(ctx context.Context, purpose, target, ip string, limits VerificationLimits) error
	Issue(ctx context.Context, credential VerificationCredential) error
	RecordDelivery(ctx context.Context, id string, accepted bool) error
	Consume(ctx context.Context, attempt VerificationAttempt) (*User, error)
	UnbindPhone(ctx context.Context, userID string, expectedVersion int64) error
}

// SMSProvider 短信发送接口（pkg/sms 的 Provider 已满足此签名）。
type SMSProvider interface {
	Send(ctx context.Context, to, templateCode string, params map[string]string) error
}

// MailSender 邮件发送接口（pkg/email 的 Provider 适配后注入）。
type MailSender interface {
	Send(ctx context.Context, to, subject, htmlBody string) error
}
