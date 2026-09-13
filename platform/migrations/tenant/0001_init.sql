-- +goose Up
-- Tenant database template: analyses, documents, sentiment, reports, dashboard.

CREATE TABLE data_sources (
    id           TEXT PRIMARY KEY,
    type         TEXT NOT NULL,
    name         TEXT NOT NULL,
    config_json  JSONB NOT NULL DEFAULT '{}',
    enabled      BOOLEAN DEFAULT true,
    health       TEXT,
    last_sync_at TIMESTAMPTZ
);

CREATE TABLE analyses (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    analysis_type  TEXT NOT NULL,
    params_json    JSONB NOT NULL DEFAULT '{}',
    state          TEXT NOT NULL DEFAULT 'draft',
    priority       INTEGER DEFAULT 0,
    progress       INTEGER DEFAULT 0,
    error_code     TEXT,
    owner_id       TEXT NOT NULL,
    token_estimate INTEGER DEFAULT 0,
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE task_steps (
    id          BIGSERIAL PRIMARY KEY,
    analysis_id TEXT NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    engine      TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'pending',
    result_ref  TEXT,
    error       TEXT,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE TABLE raw_documents (
    id           TEXT PRIMARY KEY,
    analysis_id  TEXT REFERENCES analyses(id) ON DELETE CASCADE,
    source_id    TEXT,
    source_type  TEXT,
    title        TEXT,
    url          TEXT,
    content      TEXT,
    author       TEXT,
    published_at TIMESTAMPTZ,
    fetched_at   TIMESTAMPTZ DEFAULT now(),
    content_hash TEXT NOT NULL UNIQUE,
    media_json   JSONB,
    sentiment_ref TEXT,
    status       TEXT DEFAULT 'pending'
);

CREATE TABLE sentiment_results (
    id            TEXT PRIMARY KEY,
    doc_id        TEXT REFERENCES raw_documents(id) ON DELETE CASCADE,
    sentiment     TEXT NOT NULL,
    score         NUMERIC(5,4),
    emotions_json JSONB,
    aspects_json  JSONB,
    model         TEXT,
    cost_micro_cny BIGINT DEFAULT 0,
    created_at    TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE topics (
    id          TEXT PRIMARY KEY,
    analysis_id TEXT NOT NULL,
    name        TEXT,
    cluster_id  TEXT,
    doc_count   INTEGER DEFAULT 0,
    trend_json  JSONB,
    created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE alerts (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    rule_json         JSONB NOT NULL DEFAULT '{}',
    last_triggered_at TIMESTAMPTZ,
    enabled           BOOLEAN DEFAULT true
);

CREATE TABLE reports (
    id           TEXT PRIMARY KEY,
    analysis_id  TEXT REFERENCES analyses(id) ON DELETE CASCADE,
    title        TEXT,
    format       TEXT,
    status       TEXT DEFAULT 'generating',
    file_key     TEXT,
    summary_json JSONB,
    generated_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE report_templates (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    content    TEXT,
    engine     TEXT,
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE crawl_jobs (
    id           TEXT PRIMARY KEY,
    analysis_id  TEXT,
    source_id    TEXT,
    schedule     TEXT,
    state        TEXT DEFAULT 'pending',
    stats_json   JSONB DEFAULT '{}',
    last_run_at  TIMESTAMPTZ,
    next_run_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE dashboard_daily (
    d              DATE PRIMARY KEY,
    analyses       INTEGER DEFAULT 0,
    docs           INTEGER DEFAULT 0,
    sentiment_pos  INTEGER DEFAULT 0,
    sentiment_neg  INTEGER DEFAULT 0,
    sentiment_neu  INTEGER DEFAULT 0,
    cost_micro_cny BIGINT DEFAULT 0
);

CREATE INDEX idx_analyses_state ON analyses(state);
CREATE INDEX idx_analyses_created ON analyses(created_at);
CREATE INDEX idx_raw_documents_analysis ON raw_documents(analysis_id);
CREATE INDEX idx_raw_documents_fts ON raw_documents USING GIN (to_tsvector('simple', content));
CREATE INDEX idx_sentiment_doc ON sentiment_results(doc_id);
CREATE INDEX idx_reports_analysis ON reports(analysis_id);

-- +goose Down
DROP TABLE IF EXISTS dashboard_daily;
DROP TABLE IF EXISTS crawl_jobs;
DROP TABLE IF EXISTS report_templates;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS topics;
DROP TABLE IF EXISTS sentiment_results;
DROP TABLE IF EXISTS raw_documents;
DROP TABLE IF EXISTS task_steps;
DROP TABLE IF EXISTS analyses;
DROP TABLE IF EXISTS data_sources;
