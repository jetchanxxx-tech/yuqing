-- +goose Up
-- 报告中心 P0：新增 created_by 列 + report_version 多版本支持
-- 基于：P2_IMPLEMENTATION_PLAN.html 决策
--
-- 语义：
--   analyses.created_by   — 创建人（创建时写入）；历史数据由 CLI 回填
--   reports.created_by   — 创建人（从 analyses.created_by 继承）
--   reports.report_version — 同一 analysis 的第 N 次报告（从 1 起）

-- ─────────────────────────────────────────────────────────
-- 1. analyses 表新增 created_by
-- ─────────────────────────────────────────────────────────
ALTER TABLE analyses ADD COLUMN created_by UUID;

-- 回填：取 members 表该租户第一个成员（按 created_at 升序）
WITH first_member AS (
    SELECT tenant_id, user_id,
           ROW_NUMBER() OVER (PARTITION BY tenant_id ORDER BY created_at) AS rn
    FROM members
)
UPDATE analyses a
SET created_by = fm.user_id
FROM first_member fm
WHERE a.tenant_id = fm.tenant_id
  AND fm.rn = 1
  AND a.created_by IS NULL;

-- 回填后设非空约束（历史数据已回填完毕）
ALTER TABLE analyses ALTER COLUMN created_by SET NOT NULL;

-- FK + 索引
ALTER TABLE analyses ADD CONSTRAINT fk_analyses_created_by
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX idx_analyses_created_by ON analyses(created_by);

-- ─────────────────────────────────────────────────────────
-- 2. reports 表新增 created_by + report_version
-- ─────────────────────────────────────────────────────────
ALTER TABLE reports ADD COLUMN created_by UUID;
ALTER TABLE reports ADD COLUMN report_version INTEGER NOT NULL DEFAULT 1;

-- 回填：created_by 从 analyses.created_by 继承
UPDATE reports r
SET created_by = a.created_by
FROM analyses a
WHERE r.analysis_id = a.id
  AND r.created_by IS NULL;

-- 回填后设非空约束
ALTER TABLE reports ALTER COLUMN created_by SET NOT NULL;

-- FK + 索引
ALTER TABLE reports ADD CONSTRAINT fk_reports_created_by
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL;
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
