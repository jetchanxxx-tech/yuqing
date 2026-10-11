-- +goose Up
-- Preserve source text separately when no timezone-qualified instant exists.
-- No historical backfill: original values already discarded by older binaries
-- are genuinely unknown. Existing UTC timestamps and ordering stay unchanged.
ALTER TABLE raw_documents ADD COLUMN source_published_at TEXT;

-- +goose Down
-- Original source evidence must survive an application rollback.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'document source time migration is forward-only'; END $$;
-- +goose StatementEnd
