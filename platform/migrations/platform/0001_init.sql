-- +goose Up
-- Platform database: tenants, users, plans, billing, usage.

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE tenants (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    slug          TEXT UNIQUE NOT NULL,
    db_name       TEXT UNIQUE NOT NULL,
    status        TEXT NOT NULL DEFAULT 'provisioning',
    plan_code     TEXT NOT NULL DEFAULT 'free',
    quota_json    JSONB NOT NULL DEFAULT '{}',
    settings_json JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id             TEXT PRIMARY KEY,
    email          CITEXT UNIQUE NOT NULL,
    password_hash  TEXT NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'active',
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tenant_members (
    tenant_id   TEXT REFERENCES tenants(id) ON DELETE CASCADE,
    user_id     TEXT REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'analyst',
    invited_at  TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, user_id)
);

CREATE TABLE plans (
    code                  TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    price_monthly_cny     INTEGER NOT NULL DEFAULT 0,
    features_json         JSONB NOT NULL DEFAULT '{}',
    token_quota_m         INTEGER NOT NULL DEFAULT 1,
    budget_mode           TEXT NOT NULL DEFAULT 'hard_cap',
    overage_in_rate_per_m  INTEGER DEFAULT 0,
    overage_out_rate_per_m INTEGER DEFAULT 0,
    created_at            TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE subscriptions (
    id                   TEXT PRIMARY KEY,
    tenant_id            TEXT NOT NULL REFERENCES tenants(id),
    plan_code            TEXT NOT NULL REFERENCES plans(code),
    status               TEXT NOT NULL DEFAULT 'trialing',
    current_period_start TIMESTAMPTZ NOT NULL,
    current_period_end   TIMESTAMPTZ NOT NULL,
    cancel_at_period_end BOOLEAN DEFAULT false,
    gateway_ref          TEXT,
    created_at           TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE billing_periods (
    id              TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    period_start    TIMESTAMPTZ NOT NULL,
    period_end      TIMESTAMPTZ NOT NULL,
    tokens_used     BIGINT DEFAULT 0,
    overage_tokens  BIGINT DEFAULT 0,
    base_amount     INTEGER DEFAULT 0,
    overage_amount  INTEGER DEFAULT 0,
    status          TEXT DEFAULT 'open',
    UNIQUE (subscription_id, period_start)
);

CREATE TABLE invoices (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    subscription_id TEXT,
    period_start    TIMESTAMPTZ,
    period_end      TIMESTAMPTZ,
    subtotal_cny    INTEGER DEFAULT 0,
    tax_cny         INTEGER DEFAULT 0,
    total_cny       INTEGER DEFAULT 0,
    status          TEXT DEFAULT 'unpaid',
    pdf_key         TEXT,
    paid_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE usage_events (
    id              BIGSERIAL,
    tenant_id       TEXT NOT NULL,
    user_id         TEXT,
    model           TEXT NOT NULL,
    analysis_id     TEXT,
    task_id         TEXT,
    engine          TEXT,
    prompt_tokens   INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cache_tokens    INTEGER DEFAULT 0,
    cost_micro_cny   BIGINT NOT NULL DEFAULT 0,
    billed_micro_cny BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE usage_daily (
    tenant_id    TEXT NOT NULL,
    usage_date   DATE NOT NULL,
    model        TEXT NOT NULL,
    in_tokens    BIGINT DEFAULT 0,
    out_tokens   BIGINT DEFAULT 0,
    cache_tokens BIGINT DEFAULT 0,
    cost_micro_cny  BIGINT DEFAULT 0,
    billed_micro_cny BIGINT DEFAULT 0,
    PRIMARY KEY (tenant_id, usage_date, model)
);

CREATE TABLE api_keys (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    name        TEXT,
    key_hash    TEXT NOT NULL,
    scopes      JSONB,
    last_used_at TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ
);

CREATE TABLE audit_logs (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    TEXT,
    actor_id     TEXT,
    action       TEXT,
    resource     TEXT,
    details_json JSONB,
    created_at   TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_usage_events_tenant_created ON usage_events(tenant_id, created_at);
CREATE INDEX idx_audit_logs_tenant_created ON audit_logs(tenant_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS usage_daily;
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS billing_periods;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS tenant_members;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
