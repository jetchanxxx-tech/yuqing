package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yuqing/platform/internal/pkg/notification"
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

type receiptMail struct{ subject, body string }

func (m *receiptMail) Send(ctx context.Context, to, subject, body string) error {
	_, err := m.SendReceipt(ctx, to, subject, body)
	return err
}
func (m *receiptMail) SendReceipt(_ context.Context, _, subject, body string) (notification.Receipt, error) {
	m.subject, m.body = subject, body
	return notification.Accepted("resend", "sandbox-email-reference"), nil
}

type receiptSMS struct {
	alias, code string
	reject      bool
}

func (m *receiptSMS) Send(ctx context.Context, to, alias string, params map[string]string) error {
	_, err := m.SendReceipt(ctx, to, alias, params)
	return err
}
func (m *receiptSMS) SendReceipt(_ context.Context, _, alias string, params map[string]string) (notification.Receipt, error) {
	m.alias, m.code = alias, params["code"]
	if m.reject {
		return notification.Receipt{Provider: "aliyun"}, fmt.Errorf("isolated rejection")
	}
	return notification.Accepted("aliyun", "sandbox-sms-reference"), nil
}
func TestNotificationReceiptPGAllPurposesAndRejectedState(t *testing.T) {
	pool := pgtest.Pool(t, "notification_purposes")
	ctx := context.Background()
	users := NewPGStore(pool)
	for _, purpose := range []string{EmailVerify, PasswordReset, EmailChange, SetPassword, PhoneBind, PhoneLogin, PhoneReset} {
		t.Run(purpose, func(t *testing.T) {
			uid := "receipt-" + purpose
			seedUCUserOnUserStore(t, users, uid, uid+"@example.invalid")
			if purpose == SetPassword {
				if _, err := pool.Exec(ctx, `UPDATE users SET status='pending_activation' WHERE id=$1`, uid); err != nil {
					t.Fatal(err)
				}
			}
			if purpose == PhoneLogin || purpose == PhoneReset {
				if _, err := pool.Exec(ctx, `UPDATE users SET phone=$2,phone_verified_at=now() WHERE id=$1`, uid, map[string]string{PhoneLogin: "13800000001", PhoneReset: "13800000002"}[purpose]); err != nil {
					t.Fatal(err)
				}
			}
			u, err := users.GetByID(ctx, uid)
			if err != nil {
				t.Fatal(err)
			}
			mail, sms := &receiptMail{}, &receiptSMS{}
			svc := NewService(users, testSecret, "15m", "720h")
			svc.EnableUserCenter(users, NewPGVerificationStore(pool), sms, mail, "https://example.invalid")
			target := u.Email
			table := "verification_tokens"
			provider := "resend"
			if verificationSMS(purpose) {
				target = u.Phone
				if target == "" {
					target = "13800000000"
				}
				table = "sms_verification_codes"
				provider = "aliyun"
			}
			if purpose == EmailChange {
				target = "changed@example.invalid"
			}
			if err = svc.issueVerification(ctx, u, purpose, target); err != nil {
				t.Fatal(err)
			}
			var raw string
			if err = pool.QueryRow(ctx, `SELECT delivery_receipt::text FROM `+table+` WHERE user_id=$1`, uid).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var r notification.Receipt
			if err = json.Unmarshal([]byte(raw), &r); err != nil {
				t.Fatal(err)
			}
			if r.Purpose != purpose || r.Provider != provider || r.State != "accepted" || len(r.ProviderID) != 71 {
				t.Fatal("supplier receipt/purpose not retained")
			}
			if verificationSMS(purpose) {
				if sms.alias != verificationTemplate(purpose) {
					t.Fatal("purpose alias differs")
				}
			} else {
				paths := map[string]string{EmailVerify: "/verify-email", PasswordReset: "/reset-password", EmailChange: "/email-change", SetPassword: "/activate"}
				if !strings.Contains(mail.body, paths[purpose]+"#token=") {
					t.Fatal("purpose link differs")
				}
				if mail.subject == "" {
					t.Fatal("purpose subject absent")
				}
			}
		})
	}
	seedUCUserOnUserStore(t, users, "receipt-rejected", "receipt-rejected@example.invalid")
	rejected := &receiptSMS{reject: true}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), rejected, nil, "https://example.invalid")
	if err := svc.SendPhoneCode(ctx, "receipt-rejected", "13800000003"); err == nil {
		t.Fatal("rejection reported success")
	}
	var safe bool
	if err := pool.QueryRow(ctx, `SELECT delivery_status='rejected' AND delivery_receipt->>'state'='rejected' AND delivery_receipt->>'provider'='aliyun' AND used_at IS NOT NULL FROM sms_verification_codes WHERE user_id='receipt-rejected'`).Scan(&safe); err != nil || !safe {
		t.Fatal("rejection receipt not durable")
	}
}
func TestNotificationReceiptPGWriteFailureCannotAcceptCredential(t *testing.T) {
	pool := pgtest.Pool(t, "notification_receipt_failure")
	ctx := context.Background()
	users := NewPGStore(pool)
	seedUCUserOnUserStore(t, users, "receipt-fault", "fault@example.invalid")
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.delivery_status='accepted' THEN RAISE EXCEPTION 'isolated receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_receipt BEFORE UPDATE ON sms_verification_codes FOR EACH ROW EXECUTE FUNCTION reject_receipt()`); err != nil {
		t.Fatal(err)
	}
	sender := &receiptSMS{}
	svc := NewService(users, testSecret, "15m", "720h")
	svc.EnableUserCenter(users, NewPGVerificationStore(pool), sender, nil, "https://example.invalid")
	if err := svc.SendPhoneCode(ctx, "receipt-fault", "13800000000"); err == nil {
		t.Fatal("receipt failure reported success")
	}
	if err := svc.BindPhone(ctx, "receipt-fault", "13800000000", sender.code); err == nil {
		t.Fatal("unrecorded acceptance can consume")
	}
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT delivery_status='pending' AND delivery_receipt='{}'::jsonb FROM sms_verification_codes WHERE user_id='receipt-fault'`).Scan(&pending); err != nil || !pending {
		t.Fatal("receipt/status did not roll back together")
	}
}
