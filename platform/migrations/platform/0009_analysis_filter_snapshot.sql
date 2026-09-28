-- +goose Up
ALTER TABLE analyses ADD COLUMN date_from TEXT NOT NULL DEFAULT '';
ALTER TABLE analyses ADD COLUMN date_to TEXT NOT NULL DEFAULT '';
ALTER TABLE analyses ADD COLUMN exclude_words JSONB NOT NULL DEFAULT 'null';

-- +goose Down
ALTER TABLE analyses DROP COLUMN exclude_words;
ALTER TABLE analyses DROP COLUMN date_to;
ALTER TABLE analyses DROP COLUMN date_from;
