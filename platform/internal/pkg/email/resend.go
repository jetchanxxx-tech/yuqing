package email

import (
	"context"
	"fmt"
	"github.com/yuqing/platform/internal/pkg/notification"
	"net/http"
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

// SendVerificationEmail sends through the same context-aware SDK path.
func (p *ResendProvider) SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error {
	body := strings.ReplaceAll(verificationEmailHTML, "{{.Name}}", name)
	return p.SendRaw(ctx, to, "验证您的邮箱 - 盘古舆情", strings.ReplaceAll(body, "{{.VerifyURL}}", verifyURL))
}
func (p *ResendProvider) SendPasswordResetEmail(ctx context.Context, to, name, resetURL string) error {
	body := strings.ReplaceAll(passwordResetEmailHTML, "{{.Name}}", name)
	return p.SendRaw(ctx, to, "重置您的密码 - 盘古舆情", strings.ReplaceAll(body, "{{.ResetURL}}", resetURL))
}
func (p *ResendProvider) SendTestEmail(ctx context.Context, to string) error {
	return p.SendRaw(ctx, to, "邮件服务测试 - 盘古舆情", strings.ReplaceAll(testEmailHTML, "{{.Timestamp}}", time.Now().Format("2006-01-02 15:04:05")))
}
func (p *ResendProvider) SendRaw(ctx context.Context, to, subject, body string) error {
	_, err := p.SendRawReceipt(ctx, to, subject, body)
	return err
}

// The pinned SDK has no context argument on Emails.Send. Its request and
// response methods preserve the vendor contract while attaching our context.
func (p *ResendProvider) SendRawReceipt(ctx context.Context, to, subject, body string) (notification.Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return notification.Receipt{}, err
	}
	req, err := p.client.NewRequest(http.MethodPost, "emails", &resend.SendEmailRequest{From: fmt.Sprintf("%s <%s>", p.fromName, p.fromAddress), To: []string{to}, Subject: subject, Html: body})
	if err != nil {
		return notification.Receipt{}, fmt.Errorf("resend request could not be prepared")
	}
	response := new(resend.SendEmailResponse)
	_, err = p.client.Perform(req.WithContext(ctx), response)
	if err != nil {
		if ctx.Err() != nil {
			return notification.Receipt{}, ctx.Err()
		}
		return notification.Receipt{}, fmt.Errorf("resend delivery was not accepted")
	}
	if strings.TrimSpace(response.Id) == "" {
		return notification.Receipt{}, fmt.Errorf("resend acceptance receipt missing")
	}
	return notification.Accepted("resend", response.Id), nil
}
