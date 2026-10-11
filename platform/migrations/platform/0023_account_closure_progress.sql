-- +goose Up
ALTER TABLE account_closures ADD COLUMN avatar_storage_id TEXT NOT NULL DEFAULT '',
 ADD COLUMN audit_cursor BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN audit_processed BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN business_remaining BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN cleanup_completed_at TIMESTAMPTZ,
 ADD COLUMN next_attempt_at TIMESTAMPTZ;
CREATE INDEX account_closure_retry ON account_closures(next_attempt_at) WHERE cleanup_status<>'complete' AND state<>'cancelled';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'closure storage identity and progress are forward-only'; END $$;
-- +goose StatementEnd
