-- +goose Up
-- 报告中心 P0：新增 created_by 列 + report_version 多版本支持
-- 基于：P2_IMPLEMENTATION_PLAN.html 决策
--
-- 语义：
--   analyses.created_by   — 创建人（创建时写入）；历史数据无可信归属时拒绝迁移
--   reports.created_by   — 创建人（从 analyses.created_by 继承）
--   reports.report_version — 同一 analysis 的第 N 次报告（从 1 起）

-- ─────────────────────────────────────────────────────────
-- 1. analyses 表新增 created_by
-- ─────────────────────────────────────────────────────────
ALTER TABLE analyses ADD COLUMN IF NOT EXISTS created_by TEXT;
ALTER TABLE analyses ALTER COLUMN created_by DROP DEFAULT;

-- 成员身份不能证明谁创建了历史分析；拒绝缺失/无效的归属，不凭空分配。
-- 迁移前需在 v7 克隆核对来源，并显式回填可信用户 ID；失败不会推进 goose 版本。
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM analyses WHERE created_by IS NULL OR btrim(created_by) = '') THEN
        RAISE EXCEPTION 'cannot backfill analyses.created_by: verified creator missing; resolve historical ownership before migration';
    END IF;
    IF EXISTS (SELECT 1 FROM analyses a LEFT JOIN users u ON u.id = a.created_by WHERE u.id IS NULL) THEN
        RAISE EXCEPTION 'cannot backfill analyses.created_by: creator does not exist in users';
    END IF;
END;
$$;
-- +goose StatementEnd

-- 只对已确认的归属设非空约束
ALTER TABLE analyses ALTER COLUMN created_by SET NOT NULL;

-- FK + 索引
ALTER TABLE analyses ADD CONSTRAINT fk_analyses_created_by
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_analyses_created_by ON analyses(created_by);

-- ─────────────────────────────────────────────────────────
-- 2. reports 表新增 created_by + report_version
-- ─────────────────────────────────────────────────────────
ALTER TABLE reports ADD COLUMN IF NOT EXISTS created_by TEXT;
ALTER TABLE reports ALTER COLUMN created_by DROP DEFAULT;
ALTER TABLE reports ADD COLUMN IF NOT EXISTS report_version INTEGER NOT NULL DEFAULT 1;
ALTER TABLE reports ALTER COLUMN report_version DROP DEFAULT;
ALTER TABLE reports ALTER COLUMN report_version TYPE INTEGER
    USING trim(leading 'v' from report_version::text)::integer;
ALTER TABLE reports ALTER COLUMN report_version SET DEFAULT 1;

-- 回填：created_by 从 analyses.created_by 继承
UPDATE reports r
SET created_by = a.created_by
FROM analyses a
WHERE r.analysis_id = a.id AND r.tenant_id = a.tenant_id
  AND (r.created_by IS NULL OR r.created_by = '');

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM reports WHERE created_by IS NULL OR btrim(created_by) = '') THEN
        RAISE EXCEPTION 'cannot backfill reports.created_by: verified creator missing or analysis tenant mismatch';
    END IF;
END;
$$;
-- +goose StatementEnd

-- 已确认的报告归属才可设非空约束
ALTER TABLE reports ALTER COLUMN created_by SET NOT NULL;

-- FK + 索引
ALTER TABLE reports ADD CONSTRAINT fk_reports_created_by
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;
CREATE INDEX idx_reports_created_by ON reports(created_by);

-- 回填 report_version：同一 analysis 按 created_at 升序编号
WITH versioned AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY analysis_id ORDER BY created_at, id) AS rn
    FROM reports
    WHERE report_version = 1   -- 默认值行才有意义重编
)
UPDATE reports r
SET report_version = v.rn
FROM versioned v
WHERE r.id = v.id AND r.report_version = 1;

-- +goose Down
-- 回滚顺序：先删索引/约束，再删列

DROP INDEX IF EXISTS idx_reports_created_by;
DROP INDEX IF EXISTS idx_analyses_created_by;

ALTER TABLE reports DROP CONSTRAINT IF EXISTS fk_reports_created_by;
ALTER TABLE analyses DROP CONSTRAINT IF EXISTS fk_analyses_created_by;

ALTER TABLE reports DROP COLUMN IF EXISTS report_version;
ALTER TABLE reports DROP COLUMN IF EXISTS created_by;
ALTER TABLE analyses DROP COLUMN IF EXISTS created_by;
