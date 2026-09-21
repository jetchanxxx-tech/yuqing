#!/bin/bash
# RSSHub 路由验证脚本 - 直接请求公共实例验证路由格式
set -e

BASE="https://rsshub.app"
echo "=== RSSHub 路由验证 (公共实例) ==="
echo

# 微博热搜
echo "1. 微博热搜 /weibo/search/hot"
curl -sS -m 15 "${BASE}/weibo/search/hot" | head -c 500 | grep -q "<rss" && echo "   ✓ RSS 有效" || echo "   ✗ 失败"
echo

# B站热搜
echo "2. B站热搜 /bilibili/hot-search"
curl -sS -m 15 "${BASE}/bilibili/hot-search" | head -c 500 | grep -q "<rss" && echo "   ✓ RSS 有效" || echo "   ✗ 失败"
echo

# 知乎热榜
echo "3. 知乎热榜 /zhihu/hotlist"
curl -sS -m 15 "${BASE}/zhihu/hotlist" | head -c 500 | grep -q "<rss" && echo "   ✓ RSS 有效" || echo "   ✗ 失败"
echo

# 抖音热榜（需验证是否存在）
echo "4. 抖音热榜 /douyin/hot（需核实路由名）"
curl -sS -m 15 "${BASE}/douyin/hot" 2>&1 | head -c 300 | grep -qi "not found\|404" && echo "   ✗ 路由不存在" || echo "   ? 需人工确认"
echo

# 小红书热榜
echo "5. 小红书 /xiaohongshu/user/xxx（通用路由，热榜待确认）"
echo "   ? 小红书无公开热榜路由，跳过"
echo

echo "=== 验证完成 ==="
