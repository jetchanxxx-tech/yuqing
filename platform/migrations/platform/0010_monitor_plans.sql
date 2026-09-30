-- +goose Up
CREATE TABLE monitor_plans (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    template_id TEXT NOT NULL,
    template_version INTEGER NOT NULL CHECK (template_version > 0),
    analysis_type TEXT NOT NULL CHECK (analysis_type IN ('event', 'brand', 'competitor', 'industry')),
    name TEXT NOT NULL,
    inputs_json JSONB NOT NULL CHECK (jsonb_typeof(inputs_json) = 'object'),
    config_json JSONB NOT NULL CHECK (jsonb_typeof(config_json) = 'object'),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    state TEXT NOT NULL DEFAULT 'draft' CHECK (state = 'draft'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_monitor_plans_tenant_id ON monitor_plans(tenant_id, id);
CREATE INDEX idx_monitor_plans_owner_created ON monitor_plans(tenant_id, owner_id, created_at DESC, id);

-- +goose Down
DROP TABLE monitor_plans;
