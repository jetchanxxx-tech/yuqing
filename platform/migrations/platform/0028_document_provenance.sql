-- +goose Up
ALTER TABLE raw_documents ADD COLUMN content_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE raw_documents ADD COLUMN fetch_status TEXT NOT NULL DEFAULT '';
ALTER TABLE raw_documents ADD COLUMN fetch_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE raw_documents ADD COLUMN final_url TEXT NOT NULL DEFAULT '';
ALTER TABLE raw_documents ADD COLUMN truncated BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE raw_documents ADD COLUMN semantic_classification TEXT NOT NULL DEFAULT '';
ALTER TABLE raw_documents ADD COLUMN semantic_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE raw_documents ADD COLUMN evidence_spans JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE raw_documents DROP COLUMN evidence_spans;
ALTER TABLE raw_documents DROP COLUMN semantic_reason;
ALTER TABLE raw_documents DROP COLUMN semantic_classification;
ALTER TABLE raw_documents DROP COLUMN truncated;
ALTER TABLE raw_documents DROP COLUMN final_url;
ALTER TABLE raw_documents DROP COLUMN fetch_reason;
ALTER TABLE raw_documents DROP COLUMN fetch_status;
ALTER TABLE raw_documents DROP COLUMN content_kind;
