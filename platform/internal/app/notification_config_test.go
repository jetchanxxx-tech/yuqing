package app

import (
	"context"
	"errors"
	"github.com/yuqing/platform/internal/platform/settings"
	"testing"
)

type notificationReadFailure struct{ settings.Store }

func (s notificationReadFailure) Get(ctx context.Context, key string) (string, error) {
	if key == "resend_api_key" {
		return "", errors.New("isolated read outage")
	}
	return s.Store.Get(ctx, key)
}
func TestNotificationSelectedProviderCannotFallbackOrHideReadFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		var st settings.Store = settings.NewMemoryStore(map[string]string{"email_provider": "resend", "email_from_address": "sender@example.invalid", "smtp_host": "smtp.example.invalid"})
		if failure {
			st = notificationReadFailure{st}
		}
		if err := NewSettingsMailer(st).CheckVerification(context.Background(), "password_reset"); err == nil {
			t.Errorf("selected Resend configuration failure=%v silently used SMTP", failure)
		}
	}
}
func TestNotificationTencentPreflightRequiresAppID(t *testing.T) {
	st := settings.NewMemoryStore(map[string]string{"sms_provider": "tencent", "sms_access_key_id": "fake-id", "sms_access_key_secret": "fake-secret", "sms_sign_name": "sandbox", "sms_phone_login_template_code": "login"})
	if err := NewSettingsSMS(st).CheckVerification(context.Background(), "phone_login"); err == nil {
		t.Fatal("Tencent missing App ID passed public preflight")
	}
}
