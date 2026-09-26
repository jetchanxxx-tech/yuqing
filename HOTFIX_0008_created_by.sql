-- HOTFIX_0008: 添加缺失的 created_by 列
-- 日期: 2026-09-26
-- 原因: 生产环境analyses表缺少created_by列导致API 500错误

BEGIN;

-- 检查列是否已存在
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'analyses' AND column_name = 'created_by'
    ) THEN
        ALTER TABLE analyses ADD COLUMN created_by VARCHAR(255);
        RAISE NOTICE 'Added created_by column to analyses table';
    ELSE
        RAISE NOTICE 'created_by column already exists';
    END IF;
END $$;

COMMIT;
