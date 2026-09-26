#!/bin/bash
# Hotfix: 执行迁移 0008 修复 created_by 列缺失
set -euo pipefail

echo "=== 盘古舆情 Hotfix: 迁移 0008 ===="
echo "修复: created_by 列缺失导致的生产故障"
echo ""

# 1. 停止服务
echo "[1/6] 停止服务..."
sudo systemctl stop yuqing-server yuqing-worker

# 2. 验证当前 schema 状态
echo "[2/6] 验证当前 schema 状态..."
if sudo -u postgres psql -d yuqing_platform -t -c "\d analyses" | grep -q created_by; then
    echo "警告: created_by 列已存在，迁移可能已执行"
    read -p "是否继续? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "中止执行"
        exit 1
    fi
else
    echo "确认: created_by 列不存在，需要执行迁移"
fi

# 3. 执行迁移
echo "[3/6] 执行迁移 0008..."
cd /opt/yuqing
YUQING_CONFIG=/opt/yuqing/config/config.yaml bin/yuqing-cli migrate platform

# 4. 验证迁移成功
echo "[4/6] 验证迁移成功..."
if sudo -u postgres psql -d yuqing_platform -t -c "\d analyses" | grep -q "created_by"; then
    echo "✓ analyses.created_by 列已创建"
else
    echo "✗ 迁移失败: analyses.created_by 列仍不存在"
    exit 1
fi

if sudo -u postgres psql -d yuqing_platform -t -c "\d reports" | grep -q "created_by"; then
    echo "✓ reports.created_by 列已创建"
else
    echo "✗ 迁移失败: reports.created_by 列仍不存在"
    exit 1
fi

# 5. 重启服务
echo "[5/6] 重启服务..."
sudo systemctl start yuqing-server yuqing-worker

# 等待服务启动
sleep 3

# 6. 验证修复
echo "[6/6] 验证修复..."
if curl -s http://127.0.0.1:8080/api/v1/health | grep -q "ok"; then
    echo "✓ 服务健康检查通过"
else
    echo "✗ 服务健康检查失败"
    sudo systemctl status yuqing-server
    exit 1
fi

echo ""
echo "=== Hotfix 完成 ==="
echo "请测试用户旅程:"
echo "  1. 注册新用户"
echo "  2. 登录"
echo "  3. 创建分析"
echo "  4. 查看数据面板"
