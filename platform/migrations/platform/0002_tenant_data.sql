-- +goose Up
-- MVP 持久化接线：业务数据暂存 platform 库（tenant_id 列隔离）。
-- database-per-tenant 物理隔离是长期演进（见 README 路线图），
-- 本轮先把「重启即丢账号/任务」的生产缺陷修掉。

-- 分析任务（对齐 analysis.AnalysisResult 内存模型）
CREATE TABLE analyses (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    name           TEXT NOT NULL,
    analysis_type  TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'queued',
    progress       INTEGER NOT NULL DEFAULT 0,
    error_code     TEXT NOT NULL DEFAULT '',
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    keywords       JSONB NOT NULL DEFAULT '[]',
    sources        JSONB NOT NULL DEFAULT '[]',
    doc_count      INTEGER NOT NULL DEFAULT 0,
    summary        TEXT NOT NULL DEFAULT '',
    warning        TEXT NOT NULL DEFAULT '',
    sentiments     JSONB NOT NULL DEFAULT '[]',
    topics         JSONB NOT NULL DEFAULT '[]',
    report_id      TEXT NOT NULL DEFAULT '',
    report_content TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_analyses_tenant_created ON analyses(tenant_id, created_at);

-- 采集文档（对齐 analysis.Document）
CREATE TABLE raw_documents (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    analysis_id  TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    source_type  TEXT NOT NULL DEFAULT '',
    source_name  TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ,
    content_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_raw_documents_analysis ON raw_documents(tenant_id, analysis_id);

-- 报告记录（对齐 report.Report）
CREATE TABLE reports (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    analysis_id TEXT NOT NULL,
    format      TEXT NOT NULL DEFAULT 'html',
    status      TEXT NOT NULL DEFAULT 'completed',
    file_key    TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_reports_analysis ON reports(tenant_id, analysis_id);

-- 告警规则（对齐 alert 内存模型）
CREATE TABLE alerts (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    rule_json   JSONB NOT NULL DEFAULT '{}',
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 平台设置（admin 在线配置持久化：Bocha/DeepSeek key 重启不丢）
CREATE TABLE platform_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS platform_settings;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS raw_documents;
DROP TABLE IF EXISTS analyses;
