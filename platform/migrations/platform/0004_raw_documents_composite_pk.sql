-- +goose Up
-- raw_documents.id 原是全局主键：两个租户采集到同一篇内容（ID=content_hash）
-- 时，后写入的租户整行被 ON CONFLICT DO NOTHING 跳过 —— 租户 B 的
-- /result 里会静默缺文档。改为 (tenant_id, analysis_id, id) 复合主键，
-- 同租户同分析内去重（幂等重跑），跨租户/跨分析互不冲突。
ALTER TABLE raw_documents DROP CONSTRAINT raw_documents_pkey;
ALTER TABLE raw_documents ADD PRIMARY KEY (tenant_id, analysis_id, id);
-- id 不再是主键后仍需按 id 定位（可选场景），保留单列索引。
CREATE INDEX IF NOT EXISTS idx_raw_documents_id ON raw_documents(id);

-- +goose Down
DROP INDEX IF EXISTS idx_raw_documents_id;
ALTER TABLE raw_documents DROP CONSTRAINT raw_documents_pkey;
ALTER TABLE raw_documents ADD PRIMARY KEY (id);
