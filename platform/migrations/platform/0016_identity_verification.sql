-- +goose Up
-- Legacy bearer credentials cannot cross the migration boundary.
ALTER TABLE verification_tokens ALTER COLUMN token DROP NOT NULL;
ALTER TABLE verification_tokens ADD COLUMN purpose TEXT,
 ADD COLUMN token_hash TEXT, ADD COLUMN target TEXT,
 ADD COLUMN issuer_user_id TEXT REFERENCES users(id),
 ADD COLUMN issued_version BIGINT, ADD COLUMN issuer_version BIGINT,
 ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'rejected' CHECK(delivery_status IN ('pending','accepted','rejected'));
UPDATE verification_tokens SET token=NULL, used_at=COALESCE(used_at,now());
ALTER TABLE verification_tokens ADD CONSTRAINT verification_no_plaintext CHECK(token IS NULL);
CREATE UNIQUE INDEX verification_token_hash_unique ON verification_tokens(token_hash) WHERE token_hash IS NOT NULL;
CREATE INDEX verification_tokens_user_purpose ON verification_tokens(user_id,purpose) WHERE used_at IS NULL;
ALTER TABLE verification_tokens ADD CONSTRAINT verification_email_purpose CHECK(purpose IS NULL OR purpose IN ('email_verify','set_password','password_reset','email_change'));

ALTER TABLE sms_verification_codes ALTER COLUMN code DROP NOT NULL;
ALTER TABLE sms_verification_codes DROP CONSTRAINT sms_verification_codes_phone_key;
ALTER TABLE sms_verification_codes ADD COLUMN user_id TEXT REFERENCES users(id),
 ADD COLUMN issuer_user_id TEXT REFERENCES users(id),
 ADD COLUMN issued_version BIGINT, ADD COLUMN issuer_version BIGINT,
 ADD COLUMN code_hash TEXT, ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 5),
 ADD COLUMN used_at TIMESTAMPTZ,
 ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'rejected' CHECK(delivery_status IN ('pending','accepted','rejected'));
UPDATE sms_verification_codes SET code=NULL,used_at=now();
ALTER TABLE sms_verification_codes ADD CONSTRAINT sms_no_plaintext CHECK(code IS NULL);
ALTER TABLE sms_verification_codes ADD CONSTRAINT sms_new_purpose CHECK(code_hash IS NULL OR purpose IN ('phone_bind','phone_login','phone_reset'));
CREATE UNIQUE INDEX sms_verification_phone_purpose ON sms_verification_codes(phone,purpose) WHERE used_at IS NULL;
CREATE INDEX sms_verification_user ON sms_verification_codes(user_id) WHERE used_at IS NULL;

CREATE TABLE verification_send_gates (
 gate_key TEXT PRIMARY KEY,
 window_start TIMESTAMPTZ NOT NULL,
 last_sent TIMESTAMPTZ NOT NULL,
 sends INTEGER NOT NULL CHECK(sends>0)
);

-- +goose Down
-- Forward-only security migration: plaintext and pre-migration tokens cannot
-- be restored. Roll back the application only with a compatible artifact.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'identity verification migration is forward-only'; END $$;
-- +goose StatementEnd
