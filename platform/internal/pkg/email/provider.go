package email

import (
	"context"
	"fmt"
)

// Provider 邮件发送接口
type Provider interface {
	// SendVerificationEmail 发送邮箱验证邮件
	SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error

	// SendPasswordResetEmail 发送密码重置邮件
	SendPasswordResetEmail(ctx context.Context, to, name, resetURL string) error

	// SendTestEmail 发送测试邮件（管理后台配置验证）
	SendTestEmail(ctx context.Context, to string) error
}

// Config 邮件服务配置
type Config struct {
	Provider    string // resend | smtp
	FromAddress string
	FromName    string

	// Resend 配置
	ResendAPIKey string

	// SMTP 配置（fallback）
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
}

// NewProvider 根据配置创建邮件服务提供商
func NewProvider(cfg Config) (Provider, error) {
	if cfg.Provider == "resend" && cfg.ResendAPIKey != "" {
		return NewResendProvider(cfg)
	}

	if cfg.SMTPHost != "" {
		return NewSMTPProvider(cfg)
	}

	return nil, fmt.Errorf("no email provider configured")
}

// 邮件模板
const (
	verificationEmailHTML = `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 30px; text-align: center; border-radius: 8px 8px 0 0; }
        .content { background: #f8fafc; padding: 30px; border-radius: 0 0 8px 8px; }
        .button { display: inline-block; background: #667eea; color: white; padding: 12px 32px; text-decoration: none; border-radius: 6px; margin: 20px 0; }
        .footer { margin-top: 20px; padding-top: 20px; border-top: 1px solid #e2e8f0; font-size: 12px; color: #64748b; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>盘古舆情</h1>
        </div>
        <div class="content">
            <p>您好，{{.Name}}！</p>
            <p>感谢注册盘古舆情。请点击下方按钮验证您的邮箱地址：</p>
            <p style="text-align: center;">
                <a href="{{.VerifyURL}}" class="button">验证邮箱</a>
            </p>
            <p style="font-size: 14px; color: #64748b;">
                或复制以下链接到浏览器：<br>
                <code style="background: #e2e8f0; padding: 4px 8px; border-radius: 4px;">{{.VerifyURL}}</code>
            </p>
            <p style="font-size: 14px; color: #64748b;">
                此链接 24 小时内有效。如果您没有注册盘古舆情，请忽略此邮件。
            </p>
        </div>
        <div class="footer">
            <p>此邮件由系统自动发送，请勿直接回复。</p>
            <p>© 2026 盘古舆情 | <a href="https://pangu-cloud.com">pangu-cloud.com</a></p>
        </div>
    </div>
</body>
</html>
`

	passwordResetEmailHTML = `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background: linear-gradient(135deg, #f59e0b 0%, #dc2626 100%); color: white; padding: 30px; text-align: center; border-radius: 8px 8px 0 0; }
        .content { background: #f8fafc; padding: 30px; border-radius: 0 0 8px 8px; }
        .button { display: inline-block; background: #f59e0b; color: white; padding: 12px 32px; text-decoration: none; border-radius: 6px; margin: 20px 0; }
        .warning { background: #fef3c7; border-left: 4px solid #f59e0b; padding: 12px; margin: 20px 0; }
        .footer { margin-top: 20px; padding-top: 20px; border-top: 1px solid #e2e8f0; font-size: 12px; color: #64748b; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🔐 密码重置</h1>
        </div>
        <div class="content">
            <p>您好，{{.Name}}！</p>
            <p>我们收到了您的密码重置请求。点击下方按钮重置密码：</p>
            <p style="text-align: center;">
                <a href="{{.ResetURL}}" class="button">重置密码</a>
            </p>
            <p style="font-size: 14px; color: #64748b;">
                或复制以下链接到浏览器：<br>
                <code style="background: #e2e8f0; padding: 4px 8px; border-radius: 4px;">{{.ResetURL}}</code>
            </p>
            <div class="warning">
                <strong>⚠️ 安全提示</strong><br>
                此链接 1 小时内有效。如果您没有申请重置密码，请忽略此邮件并检查账户安全。
            </div>
        </div>
        <div class="footer">
            <p>此邮件由系统自动发送，请勿直接回复。</p>
            <p>© 2026 盘古舆情 | <a href="https://pangu-cloud.com">pangu-cloud.com</a></p>
        </div>
    </div>
</body>
</html>
`

	testEmailHTML = `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; line-height: 1.6; color: #333; }
        .container { max-width: 600px; margin: 0 auto; padding: 20px; }
        .header { background: linear-gradient(135deg, #10b981 0%, #059669 100%); color: white; padding: 30px; text-align: center; border-radius: 8px 8px 0 0; }
        .content { background: #f8fafc; padding: 30px; border-radius: 0 0 8px 8px; text-align: center; }
        .success { font-size: 48px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>✅ 邮件服务测试</h1>
        </div>
        <div class="content">
            <div class="success">🎉</div>
            <h2>邮件配置成功！</h2>
            <p>如果您收到此邮件，说明邮件服务已正确配置。</p>
            <p style="font-size: 14px; color: #64748b;">发送时间：{{.Timestamp}}</p>
        </div>
    </div>
</body>
</html>
`
)
