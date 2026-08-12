#!/bin/bash
# yuging platform — idempotent deployment script.
# Run as: sudo bash deploy.sh
set -euo pipefail

APP_ROOT="${YUGING_ROOT:-/opt/yuging}"
LOG_FILE="$APP_ROOT/data/logs/deploy.log"
DOMAIN="${YUGING_DOMAIN:-}"

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; NC='\033[0m'
log_info()  { echo -e "${GREEN}[INFO]${NC}  $*" | tee -a "$LOG_FILE"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*" | tee -a "$LOG_FILE"; }
log_skip()  { echo -e "${YELLOW}[SKIP]${NC} $*" | tee -a "$LOG_FILE"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" | tee -a "$LOG_FILE"; }

mkdir -p "$APP_ROOT"/{bin,engines,web,config,data/{storage,logs},scripts}

# --- Nginx ---
check_nginx() { command -v nginx &>/dev/null && { log_skip "nginx installed"; return 0; }; return 1; }
install_nginx()  { apt-get install -y nginx; log_info "nginx installed"; }
check_nginx || install_nginx

# --- PostgreSQL ---
check_postgres() { systemctl is-active --quiet postgresql 2>/dev/null && { log_skip "PostgreSQL running"; return 0; }; return 1; }
install_postgres() { apt-get install -y postgresql-15 postgresql-contrib-15; systemctl enable --now postgresql; }
check_postgres || install_postgres

# --- Redis ---
check_redis() { systemctl is-active --quiet redis-server 2>/dev/null && { log_skip "Redis running"; return 0; }; return 1; }
install_redis() { apt-get install -y redis-server; systemctl enable --now redis-server; }
check_redis || install_redis

# --- Platform DB ---
check_platform_db() { sudo -u postgres psql -lqt 2>/dev/null | grep -q "yuging_platform" && { log_skip "platform DB exists"; return 0; }; return 1; }
create_platform_db() {
    sudo -u postgres createdb yuging_platform 2>/dev/null || true
    sudo -u postgres psql -c "CREATE USER yuging WITH PASSWORD 'yuging' CREATEDB;" 2>/dev/null || true
    sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE yuging_platform TO yuging;" 2>/dev/null || true
}
check_platform_db || create_platform_db

# --- Go binaries ---
check_go_binaries() { [ -x "$APP_ROOT/bin/yuging-server" ] && [ -x "$APP_ROOT/bin/yuging-worker" ] && { log_skip "Go binaries exist"; return 0; }; return 1; }
build_go() {
    cd platform
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$APP_ROOT/bin/" ./cmd/...
    log_info "Go binaries built"
}
check_go_binaries || build_go

# --- Frontend ---
check_frontend() { [ -f "$APP_ROOT/web/dist/index.html" ] && { log_skip "frontend dist exists"; return 0; }; return 1; }
build_frontend() {
    cd web && npm ci && npm run build
    cp -r dist "$APP_ROOT/web/"
    log_info "frontend built"
}
check_frontend || build_frontend

# --- Nginx config ---
check_nginx_config() { [ -f /etc/nginx/conf.d/yuging.conf ] && { log_warn "nginx config exists, skip overwrite"; return 0; }; return 1; }
install_nginx_config() {
    cp scripts/nginx.conf /etc/nginx/conf.d/yuging.conf
    sed -i "s/your-domain.com/$DOMAIN/g" /etc/nginx/conf.d/yuging.conf
    nginx -t && systemctl reload nginx
    log_info "nginx config deployed"
}
check_nginx_config || install_nginx_config

# --- systemd ---
check_systemd() { systemctl list-unit-files yuging-server.service 2>/dev/null | grep -q "yuging-server" && { log_skip "systemd units exist"; return 0; }; return 1; }
install_systemd() {
    cp scripts/systemd/*.service /etc/systemd/system/
    systemctl daemon-reload
    systemctl enable yuging-server yuging-worker
    systemctl restart yuging-server yuging-worker
    log_info "systemd services started"
}
check_systemd || install_systemd

# --- Database migrations ---
log_info "running platform migrations..."
"$APP_ROOT/bin/yuging-cli" migrate platform || log_warn "migration may have already been applied (goose is idempotent)"

# --- Config ---
if [ -f "$APP_ROOT/config/config.yaml" ]; then
    log_warn "config.yaml exists, skip overwrite"
else
    cp platform/config.example.yaml "$APP_ROOT/config/config.yaml"
    log_info "config.yaml created — please edit it"
fi

# --- Health check ---
sleep 2
curl -sf http://localhost:8080/api/v1/health && log_info "Health: PASS" || log_error "Health: FAIL"

echo ""
echo "=== yuging deployment complete ==="
echo "  API:  http://localhost:8080/api/v1/health"
echo "  Web:  http://localhost/"
echo "  Logs: $APP_ROOT/data/logs/"
