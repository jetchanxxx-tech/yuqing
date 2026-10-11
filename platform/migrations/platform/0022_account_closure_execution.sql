-- +goose Up
ALTER TABLE users ADD COLUMN closed_at TIMESTAMPTZ, ADD COLUMN pii_anonymized_at TIMESTAMPTZ;
ALTER TABLE account_closures ADD COLUMN avatar_deleted BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN attempts BIGINT NOT NULL DEFAULT 0, ADD COLUMN last_attempt_at TIMESTAMPTZ;
ALTER TABLE analyses ADD COLUMN closure_purged_at TIMESTAMPTZ;
ALTER TABLE verification_tokens DROP CONSTRAINT verification_tokens_notice_state_check;
ALTER TABLE verification_tokens ADD CONSTRAINT verification_tokens_notice_state_check CHECK(notice_state IN ('','pending','processing','failed','accepted','cancelled'));
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'closure completion and anonymization are forward-only'; END $$;
-- +goose StatementEnd
