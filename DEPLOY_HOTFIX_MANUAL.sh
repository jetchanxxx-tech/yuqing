#!/bin/bash
# HOTFIX部署脚本 - 手动执行版本
# 日期: 2026-09-26
# 目的: 修复生产环境analyses表缺少created_by列的bug

set -e  # 遇到错误立即退出

echo "=========================================="
echo "HOTFIX 0008: 添加 created_by 列"
echo "=========================================="
echo ""

# 1. 创建SQL文件
echo "步骤1: 创建SQL迁移文件..."
cat > /tmp/HOTFIX_0008_created_by.sql << 'EOSQL'
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
EOSQL

echo "✓ SQL文件已创建: /tmp/HOTFIX_0008_created_by.sql"
echo ""

# 2. 执行SQL迁移
echo "步骤2: 执行SQL迁移..."
sudo -u postgres psql yuqing_production < /tmp/HOTFIX_0008_created_by.sql
echo ""

# 3. 验证列已添加
echo "步骤3: 验证列已添加..."
sudo -u postgres psql yuqing_production -c "\d analyses" | grep created_by
if [ $? -eq 0 ]; then
    echo "✓ created_by列已成功添加"
else
    echo "✗ 警告: 未找到created_by列"
    exit 1
fi
echo ""

# 4. 重启后端服务
echo "步骤4: 重启后端服务..."
sudo systemctl restart yuqing-backend
sleep 3
echo ""

# 5. 检查服务状态
echo "步骤5: 检查服务状态..."
sudo systemctl status yuqing-backend --no-pager -l
echo ""

# 6. 测试API健康检查
echo "步骤6: 测试API健康检查..."
curl -s http://localhost:8001/health | jq .
echo ""

echo "=========================================="
echo "✓ HOTFIX部署完成！"
echo "=========================================="
echo ""
echo "下一步:"
echo "1. 测试创建分析API: curl -X POST http://localhost:8001/api/v1/analyses ..."
echo "2. 检查日志: sudo journalctl -u yuqing-backend -n 50"
echo "3. 如果一切正常，记录到部署日志"
