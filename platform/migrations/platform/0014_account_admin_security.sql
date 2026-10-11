-- +goose Up
-- Account-wide credential revocation and optimistic concurrency for administration.
-- Keep historical accounts, tenant membership roles, and business data unchanged.
-- Platform-role grants are provisioned explicitly using verified immutable user IDs.
-- Shared last-platform-admin checks in auth transactions must first acquire
-- pg_advisory_xact_lock(741914) before reading or changing admin roles/account status.

ALTER TABLE users
    ADD COLUMN token_version BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN row_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT users_token_version_nonnegative CHECK (token_version >= 0),
    ADD CONSTRAINT users_row_version_nonnegative CHECK (row_version >= 0);

ALTER TABLE tenants
    ADD COLUMN row_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT tenants_row_version_nonnegative CHECK (row_version >= 0);

ALTER TABLE tenant_members
    ADD COLUMN row_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT tenant_members_row_version_nonnegative CHECK (row_version >= 0);

CREATE TABLE platform_user_roles (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    role       TEXT NOT NULL CHECK (role IN ('platform_admin')),
    granted_by TEXT REFERENCES users(id) ON DELETE RESTRICT,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role)
);

CREATE INDEX idx_users_status_created_id ON users(status, created_at, id);
CREATE INDEX idx_tenants_status_created_id ON tenants(status, created_at, id);
CREATE INDEX idx_tenant_members_user_tenant ON tenant_members(user_id, tenant_id);
CREATE INDEX idx_platform_user_roles_role_user ON platform_user_roles(role, user_id);

-- +goose Down
-- Dependency order is reversed; existing account and tenant rows remain intact.
DROP INDEX IF EXISTS idx_platform_user_roles_role_user;
DROP INDEX IF EXISTS idx_tenant_members_user_tenant;
DROP INDEX IF EXISTS idx_tenants_status_created_id;
DROP INDEX IF EXISTS idx_users_status_created_id;

DROP TABLE IF EXISTS platform_user_roles;

ALTER TABLE tenant_members DROP CONSTRAINT IF EXISTS tenant_members_row_version_nonnegative;
ALTER TABLE tenant_members DROP COLUMN IF EXISTS row_version;

ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_row_version_nonnegative;
ALTER TABLE tenants DROP COLUMN IF EXISTS row_version;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_row_version_nonnegative;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_token_version_nonnegative;
ALTER TABLE users DROP COLUMN IF EXISTS row_version;
ALTER TABLE users DROP COLUMN IF EXISTS token_version;
