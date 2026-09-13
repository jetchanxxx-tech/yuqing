#!/bin/bash
# ============================================================
# 盘古舆情 — 服务器健康自检脚本
#
# 用法:
#   sudo bash scripts/healthcheck.sh              # 交互输出全部检查项
#   sudo bash scripts/healthcheck.sh --json       # JSON 输出（适合 cron/监控采集）
#
# 覆盖: 7 个 systemd 服务 / Go API / 5 个 Python 引擎 /
#       PostgreSQL / Redis / nginx / HTTPS 站点 / 磁盘
#
# 退出码: 0 = 全部正常；1 = 存在异常（JSON 模式下 details 字段给原因）
# 与 systemd 自启的关系: 各 unit 已配 Restart=always + RestartSec=5，
#   本脚本负责「发现」，systemd 负责「拉起」；建议配 systemd timer
#   每 5 分钟跑一次（见 OPS_MANUAL.html「自检与自启」）。
# ============================================================
set -uo pipefail

JSON=0
[ "${1:-}" = "--json" ] && JSON=1

APP_ROOT="${YUQING_ROOT:-/opt/yuqing}"
API_PORT=8080
ENGINE_PORTS="query:8000 media:8001 insight:8002 report:8003 forum:8004"
DOMAIN="${YUQING_DOMAIN:-}"
FAILS=0
FAIL_DETAILS=""

check() { # check <名称> <命令>
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    [ "$JSON" = 0 ] && echo -e "\033[32m[OK]\033[0m   $name"
  else
    [ "$JSON" = 0 ] && echo -e "\033[31m[FAIL]\033[0m $name"
    FAILS=$((FAILS+1))
    FAIL_DETAILS="$FAIL_DETAILS$name; "
  fi
}

http_ok() { # http_ok <url>
  curl -s -m 5 -o /dev/null -w '%{http_code}' "$1" | grep -q '200'
}

# ── 1. systemd 服务（active 即「已拉起」）──────────────────
for unit in yuqing-server yuqing-worker yuqing-query yuqing-media \
            yuqing-insight yuqing-report yuqing-forum; do
  check "systemd $unit" systemctl is-active --quiet "$unit"
done

# ── 2. Go API 与 Python 引擎健康端点 ───────────────────────
check "Go API /api/v1/health" http_ok "http://127.0.0.1:${API_PORT}/api/v1/health"
for pair in $ENGINE_PORTS; do
  name="${pair%%:*}"; port="${pair##*:}"
  check "引擎 $name :$port /health" http_ok "http://127.0.0.1:${port}/health"
done

# ── 3. 依赖组件 ────────────────────────────────────────────
check "PostgreSQL" pg_isready -q
check "Redis" redis-cli ping
check "nginx 配置" nginx -t

# ── 4. 站点（有域名时）─────────────────────────────────────
if [ -n "$DOMAIN" ]; then
  check "HTTPS 站点 https://$DOMAIN" http_ok "https://$DOMAIN/"
fi

# ── 5. 磁盘（可用空间 < 1GB 视为异常）──────────────────────
check "磁盘空间（$APP_ROOT）" sh -c "df -BG --output=avail '$APP_ROOT' | tail -1 | tr -d ' ' | sed 's/G//' | awk '\$1 >= 1'"

# ── 汇总 ──────────────────────────────────────────────────
if [ "$JSON" = 1 ]; then
  if [ "$FAILS" = 0 ]; then
    echo '{"status":"ok","fails":0}'
  else
    echo "{\"status\":\"degraded\",\"fails\":$FAILS,\"details\":\"$FAIL_DETAILS\"}"
  fi
else
  echo "----------------------------------------"
  if [ "$FAILS" = 0 ]; then
    echo -e "\033[32m全部检查通过\033[0m"
  else
    echo -e "\033[31m$FAILS 项异常: $FAIL_DETAILS\033[0m"
  fi
fi

exit $([ "$FAILS" = 0 ] && echo 0 || echo 1)
