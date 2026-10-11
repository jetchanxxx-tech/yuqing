-- +goose Up
ALTER TABLE verification_tokens ADD COLUMN delivery_receipt JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE sms_verification_codes ADD COLUMN delivery_receipt JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE verification_tokens DROP COLUMN delivery_receipt;
ALTER TABLE sms_verification_codes DROP COLUMN delivery_receipt;
