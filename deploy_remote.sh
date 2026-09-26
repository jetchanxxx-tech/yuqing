#!/bin/bash
# 远程部署脚本 - 在本地执行
# 使用方式: bash deploy_remote.sh

set -e

SERVER="101.96.209.90"
PORT="22352"
USER="jet"
SQL_FILE="/tmp/HOTFIX_0008_created_by.sql"

echo "=== 步骤1: 上传修复SQL ==="
scp -P $PORT HOTFIX_0008_created_by.sql ${USER}@${SERVER}:${SQL_FILE}

echo ""
echo "=== 步骤2: 执行远程部署 ==="
ssh -p $PORT ${USER}@${SERVER} << 'ENDSSH'
set -e

echo "=== 检查PostgreSQL服务 ==="
sudo systemctl status postgresql | head -3

echo ""
echo "=== 执行修复SQL ==="
sudo -u postgres psql -d yuqing_db -f /tmp/HOTFIX_0008_created_by.sql

echo ""
echo "=== 验证修复结果 ==="
sudo -u postgres psql -d yuqing_db -c "
SELECT column_name, data_type, is_nullable
FROM information_schema.columns
WHERE table_name = 'analyses' AND column_name = 'created_by';
"

echo ""
echo "=== 重启Go服务 ==="
sudo systemctl restart yuqing-server
sleep 3
sudo systemctl status yuqing-server | head -10

echo ""
echo "=== 验证API响应 ==="
curl -s http://localhost:8080/health | jq .

echo ""
echo "✅ 部署完成！"
ENDSSH

echo ""
echo "=== 部署完成 ==="
