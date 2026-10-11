package app

import "testing"

func TestVerificationSMSTemplatesCannotCrossPurpose(t *testing.T) {
	for _, tc := range []struct{ alias, key string }{{"SMS_BIND_PHONE", "sms_bind_phone_template_code"}, {"SMS_PHONE_LOGIN", "sms_phone_login_template_code"}, {"SMS_PHONE_RESET", "sms_phone_reset_template_code"}} {
		key, err := smsPurposeSetting(tc.alias)
		if err != nil || key != tc.key {
			t.Fatalf("wrong configuration mapping for %s", tc.alias)
		}
	}
	if _, err := smsPurposeSetting("payment_confirm"); err == nil {
		t.Fatal("unsupported purpose silently fell back to generic template")
	}
}
