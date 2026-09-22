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
// 实际发送用 settings 里的 sms_template_code（独立部署环境各自申请的模板）。
func (m *settingsSMS) Send(ctx context.Context, to, _ string, params map[string]string) error {
	get := func(key string) string {
		v, _ := m.store.Get(ctx, key)
		return v
	}
	cfg := sms.Config{
		Provider: get("sms_provider"),
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
	template := get("sms_template_code")
	if template == "" {
		return fmt.Errorf("sms: sms_template_code not configured (admin settings)")
	}
	return p.Send(ctx, to, template, params)
}
