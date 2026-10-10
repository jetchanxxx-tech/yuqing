package app

// 用户中心外部通道适配器：邮件/短信供应商从 platform_settings 动态读取
// （admin 后台改配置即时生效，独立部署时各环境自行配置，零重启）。

import (
	"context"
	"fmt"
	"github.com/yuqing/platform/internal/pkg/notification"
	"net/mail"
	"strconv"
	"strings"

	"github.com/yuqing/platform/internal/pkg/email"
	"github.com/yuqing/platform/internal/pkg/sms"
	"github.com/yuqing/platform/internal/platform/settings"
)

// settingsMailer 每次发送时按当前配置构建 Provider（Resend 或 SMTP）。
type settingsMailer struct {
	store settings.Store
}

// NewSettingsMailer 创建动态邮件发送器。
func NewSettingsMailer(store settings.Store) *settingsMailer { return &settingsMailer{store: store} }

// Send 实现 auth.MailSender。
func (m *settingsMailer) Send(ctx context.Context, to, subject, htmlBody string) error {
	_, err := m.SendReceipt(ctx, to, subject, htmlBody)
	return err
}
func (m *settingsMailer) SendReceipt(ctx context.Context, to, subject, body string) (notification.Receipt, error) {
	cfg, err := m.emailConfig(ctx)
	if err != nil {
		return notification.Receipt{}, err
	}
	p, err := email.NewProvider(cfg)
	if err != nil {
		return notification.Receipt{}, fmt.Errorf("mail provider unavailable")
	}
	sender, ok := p.(interface {
		SendRawReceipt(context.Context, string, string, string) (notification.Receipt, error)
	})
	if !ok {
		return notification.Receipt{}, fmt.Errorf("mail receipt unavailable")
	}
	r, err := sender.SendRawReceipt(ctx, to, subject, body)
	r.Provider = cfg.Provider
	return r, err
}

func readNotificationConfig(ctx context.Context, store settings.Store, keys ...string) (map[string]string, error) {
	values := map[string]string{}
	for _, key := range keys {
		v, err := store.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("notification configuration read failed")
		}
		values[key] = strings.TrimSpace(v)
	}
	return values, nil
}
func (m *settingsMailer) emailConfig(ctx context.Context) (email.Config, error) {
	v, err := readNotificationConfig(ctx, m.store, "email_provider", "email_from_address", "email_from_name")
	if err != nil {
		return email.Config{}, err
	}
	cfg := email.Config{Provider: v["email_provider"], FromAddress: v["email_from_address"], FromName: v["email_from_name"]}
	addr, err := mail.ParseAddress(cfg.FromAddress)
	if err != nil || addr.Address != cfg.FromAddress {
		return cfg, fmt.Errorf("mail sender address not configured")
	}
	switch cfg.Provider {
	case "resend":
		v, err = readNotificationConfig(ctx, m.store, "resend_api_key")
		if err != nil {
			return cfg, err
		}
		cfg.ResendAPIKey = v["resend_api_key"]
		if cfg.ResendAPIKey == "" {
			return cfg, fmt.Errorf("resend key not configured")
		}
	case "smtp":
		v, err = readNotificationConfig(ctx, m.store, "smtp_host", "smtp_port", "smtp_username", "smtp_password")
		if err != nil {
			return cfg, err
		}
		cfg.SMTPHost, cfg.SMTPUsername, cfg.SMTPPassword = v["smtp_host"], v["smtp_username"], v["smtp_password"]
		cfg.SMTPPort = 465
		if v["smtp_port"] != "" {
			cfg.SMTPPort, err = strconv.Atoi(v["smtp_port"])
			if err != nil || cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 {
				return cfg, fmt.Errorf("SMTP port invalid")
			}
		}
		if cfg.SMTPHost == "" || cfg.SMTPUsername == "" || cfg.SMTPPassword == "" {
			return cfg, fmt.Errorf("SMTP configuration incomplete")
		}
	default:
		return cfg, fmt.Errorf("mail provider not configured")
	}
	return cfg, nil
}

// settingsSMS 每次发送时按当前配置构建 Provider（阿里云或腾讯云）。
type settingsSMS struct {
	store settings.Store
}

// NewSettingsSMS 创建动态短信发送器。
func NewSettingsSMS(store settings.Store) *settingsSMS { return &settingsSMS{store: store} }

// Send 实现 auth.SMSProvider。templateCode 为业务用途别名（如 SMS_BIND_PHONE），
// 实际发送使用该目的独立配置的供应商模板。
func (m *settingsSMS) smsConfig(ctx context.Context, alias string) (sms.Config, string, error) {
	key, err := smsPurposeSetting(alias)
	if err != nil {
		return sms.Config{}, "", err
	}
	v, err := readNotificationConfig(ctx, m.store, "sms_provider", "sms_access_key_id", "sms_access_key_secret", "sms_sign_name", key)
	if err != nil {
		return sms.Config{}, "", err
	}
	for _, name := range []string{"sms_access_key_id", "sms_access_key_secret", "sms_sign_name", key} {
		if v[name] == "" {
			return sms.Config{}, "", fmt.Errorf("SMS configuration incomplete")
		}
	}
	cfg := sms.Config{Provider: v["sms_provider"], AliyunAccessKeyID: v["sms_access_key_id"], AliyunAccessKeySecret: v["sms_access_key_secret"], AliyunSignName: v["sms_sign_name"], TencentSecretID: v["sms_access_key_id"], TencentSecretKey: v["sms_access_key_secret"], TencentSignName: v["sms_sign_name"]}
	switch cfg.Provider {
	case "aliyun":
	case "tencent":
		app, err := readNotificationConfig(ctx, m.store, "sms_sdk_app_id")
		if err != nil {
			return cfg, "", err
		}
		cfg.TencentSDKAppID = app["sms_sdk_app_id"]
		if cfg.TencentSDKAppID == "" {
			return cfg, "", fmt.Errorf("Tencent App ID not configured")
		}
	default:
		return cfg, "", fmt.Errorf("SMS provider not configured")
	}
	return cfg, v[key], nil
}
func (m *settingsSMS) Send(ctx context.Context, to, alias string, params map[string]string) error {
	_, err := m.SendReceipt(ctx, to, alias, params)
	return err
}
func (m *settingsSMS) SendReceipt(ctx context.Context, to, alias string, params map[string]string) (notification.Receipt, error) {
	cfg, template, err := m.smsConfig(ctx, alias)
	if err != nil {
		return notification.Receipt{}, err
	}
	p, err := sms.NewProvider(cfg)
	if err != nil {
		return notification.Receipt{}, fmt.Errorf("SMS provider unavailable")
	}
	sender, ok := p.(interface {
		SendReceipt(context.Context, string, string, map[string]string) (notification.Receipt, error)
	})
	if !ok {
		return notification.Receipt{}, fmt.Errorf("SMS receipt unavailable")
	}
	r, err := sender.SendReceipt(ctx, to, template, params)
	r.Provider = cfg.Provider
	return r, err
}

// Purpose mappings are explicit; a bind template cannot silently send login or
// recovery messages. K6a configures supplier-specific values and receipt tests.
func smsPurposeSetting(alias string) (string, error) {
	switch alias {
	case "SMS_BIND_PHONE":
		return "sms_bind_phone_template_code", nil
	case "SMS_PHONE_LOGIN":
		return "sms_phone_login_template_code", nil
	case "SMS_PHONE_RESET":
		return "sms_phone_reset_template_code", nil
	}
	return "", fmt.Errorf("sms: unsupported verification purpose")
}
func (m *settingsSMS) CheckVerification(ctx context.Context, purpose string) error {
	aliases := map[string]string{"phone_bind": "SMS_BIND_PHONE", "phone_login": "SMS_PHONE_LOGIN", "phone_reset": "SMS_PHONE_RESET"}
	_, _, err := m.smsConfig(ctx, aliases[purpose])
	return err
}
func (m *settingsMailer) CheckVerification(ctx context.Context, purpose string) error {
	switch purpose {
	case "email_verify", "set_password", "password_reset", "email_change":
	default:
		return fmt.Errorf("mail: unsupported verification purpose")
	}
	cfg, err := m.emailConfig(ctx)
	if err != nil {
		return err
	}
	_, err = email.NewProvider(cfg)
	return err
}
