package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/yuqing/platform/internal/pkg/notification"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPProvider SMTP 邮件服务实现（fallback）
type SMTPProvider struct {
	host        string
	port        int
	username    string
	password    string
	fromAddress string
	fromName    string
}

// NewSMTPProvider 创建 SMTP 提供商
func NewSMTPProvider(cfg Config) (*SMTPProvider, error) {
	if cfg.SMTPHost == "" {
		return nil, fmt.Errorf("smtp host is required")
	}

	return &SMTPProvider{
		host:        cfg.SMTPHost,
		port:        cfg.SMTPPort,
		username:    cfg.SMTPUsername,
		password:    cfg.SMTPPassword,
		fromAddress: cfg.FromAddress,
		fromName:    cfg.FromName,
	}, nil
}

// SendVerificationEmail 发送邮箱验证邮件
func (p *SMTPProvider) SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error {
	html := strings.ReplaceAll(verificationEmailHTML, "{{.Name}}", name)
	html = strings.ReplaceAll(html, "{{.VerifyURL}}", verifyURL)

	return p.sendEmail(ctx, to, "验证您的邮箱 - 盘古舆情", html)
}

// SendPasswordResetEmail 发送密码重置邮件
func (p *SMTPProvider) SendPasswordResetEmail(ctx context.Context, to, name, resetURL string) error {
	html := strings.ReplaceAll(passwordResetEmailHTML, "{{.Name}}", name)
	html = strings.ReplaceAll(html, "{{.ResetURL}}", resetURL)

	return p.sendEmail(ctx, to, "重置您的密码 - 盘古舆情", html)
}

// SendTestEmail 发送测试邮件
func (p *SMTPProvider) SendTestEmail(ctx context.Context, to string) error {
	html := strings.ReplaceAll(testEmailHTML, "{{.Timestamp}}", time.Now().Format("2006-01-02 15:04:05"))

	return p.sendEmail(ctx, to, "邮件服务测试 - 盘古舆情", html)
}

// SendRaw 发送自定义 HTML 邮件。
func (p *SMTPProvider) SendRaw(ctx context.Context, to, subject, htmlBody string) error {
	return p.sendEmail(ctx, to, subject, htmlBody)
}

// sendEmail SMTP 发送邮件实现
func (p *SMTPProvider) SendRawReceipt(ctx context.Context, to, subject, body string) (notification.Receipt, error) {
	if err := p.sendEmail(ctx, to, subject, body); err != nil {
		return notification.Receipt{}, err
	}
	return notification.Receipt{Provider: "smtp", State: "accepted", AcceptedAt: time.Now().UTC()}, nil
}
func (p *SMTPProvider) sendEmail(ctx context.Context, to, subject, htmlBody string) (sendErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	defer func() {
		if sendErr != nil {
			if ctx.Err() != nil {
				sendErr = ctx.Err()
			} else {
				sendErr = fmt.Errorf("SMTP delivery was not accepted")
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, v := range []string{to, subject, p.fromAddress, p.fromName} {
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("invalid mail header")
		}
	}

	from := fmt.Sprintf("%s <%s>", p.fromName, p.fromAddress)

	// 构建邮件内容
	message := fmt.Sprintf("From: %s\r\n"+
		"To: %s\r\n"+
		"Subject: %s\r\n"+
		"MIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\n"+
		"\r\n"+
		"%s",
		from, to, subject, htmlBody)

	// SMTP 认证
	auth := smtp.PlainAuth("", p.username, p.password, p.host)

	// 发送邮件（支持 TLS）
	addr := fmt.Sprintf("%s:%d", p.host, p.port)

	// 建立 TLS 连接
	tlsConfig := &tls.Config{
		ServerName: p.host,
	}

	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: tlsConfig}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp tls dial failed: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, p.host)
	if err != nil {
		return fmt.Errorf("smtp new client failed: %w", err)
	}
	defer client.Close()

	// 认证
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth failed: %w", err)
	}

	// 设置发件人
	if err = client.Mail(p.fromAddress); err != nil {
		return fmt.Errorf("smtp mail failed: %w", err)
	}

	// 设置收件人
	if err = client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt failed: %w", err)
	}

	// 发送邮件内容
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data failed: %w", err)
	}

	_, err = w.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("smtp write failed: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("smtp close failed: %w", err)
	}

	// DATA completion is acceptance; QUIT failure cannot undo it.
	_ = client.Quit()
	return nil
}
