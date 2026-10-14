-- +goose Up
ALTER TABLE analyses ADD COLUMN scenario_template_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE analyses DROP COLUMN scenario_template_id;
