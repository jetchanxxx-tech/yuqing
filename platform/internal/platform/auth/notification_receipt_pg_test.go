package auth

import (
	"context"
	"encoding/json"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"strings"
	"testing"
)

func TestNotificationReceiptPGPersistsWithoutCredentialPayload(t *testing.T) {
	pool := pgtest.Pool(t, "notification_receipt")
	ctx := context.Background()
	users := NewPGStore(pool)
	seedUCUserOnUserStore(t, users, "receipt-user", "receipt@example.invalid")
	mail := &fakeMailSender{}
	sms := &fakeSMS{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), sms, mail, "https://example.invalid")
	if err := svc.SendVerificationEmail(ctx, "receipt-user", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.SendPhoneCode(ctx, "receipt-user", "13800000000"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"verification_tokens", "sms_verification_codes"} {
		var raw string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(to_jsonb(t)->'delivery_receipt','{}'::jsonb)::text FROM `+table+` t WHERE user_id='receipt-user'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var r map[string]any
		if json.Unmarshal([]byte(raw), &r) != nil {
			t.Fatal("receipt not JSON")
		}
		if r["state"] != "accepted" || r["provider"] == nil || r["purpose"] == nil || r["accepted_at"] == nil {
			t.Fatalf("safe durable acceptance receipt missing for %s", table)
		}
		token := strings.Split(strings.Split(mail.body, "token=")[1], "\"")[0]
		for _, secret := range []string{token, sms.code, "receipt@example.invalid", "13800000000"} {
			if strings.Contains(raw, secret) {
				t.Fatal("receipt contains credential or recipient")
			}
		}
	}
	// Reconstructing the service/store must retain accepted credentials.
	again := NewService(users, testSecret, "15m", "720h")
	again.EnableUserCenter(users, NewPGVerificationStore(pool), sms, mail, "https://example.invalid")
	if err := again.BindPhone(ctx, "receipt-user", "13800000000", sms.code); err != nil {
		t.Fatal(err)
	}
}
