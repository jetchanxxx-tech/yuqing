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
# 检测 ensurepip 而非 venv：基础 python3 自带 venv 模块，但 Ubuntu 把
# ensurepip 拆到独立的 python3.x-venv 包。只测 `import venv` 会误判通过，
# 直到真正创建 venv 时才以 "ensurepip is not available" 失败并中断部署。
if python3 -c 'import ensurepip' 2>/dev/null; then
  log_skip "Python3 venv 已就绪: $(python3 --version)"
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
  # 逐个显式命名：`-o dir/ ./cmd/...` 会按包目录名产出 cli/server/worker，
  # 而 systemd unit 与下方检查都引用 yuging-* 前缀（实测因此启动失败）。
  for pkg in server worker cli; do
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
      -o "$APP_ROOT/bin/yuging-$pkg" "./cmd/$pkg"
  done
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
# 以 bin/pip 判定 venv 是否完好：venv 创建中途失败（如缺 ensurepip）会留下
# 含 bin/python 但无 pip 的残缺目录。只测 bin/python 会把残venv当完成品跳过，
# 下一步调用 pip 时报 "No such file or directory"（实测）。残缺则删掉重建。
if [ -x "$PYTHON_VENV/bin/pip" ]; then
  log_skip "Python 引擎 venv 已存在"
else
  [ -d "$PYTHON_VENV" ] && { log_warn "检测到残缺 venv，删除重建"; rm -rf "$PYTHON_VENV"; }
  python3 -m venv "$PYTHON_VENV"
  log_info "Python venv 已创建"
fi
# PyPI 官方源境内同样慢：默认走清华镜像，用 YUGING_PIP_INDEX 覆盖。
"$PYTHON_VENV/bin/pip" install -q \
  -i "${YUGING_PIP_INDEX:-https://pypi.tuna.tsinghua.edu.cn/simple}" \
  -r "$REPO_ROOT/engines/requirements.txt"
log_info "Python 依赖已安装"

# Scrapling 浏览器二进制（首次安装 ~150MB，后续跳过）
if "$PYTHON_VENV/bin/python" -c "from scrapling.fetchers import Fetcher" 2>/dev/null; then
  # 以 Playwright 浏览器缓存目录判定，避免依赖 scrapling 的 CLI 子命令名。
  PW_CACHE="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}"
  if [ -d "$PW_CACHE" ] && [ -n "$(ls -A "$PW_CACHE" 2>/dev/null)" ]; then
    log_skip "Scrapling 浏览器已安装 ($PW_CACHE)"
  else
    log_info "安装 Scrapling 浏览器（首次 ~150MB）..."
    # 不加 --chromium：scrapling install 不接受该参数，误传会直接失败。
    # 不吞 stderr：失败原因必须可见，否则只能看到一句无信息的 WARN。
    if "$PYTHON_VENV/bin/scrapling" install 2>&1 | tail -5; then
      log_info "Scrapling 浏览器安装完成"
    else
      log_warn "Scrapling 浏览器安装失败（非阻塞，可稍后手动执行: $PYTHON_VENV/bin/scrapling install）"
    fi
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
# 引擎源码需对运行用户可读（unit 以 yuging 身份启动 uvicorn）
chmod -R a+rX "$REPO_ROOT/engines" 2>/dev/null || true

# 启动前做 Python 语法检查：引擎里的中文文案若混入 ASCII 双引号，会提前终止
# 字符串字面量导致 SyntaxError，服务反复重启且日志被堆栈淹没（实测发生过）。
PY_SYNTAX_BAD=0
for py in "$REPO_ROOT"/engines/*/main.py "$REPO_ROOT"/engines/common/*.py; do
  [ -f "$py" ] || continue
  if ! "$PYTHON_VENV/bin/python" -m py_compile "$py" 2>/dev/null; then
    log_error "Python 语法错误: $py"
    PY_SYNTAX_BAD=1
  fi
done
if [ "$PY_SYNTAX_BAD" -eq 1 ]; then
  log_warn "存在语法错误的引擎将无法启动，请修复后重跑（其余服务继续部署）"
  "$PYTHON_VENV/bin/python" -m py_compile "$REPO_ROOT"/engines/*/main.py || true
fi

# Bocha API Key 交给引擎进程（Bocha 搜索 + Scrapling 采集链路需要）
if [ -n "${BOCHA_API_KEY:-}" ]; then
  printf 'BOCHA_API_KEY=%s\n' "$BOCHA_API_KEY" > "$APP_ROOT/config/engines.env"
  chmod 600 "$APP_ROOT/config/engines.env"
  chown yuging:yuging "$APP_ROOT/config/engines.env" 2>/dev/null || true
  log_info "引擎环境变量已写入: $APP_ROOT/config/engines.env"
fi

if systemctl list-unit-files yuging-server.service 2>/dev/null | grep -q yuging-server; then
  log_skip "systemd units 已注册，仅 daemon-reload + 重启"
else
  for unit in "$REPO_ROOT"/scripts/systemd/*.service; do
    cp "$unit" /etc/systemd/system/
  done
  log_info "systemd units 已注册: $(ls "$REPO_ROOT"/scripts/systemd/)"
fi
systemctl daemon-reload

# Go 平台进程
systemctl enable --now yuging-server yuging-worker 2>/dev/null || log_warn "服务启动失败，请查看 journalctl -u yuging-server"
systemctl restart yuging-server yuging-worker 2>/dev/null || true

# Python 引擎（5 个）— 数据采集/分析/报告/辩论链路
ENGINE_UNITS="yuging-query yuging-media yuging-insight yuging-report yuging-forum"
systemctl enable --now $ENGINE_UNITS 2>/dev/null || log_warn "部分引擎启动失败，逐个排查: systemctl status yuging-query"
for u in $ENGINE_UNITS; do
  if systemctl is-active --quiet "$u"; then
    log_info "引擎已启动: $u"
  else
    log_warn "引擎未启动: $u（查看 journalctl -u $u）"
  fi
done

# ============================================================
# 9. nginx 站点配置（已有配置绝不覆盖）
# ============================================================
# 先排掉 default_server 冲突：Ubuntu 自带的 sites-enabled/default 占着
# 0.0.0.0:80 的 default_server，IP 直连时请求会落到 /var/www/html 欢迎页
# 而非本应用（实测：外部访问首页返回 "Welcome to nginx!"）。
# 处理方式：把冲突站点移出 sites-enabled 目录并备份到 /root/（不是改名，
# 因为 include 会加载该目录下所有文件）。
if [ -d /etc/nginx/sites-enabled ]; then
  for site in /etc/nginx/sites-enabled/*; do
    [ -f "$site" ] || continue
    if grep -q 'default_server' "$site" 2>/dev/null; then
      backup="/root/nginx-$(basename "$site").disabled-by-pangu"
      mv "$site" "$backup"
      log_warn "已停用占用 default_server 的站点: $site → $backup"
    fi
  done
fi

NGINX_CONF=/etc/nginx/conf.d/yuging.conf
# 有证书才用 HTTPS 模板：nginx.conf 里的 ssl_certificate 指向不存在的文件会让
# `nginx -t` 直接失败，进而整个 reload 被拒绝、站点完全不工作（实测）。
if [ -n "$DOMAIN" ] && [ -f "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" ]; then
  NGINX_TEMPLATE="$REPO_ROOT/scripts/nginx.conf"
  log_info "检测到 $DOMAIN 的证书，使用 HTTPS 模板"
else
  NGINX_TEMPLATE="$REPO_ROOT/scripts/nginx-http.conf"
  log_warn "未检测到证书，使用 HTTP-only 模板（后续签发证书后重跑本脚本即可切换）"
fi

if [ -f "$NGINX_CONF" ]; then
  log_warn "nginx 站点配置已存在: $NGINX_CONF — 不覆盖"
  log_warn "  变更请手工对比: diff $NGINX_TEMPLATE $NGINX_CONF"
else
  sed -e "s/your-domain.com/${DOMAIN:-_}/g" \
      -e "s|/etc/letsencrypt/live/your-domain.com|/etc/letsencrypt/live/${DOMAIN:-_}|g" \
      "$NGINX_TEMPLATE" > "$NGINX_CONF"
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
