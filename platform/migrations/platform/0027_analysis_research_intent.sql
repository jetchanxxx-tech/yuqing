-- +goose Up
ALTER TABLE analyses ADD COLUMN research_question TEXT NOT NULL DEFAULT '';
ALTER TABLE analyses ADD COLUMN research_intent JSONB NOT NULL DEFAULT '{}';
ALTER TABLE analyses ADD COLUMN research_intent_version TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE analyses DROP COLUMN research_intent_version;
ALTER TABLE analyses DROP COLUMN research_intent;
ALTER TABLE analyses DROP COLUMN research_question;
