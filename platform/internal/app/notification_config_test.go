package app

import (
	"context"
	"errors"
	"github.com/yuqing/platform/internal/platform/auth"
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

type notificationLookupCounter struct {
	*auth.MemoryStore
	lookups int
}

func (s *notificationLookupCounter) GetUserByEmail(ctx context.Context, target string) (*auth.User, error) {
	s.lookups++
	return s.MemoryStore.GetUserByEmail(ctx, target)
}
func (s *notificationLookupCounter) GetByPhone(ctx context.Context, target string) (*auth.User, error) {
	s.lookups++
	return s.MemoryStore.GetByPhone(ctx, target)
}
func TestNotificationPublicConfigurationFailureBeforeKnownUnknownLookup(t *testing.T) {
	for _, purpose := range []string{auth.PasswordReset, auth.PhoneLogin, auth.PhoneReset} {
		users := &notificationLookupCounter{MemoryStore: auth.NewMemoryStore()}
		if err := users.CreateUser(context.Background(), auth.User{ID: "known-notify", Email: "known@example.invalid", Phone: "13800000000", Status: "active"}); err != nil {
			t.Fatal(err)
		}
		settingsStore := settings.NewMemoryStore(map[string]string{"email_provider": "resend", "email_from_address": "sender@example.invalid", "sms_provider": "tencent", "sms_access_key_id": "fake-id", "sms_access_key_secret": "fake-secret", "sms_sign_name": "sandbox", "sms_phone_login_template_code": "login", "sms_phone_reset_template_code": "reset"})
		svc := auth.NewService(users, "sandbox-secret", "15m", "720h")
		svc.EnableUserCenter(users, auth.NewMemoryVerificationStore(), NewSettingsSMS(settingsStore), NewSettingsMailer(settingsStore), "https://example.invalid")
		targets := []string{"13800000000", "13900000000"}
		if purpose == auth.PasswordReset {
			targets = []string{"known@example.invalid", "unknown@example.invalid"}
		}
		var first string
		for _, target := range targets {
			err := svc.RequestPublicVerification(context.Background(), purpose, target, "192.0.2.8")
			if err == nil {
				t.Fatal("global configuration failure admitted")
			}
			if first == "" {
				first = err.Error()
			} else if first != err.Error() {
				t.Fatal("known/unknown configuration errors differ")
			}
		}
		if users.lookups != 0 {
			t.Fatal("configuration failure reached account lookup")
		}
	}
}
func TestNotificationProviderPurposePrerequisites(t *testing.T) {
	ctx := context.Background()
	for _, provider := range []string{"aliyun", "tencent"} {
		for _, purpose := range []string{"phone_bind", "phone_login", "phone_reset"} {
			vals := map[string]string{"sms_provider": provider, "sms_access_key_id": "fake-id", "sms_access_key_secret": "fake-secret", "sms_sign_name": "sandbox", "sms_sdk_app_id": "fake-app", "sms_bind_phone_template_code": "bind", "sms_phone_login_template_code": "login", "sms_phone_reset_template_code": "reset"}
			sender := NewSettingsSMS(settings.NewMemoryStore(vals))
			if err := sender.CheckVerification(ctx, purpose); err != nil {
				t.Fatal(err)
			}
			key := map[string]string{"phone_bind": "sms_bind_phone_template_code", "phone_login": "sms_phone_login_template_code", "phone_reset": "sms_phone_reset_template_code"}[purpose]
			delete(vals, key)
			if err := NewSettingsSMS(settings.NewMemoryStore(vals)).CheckVerification(ctx, purpose); err == nil {
				t.Fatal("another purpose template concealed missing template")
			}
		}
	}
	for _, key := range []string{"smtp_host", "smtp_username", "smtp_password", "smtp_port"} {
		vals := map[string]string{"email_provider": "smtp", "email_from_address": "sender@example.invalid", "smtp_host": "smtp.example.invalid", "smtp_username": "sandbox", "smtp_password": "fake-password", "smtp_port": "465"}
		if err := NewSettingsMailer(settings.NewMemoryStore(vals)).CheckVerification(ctx, "email_verify"); err != nil {
			t.Fatal(err)
		}
		vals[key] = ""
		if key == "smtp_port" {
			vals[key] = "70000"
		}
		if err := NewSettingsMailer(settings.NewMemoryStore(vals)).CheckVerification(ctx, "email_verify"); err == nil {
			t.Fatal("invalid SMTP configuration passed")
		}
	}
}
