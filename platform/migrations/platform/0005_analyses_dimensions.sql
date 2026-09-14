-- +goose Up
-- 五维度研判结论（背景/热度/情感观点/群体差异/深层原因）随分析结果持久化。
-- 部署顺序硬约束：先执行本迁移再启动新 server —— 新二进制会读写该列，
-- 旧库缺列时所有 analyses 查询报 column does not exist（P0 阻断项）。
ALTER TABLE analyses ADD COLUMN IF NOT EXISTS dimensions JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE analyses DROP COLUMN IF EXISTS dimensions;
