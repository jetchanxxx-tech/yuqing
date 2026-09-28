-- +goose Up
ALTER TABLE analyses ADD COLUMN report_template_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE analyses DROP COLUMN report_template_id;
