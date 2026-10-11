-- +goose Up
-- Historical pool rows retain their known source; only a new explicit manual
-- operation or verified purchase may assign the more specific source.
ALTER TABLE report_credits ADD COLUMN plan_source TEXT NOT NULL DEFAULT 'report_credits'
 CHECK(plan_source IN ('report_credits','manual','purchase'));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'entitlement source migration is forward-only'; END $$;
-- +goose StatementEnd
