-- +goose Up
CREATE TABLE account_closures (
 id TEXT PRIMARY KEY,user_id TEXT NOT NULL REFERENCES users(id),
 state TEXT NOT NULL CHECK(state IN ('pending','cancelled','finalizing','completed')),
 requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),withdraw_until TIMESTAMPTZ NOT NULL,
 completed_at TIMESTAMPTZ,actor_version BIGINT NOT NULL,avatar_ref TEXT NOT NULL DEFAULT '',
 cleanup_status TEXT NOT NULL DEFAULT 'pending',last_error TEXT NOT NULL DEFAULT '',
 cleanup_cursor BIGINT NOT NULL DEFAULT 0,
 CHECK(withdraw_until=requested_at+interval '168 hours')
);
CREATE UNIQUE INDEX account_closure_live ON account_closures(user_id) WHERE state IN ('pending','finalizing');
CREATE INDEX account_closure_due ON account_closures(withdraw_until) WHERE state='pending';
CREATE TABLE account_closure_tenants (
 closure_id TEXT NOT NULL REFERENCES account_closures(id),tenant_id TEXT NOT NULL REFERENCES tenants(id),
 sole_member BOOLEAN NOT NULL,original_status TEXT NOT NULL,plan_code TEXT NOT NULL,
 retention_days INTEGER,cleanup_due TIMESTAMPTZ,
 cleanup_status TEXT NOT NULL DEFAULT 'pending',PRIMARY KEY(closure_id,tenant_id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'account closure tombstones are forward-only'; END $$;
-- +goose StatementEnd
