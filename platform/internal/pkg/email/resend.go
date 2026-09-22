package email

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/resendlabs/resend-go"
)

// ResendProvider Resend.com 邮件服务实现
type ResendProvider struct {
	client      *resend.Client
	fromAddress string
	fromName    string
}

// NewResendProvider 创建 Resend 提供商
func NewResendProvider(cfg Config) (*ResendProvider, error) {
	if cfg.ResendAPIKey == "" {
		return nil, fmt.Errorf("resend api key is required")
	}

	client := resend.NewClient(cfg.ResendAPIKey)

	return &ResendProvider{
		client:      client,
		fromAddress: cfg.FromAddress,
		fromName:    cfg.FromName,
	}, nil
}

// SendVerificationEmail 发送邮箱验证邮件
func (p *ResendProvider) SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error {
	html := strings.ReplaceAll(verificationEmailHTML, "{{.Name}}", name)
	html = strings.ReplaceAll(html, "{{.VerifyURL}}", verifyURL)

	params := &resend.SendEmailRequest{
		From:    fmt.Sprintf("%s <%s>", p.fromName, p.fromAddress),
		To:      []string{to},
		Subject: "验证您的邮箱 - 盘古舆情",
		Html:    html,
	}

	_, err := p.client.Emails.Send(params)
	if err != nil {
		return fmt.Errorf("resend send email failed: %w", err)
	}

	return nil
}

// SendPasswordResetEmail 发送密码重置邮件
func (p *ResendProvider) SendPasswordResetEmail(ctx context.Context, to, name, resetURL string) error {
	html := strings.ReplaceAll(passwordResetEmailHTML, "{{.Name}}", name)
	html = strings.ReplaceAll(html, "{{.ResetURL}}", resetURL)

	params := &resend.SendEmailRequest{
		From:    fmt.Sprintf("%s <%s>", p.fromName, p.fromAddress),
		To:      []string{to},
		Subject: "重置您的密码 - 盘古舆情",
		Html:    html,
	}

	_, err := p.client.Emails.Send(params)
	if err != nil {
		return fmt.Errorf("resend send email failed: %w", err)
	}

	return nil
}

// SendTestEmail 发送测试邮件
func (p *ResendProvider) SendTestEmail(ctx context.Context, to string) error {
	html := strings.ReplaceAll(testEmailHTML, "{{.Timestamp}}", time.Now().Format("2006-01-02 15:04:05"))

	params := &resend.SendEmailRequest{
		From:    fmt.Sprintf("%s <%s>", p.fromName, p.fromAddress),
		To:      []string{to},
		Subject: "邮件服务测试 - 盘古舆情",
		Html:    html,
	}

	_, err := p.client.Emails.Send(params)
	if err != nil {
		return fmt.Errorf("resend send test email failed: %w", err)
	}

	return nil
}

// SendRaw 发送自定义 HTML 邮件。
func (p *ResendProvider) SendRaw(_ context.Context, to, subject, htmlBody string) error {
	params := &resend.SendEmailRequest{
		From:    fmt.Sprintf("%s <%s>", p.fromName, p.fromAddress),
		To:      []string{to},
		Subject: subject,
		Html:    htmlBody,
	}
	if _, err := p.client.Emails.Send(params); err != nil {
		return fmt.Errorf("resend send email failed: %w", err)
	}
	return nil
}
