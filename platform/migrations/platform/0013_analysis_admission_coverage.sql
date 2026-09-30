-- +goose Up
ALTER TABLE analyses ADD COLUMN retrieval_coverage JSONB;

-- +goose Down
ALTER TABLE analyses DROP COLUMN retrieval_coverage;
