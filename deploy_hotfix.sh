#!/bin/bash
# 生产环境HOTFIX部署脚本
# 执行方式: ssh登录后运行 bash deploy_hotfix.sh

set -e

echo "================================"
echo "HOTFIX_0008 部署脚本"
echo "================================"

# 1. 检查SQL文件
if [ ! -f "/tmp/HOTFIX_0008_created_by.sql" ]; then
    echo "❌ 错误: SQL文件不存在"
    echo "请先上传: scp -P 22352 HOTFIX_0008_created_by.sql jet@101.96.209.90:/tmp/"
    exit 1
fi

# 2. 执行SQL迁移
echo "📋 步骤1: 执行数据库迁移..."
sudo -u postgres psql yuqing_production < /tmp/HOTFIX_0008_created_by.sql

# 3. 验证列已添加
echo "✅ 步骤2: 验证created_by列..."
sudo -u postgres psql yuqing_production -c "\d analyses" | grep created_by

if [ $? -eq 0 ]; then
    echo "✅ created_by列已成功添加"
else
    echo "❌ 错误: created_by列添加失败"
    exit 1
fi

# 4. 重启后端服务
echo "🔄 步骤3: 重启后端服务..."
sudo systemctl restart yuqing-backend

# 5. 等待服务启动
echo "⏳ 等待服务启动..."
sleep 3

# 6. 检查服务状态
echo "🔍 步骤4: 检查服务状态..."
sudo systemctl status yuqing-backend --no-pager

# 7. 测试API
echo "🧪 步骤5: 测试API健康检查..."
curl -s http://localhost:8001/health || echo "⚠️  健康检查失败，请检查日志"

echo ""
echo "================================"
echo "✅ HOTFIX部署完成！"
echo "================================"
echo ""
echo "请检查日志确认无错误:"
echo "  sudo journalctl -u yuqing-backend -n 50 --no-pager"
