package app

// 用户中心外部通道适配器：邮件/短信供应商从 platform_settings 动态读取
// （admin 后台改配置即时生效，独立部署时各环境自行配置，零重启）。

import (
	"context"
	"fmt"
	"strconv"

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
	cfg, err := m.emailConfig(ctx)
	if err != nil {
		return err
	}
	p, err := email.NewProvider(cfg)
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	return p.SendRaw(ctx, to, subject, htmlBody)
}

func (m *settingsMailer) emailConfig(ctx context.Context) (email.Config, error) {
	get := func(key string) string {
		v, _ := m.store.Get(ctx, key)
		return v
	}
	cfg := email.Config{
		Provider:     get("email_provider"),
		FromAddress:  get("email_from_address"),
		FromName:     get("email_from_name"),
		ResendAPIKey: get("resend_api_key"),
		SMTPHost:     get("smtp_host"),
		SMTPUsername: get("smtp_username"),
		SMTPPassword: get("smtp_password"),
	}
	if p, err := strconv.Atoi(get("smtp_port")); err == nil && p > 0 {
		cfg.SMTPPort = p
	} else {
		cfg.SMTPPort = 465
	}
	if cfg.FromAddress == "" {
		return cfg, fmt.Errorf("mail: email_from_address not configured (admin settings)")
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
func (m *settingsSMS) Send(ctx context.Context, to, alias string, params map[string]string) error {
	get := func(key string) string {
		v, _ := m.store.Get(ctx, key)
		return v
	}
	cfg := sms.Config{
		Provider:              get("sms_provider"),
		AliyunAccessKeyID:     get("sms_access_key_id"),
		AliyunAccessKeySecret: get("sms_access_key_secret"),
		AliyunSignName:        get("sms_sign_name"),
		TencentSecretID:       get("sms_access_key_id"),
		TencentSecretKey:      get("sms_access_key_secret"),
		TencentSDKAppID:       get("sms_sdk_app_id"),
		TencentSignName:       get("sms_sign_name"),
	}
	if cfg.Provider == "" {
		return fmt.Errorf("sms: sms_provider not configured (admin settings)")
	}
	p, err := sms.NewProvider(cfg)
	if err != nil {
		return fmt.Errorf("sms: %w", err)
	}
	templateKey, err := smsPurposeSetting(alias)
	if err != nil {
		return err
	}
	template := get(templateKey)
	if template == "" {
		return fmt.Errorf("sms: purpose template not configured (admin settings)")
	}
	return p.Send(ctx, to, template, params)
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
	provider, err := m.store.Get(ctx, "sms_provider")
	if err != nil || (provider != "aliyun" && provider != "tencent") {
		return fmt.Errorf("sms: verification channel not configured")
	}
	key, err := smsPurposeSetting(aliases[purpose])
	if err != nil {
		return err
	}
	for _, name := range []string{"sms_access_key_id", "sms_access_key_secret", "sms_sign_name", key} {
		value, err := m.store.Get(ctx, name)
		if err != nil || value == "" {
			return fmt.Errorf("sms: verification channel not configured")
		}
	}
	return nil
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
