#!/bin/bash
# ============================================================
# 微舆舆情 — Ubuntu 24.04 幂等部署脚本（无 Docker，已有 Nginx）
#
# 用法:
#   sudo YUGING_DOMAIN=yuqing.example.com bash scripts/deploy.sh
#
# 环境变量:
#   YUGING_DOMAIN   站点域名（用于 nginx server_name 与 SSL）
#   YUGING_ROOT     安装根目录（默认 /opt/yuging）
#   SKIP_SSL        任意值 = 跳过 certbot SSL 配置
#
# 幂等性承诺:
#   - 已安装/已运行的组件自动检测跳过（nginx/postgresql/redis/go/node）
#   - 已存在的配置文件绝不覆盖（config.yaml / nginx 站点 / 数据库）
#   - 可反复执行，每次只补缺失的部分
# ============================================================
set -euo pipefail

APP_ROOT="${YUGING_ROOT:-/opt/yuging}"
DOMAIN="${YUGING_DOMAIN:-}"
LOG_FILE="$APP_ROOT/data/logs/deploy.log"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB_PASSWORD="${YUGING_DB_PASSWORD:-yuging}"
JWT_SECRET="${YUGING_JWT_SECRET:-}"

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; NC='\033[0m'

# 日志目录必须先存在 — log_* 通过 tee 写入 $LOG_FILE，缺失时 pipefail 会中断脚本
mkdir -p "$APP_ROOT"/{bin,config,data/{storage,logs},web}

log_info()  { echo -e "${GREEN}[INFO]${NC}  $*" | tee -a "$LOG_FILE"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*" | tee -a "$LOG_FILE"; }
log_skip()  { echo -e "${YELLOW}[SKIP]${NC} $*" | tee -a "$LOG_FILE"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" | tee -a "$LOG_FILE"; }

# ============================================================
# 0. 前置检查
# ============================================================
[ "$(id -u)" -eq 0 ] || { log_error "请用 root 运行: sudo bash scripts/deploy.sh"; exit 1; }
[ -f /etc/os-release ] && . /etc/os-release
case "${VERSION_ID:-}" in
  24.04) log_info "Ubuntu ${VERSION_ID} 已确认" ;;
  *) log_warn "非 Ubuntu 24.04（检测到 ${VERSION_ID:-未知}），继续但组件版本可能不匹配" ;;
esac
[ -n "$DOMAIN" ] || log_warn "未设置 YUGING_DOMAIN，nginx 将使用默认 server_name _，SSL 将跳过"

# ============================================================
# 1. 系统依赖（每个组件独立检测，已满足则跳过）
# ============================================================

# --- Nginx（用户已装） ---
if command -v nginx &>/dev/null && nginx -v 2>&1 | grep -qi nginx; then
  log_skip "nginx 已安装: $(nginx -v 2>&1 | head -1) — 跳过安装，仅后续添加站点配置"
else
  apt-get update -y && apt-get install -y nginx
  log_info "nginx 已安装"
fi

# --- PostgreSQL（Ubuntu 24.04 默认 16） ---
if command -v psql &>/dev/null && systemctl is-active --quiet postgresql; then
  log_skip "PostgreSQL 已运行: $(psql --version)"
else
  apt-get update -y && apt-get install -y postgresql postgresql-contrib
  systemctl enable --now postgresql
  log_info "PostgreSQL $(psql --version | awk '{print $3}') 已安装并启动"
fi

# --- Redis ---
if command -v redis-server &>/dev/null && systemctl is-active --quiet redis-server; then
  log_skip "Redis 已运行: $(redis-server --version | head -1)"
else
  apt-get install -y redis-server
  systemctl enable --now redis-server
  log_info "Redis 已安装并启动"
fi

# --- Go 工具链（要求 ≥1.25，apt 自带 1.22 不够） ---
need_go=1
if command -v go &>/dev/null; then
  GO_MINOR=$(go version | grep -oE 'go1\.[0-9]+' | cut -d. -f2)
  [ "${GO_MINOR:-0}" -ge 25 ] && need_go=0
fi
if [ "$need_go" -eq 1 ]; then
  if [ ! -x /usr/local/go/bin/go ]; then
    GO_VER="1.25.5"
    log_info "安装 Go ${GO_VER}（/usr/local/go）..."
    curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz" -o /tmp/go.tgz
    rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm -f /tmp/go.tgz
  fi
  export PATH="/usr/local/go/bin:$PATH"
  log_skip "Go 工具链已就绪: $(go version)"
else
  log_skip "Go 已满足要求: $(go version)"
fi
export PATH="/usr/local/go/bin:$PATH"

# 国内网络加速：proxy.golang.org 在境内不可达（实测 dial tcp i/o timeout）。
# 用 YUGING_GOPROXY 覆盖，设为 "direct" 即关闭镜像。
go env -w GOPROXY="${YUGING_GOPROXY:-https://goproxy.cn,direct}" \
          GOSUMDB=sum.golang.google.cn \
          GOTOOLCHAIN=local
log_info "Go 代理已配置: $(go env GOPROXY)"

# --- Node.js（要求 ≥20.19，apt 自带 18.19 不够） ---
need_node=1
if command -v node &>/dev/null; then
  NODE_MAJOR=$(node -v | cut -d. -f1 | tr -d 'v')
  [ "${NODE_MAJOR:-0}" -ge 20 ] && need_node=0
fi
if [ "$need_node" -eq 1 ]; then
  log_info "安装 Node.js 22 LTS（NodeSource）..."
  curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  apt-get install -y nodejs
  log_skip "Node.js 已就绪: $(node -v)"
else
  log_skip "Node.js 已满足要求: $(node -v)（npm $(npm -v 2>/dev/null || echo ?)）"
fi

# 国内网络加速：registry.npmjs.org 在境内常超时。用 YUGING_NPM_REGISTRY 覆盖。
npm config set registry "${YUGING_NPM_REGISTRY:-https://registry.npmmirror.com}" 2>/dev/null || true
log_info "npm 源已配置: $(npm config get registry 2>/dev/null || echo '(npm 未就绪)')"

# --- Python 3 venv ---
if python3 -c 'import venv' 2>/dev/null; then
  log_skip "Python3 已就绪: $(python3 --version)"
else
  apt-get install -y python3-venv python3-pip
  log_info "python3-venv 已安装"
fi

# ============================================================
# 2. 系统用户 + 平台数据库（已存在则跳过，不覆盖）
# ============================================================
id -u yuging &>/dev/null || useradd -r -m -d "$APP_ROOT" -s /usr/sbin/nologin yuging

if sudo -u postgres psql -lqt | grep -q "yuging_platform"; then
  log_skip "平台数据库 yuging_platform 已存在，跳过创建"
else
  sudo -u postgres createdb yuging_platform
  log_info "平台数据库 yuging_platform 已创建"
fi

# 用户已存在则不重置密码
if sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='yuging'" | grep -q 1; then
  log_skip "数据库用户 yuging 已存在，密码保持不变"
else
  sudo -u postgres psql -c "CREATE USER yuging WITH PASSWORD '${DB_PASSWORD}' CREATEDB;"
  log_info "数据库用户 yuging 已创建（CREATEDB：租户库自动创建需要）"
fi
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE yuging_platform TO yuging;" >/dev/null 2>&1 || true

# ============================================================
# 3. Go 二进制构建
# ============================================================
if [ -x "$APP_ROOT/bin/yuging-server" ] && [ -x "$APP_ROOT/bin/yuging-worker" ] && [ -x "$APP_ROOT/bin/yuging-cli" ]; then
  log_skip "Go 二进制已存在，跳过构建（重编译请删掉后重跑或手动 make build）"
else
  log_info "交叉编译 Go 二进制（linux/amd64, CGO off）..."
  cd "$REPO_ROOT/platform"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$APP_ROOT/bin/" ./cmd/...
  log_info "Go 二进制构建完成: $(ls "$APP_ROOT/bin")"
fi

# ============================================================
# 4. 前端构建
# ============================================================
if [ -f "$APP_ROOT/web/dist/index.html" ]; then
  log_skip "前端 dist/ 已存在，跳过构建（重构建请删掉 dist 后重跑）"
else
  log_info "前端构建中（npm ci && npm run build）..."
  cd "$REPO_ROOT/web"
  npm ci --no-audit --no-fund
  npm run build
  cp -r dist "$APP_ROOT/web/"
  log_info "前端构建完成 → $APP_ROOT/web/dist"
fi

# ============================================================
# 5. Python 引擎 venv + Scrapling（真实数据采集）
# ============================================================
PYTHON_VENV="$APP_ROOT/engines/venv"
if [ -f "$PYTHON_VENV/bin/python" ]; then
  log_skip "Python 引擎 venv 已存在"
else
  python3 -m venv "$PYTHON_VENV"
  log_info "Python venv 已创建"
fi
"$PYTHON_VENV/bin/pip" install -q -r "$REPO_ROOT/engines/requirements.txt"
log_info "Python 依赖已安装"

# Scrapling 浏览器二进制（首次安装 ~150MB，后续跳过）
if "$PYTHON_VENV/bin/python" -c "from scrapling.fetchers import Fetcher" 2>/dev/null; then
  if [ -d "$APP_ROOT/engines/chromium" ] || "$PYTHON_VENV/bin/scrapling" check 2>/dev/null; then
    log_skip "Scrapling 浏览器已安装"
  else
    log_info "安装 Scrapling Playwright Chromium（首次 ~150MB）..."
    "$PYTHON_VENV/bin/scrapling" install --chromium 2>/dev/null || log_warn "Scrapling 浏览器安装失败（非阻塞，可后装）"
  fi
else
  log_warn "Scrapling 未安装，数据采集不可用（检查 requirements.txt）"
fi

# ============================================================
# 6. 配置文件（已存在绝不覆盖）
# ============================================================
if [ -f "$APP_ROOT/config/config.yaml" ]; then
  log_skip "config.yaml 已存在，跳过（变更请手工 diff config.example.yaml）"
else
  cp "$REPO_ROOT/platform/config.example.yaml" "$APP_ROOT/config/config.yaml"
  # 写入部署时已知的数据库连接
  sed -i "s|postgres://yuging:secret@localhost:5432/yuging_platform?sslmode=disable|postgres://yuging:${DB_PASSWORD}@localhost:5432/yuging_platform?sslmode=disable|" \
    "$APP_ROOT/config/config.yaml"
  if [ -n "$JWT_SECRET" ]; then
    sed -i "s|jwtSecret: \"\"|jwtSecret: \"${JWT_SECRET}\"|" "$APP_ROOT/config/config.yaml"
  fi
  chown yuging:yuging "$APP_ROOT/config/config.yaml"
  chmod 600 "$APP_ROOT/config/config.yaml"
  log_info "config.yaml 已生成（数据库连接已写入）"
  [ -n "$JWT_SECRET" ] || log_warn "  ⚠ jwtSecret 为空！请手工编辑 $APP_ROOT/config/config.yaml 填入"
fi

# ============================================================
# 7. 数据库迁移
# ============================================================
log_info "运行平台迁移..."
if "$APP_ROOT/bin/yuging-cli" migrate platform; then
  log_info "迁移完成"
else
  log_warn "迁移 CLI 当前为占位实现；请手工执行 SQL:"
  log_warn "  sudo -u postgres psql -d yuging_platform -f $REPO_ROOT/platform/migrations/platform/0001_init.sql"
fi

# ============================================================
# 8. systemd 服务（unit 已存在则刷新但不覆盖手工修改）
# ============================================================
if systemctl list-unit-files yuging-server.service 2>/dev/null | grep -q yuging-server; then
  log_skip "systemd units 已注册，仅 daemon-reload + 重启"
else
  for unit in "$REPO_ROOT"/scripts/systemd/*.service; do
    cp "$unit" /etc/systemd/system/
  done
  log_info "systemd units 已注册: $(ls "$REPO_ROOT"/scripts/systemd/)"
fi
systemctl daemon-reload
systemctl enable --now yuging-server yuging-worker 2>/dev/null || log_warn "服务启动失败，请查看 journalctl -u yuging-server"
systemctl restart yuging-server yuging-worker 2>/dev/null || true

# ============================================================
# 9. nginx 站点配置（已有配置绝不覆盖）
# ============================================================
NGINX_CONF=/etc/nginx/conf.d/yuging.conf
if [ -f "$NGINX_CONF" ]; then
  log_warn "nginx 站点配置已存在: $NGINX_CONF — 不覆盖"
  log_warn "  变更请手工对比: diff $REPO_ROOT/scripts/nginx.conf $NGINX_CONF"
else
  sed -e "s/your-domain.com/${DOMAIN:-_}/g" \
      -e "s|/etc/letsencrypt/live/your-domain.com|/etc/letsencrypt/live/${DOMAIN:-_}|g" \
      "$REPO_ROOT/scripts/nginx.conf" > "$NGINX_CONF"
  log_info "nginx 站点配置已部署: $NGINX_CONF"
fi
if [ -n "$DOMAIN" ] && [ "${SKIP_SSL:-}" = "" ]; then
  if [ -d "/etc/letsencrypt/live/$DOMAIN" ]; then
    log_skip "SSL 证书已存在（$DOMAIN），跳过 certbot"
  elif command -v certbot &>/dev/null; then
    log_info "签发 SSL 证书..."
    certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --redirect || log_warn "证书签发失败，HTTP 仍可用"
  else
    log_warn "未安装 certbot，跳过 SSL（HTTP 可用；需 HTTPS 请: apt install certbot python3-certbot-nginx 后重跑）"
  fi
else
  log_skip "SSL 跳过（未设 YUGING_DOMAIN 或 SKIP_SSL=1）"
fi
nginx -t && systemctl reload nginx || log_error "nginx 配置校验失败，请检查 $NGINX_CONF"

# ============================================================
# 10. 健康检查
# ============================================================
log_info "等待服务就绪..."
sleep 2
if curl -sf http://127.0.0.1:8080/api/v1/health >/dev/null; then
  log_info "健康检查通过: http://127.0.0.1:8080/api/v1/health → $(curl -s http://127.0.0.1:8080/api/v1/health)"
else
  log_error "健康检查失败！排查: journalctl -u yuging-server -n 50"
fi

echo ""
echo "============================================================"
echo " ✅ 部署完成"
echo "   服务:  yuging-server / yuging-worker (systemd)"
echo "   站点:  ${DOMAIN:-http://服务器IP}（前端静态 + /api 反代）"
echo "   目录:  $APP_ROOT"
echo "   日志:  journalctl -u yuging-server -f"
echo "============================================================"
