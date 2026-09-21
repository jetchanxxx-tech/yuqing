# 生产部署

本文档涵盖 Ubuntu 24.04 标准部署流程与 yuqing2 CentOS Stream 9 生产环境的完整配置。

## 🎯 部署概览

| 环境 | 操作系统 | 域名 | 状态 |
|------|---------|------|------|
| **标准部署** | Ubuntu 24.04 LTS | 自定义 | 参考流程 |
| **yuqing2 生产** | CentOS Stream 9 | yuqing2.pangu-cloud.com | ✅ 在线 |
| 老机 47.120.20.10 | - | - | ❌ 已废弃 |

**部署铁律**（用户明令）：
- 🔴 **一切编译/构建只在本地完成后上传**
- 服务器只做：解压 → 配置 → 迁移 → 启停
- 原因：生产服务器内存小，任何构建都可能打满内存打死 sshd

## 📋 标准部署流程（Ubuntu 24.04）

### 前置要求

- Ubuntu 24.04 LTS（4C8G 推荐）
- 域名解析到服务器 IP
- Root 或 sudo 权限
- 开放端口：22 / 80 / 443

### 一键部署

```bash
# 克隆仓库
git clone https://github.com/jetchanxxx-tech/yuqing.git
cd yuqing

# 设置域名并部署（幂等脚本，已装组件自动 [SKIP]）
sudo YUQING_DOMAIN=your-domain.com bash scripts/deploy.sh
```

**脚本做什么**：
1. 检测并安装：nginx / postgresql-15 / redis / go 1.25 / node 18
2. 本地交叉编译 Go 二进制（`GOOS=linux GOARCH=amd64`）
3. 本地构建前端（`npm run build`）
4. 上传二进制与静态文件到 `/opt/yuqing`
5. 创建 systemd units（7 个服务）
6. 配置 nginx（HTTP/HTTPS 自动选择）
7. acme.sh 申请证书（有域名时）
8. 运行数据库迁移
9. 启动全部服务

### 手动部署步骤

#### 1. 本地编译（必须在本地）

```bash
# Go 平台层
cd platform
make test  # 确保全绿
GOOS=linux GOARCH=amd64 go build -o bin/yuqing-server ./cmd/server
GOOS=linux GOARCH=amd64 go build -o bin/yuqing-worker ./cmd/worker
GOOS=linux GOARCH=amd64 go build -o bin/yuqing-cli ./cmd/cli

# 前端
cd ../web
npm ci
npm run build  # → dist/

# Python 引擎（打包依赖）
cd ../engines
pip download -r requirements.txt -d wheels/
```

#### 2. 上传到服务器

```bash
# 使用 scp 或 rsync
scp -r platform/bin/{yuqing-*} user@server:/opt/yuqing/bin/
scp -r web/dist user@server:/opt/yuqing/web/
scp -r engines user@server:/opt/yuqing/
```

#### 3. 安装依赖

```bash
# 服务器端
ssh user@server

# PostgreSQL 15
sudo apt update
sudo apt install -y postgresql-15 postgresql-client-15

# Redis
sudo apt install -y redis-server

# nginx
sudo apt install -y nginx

# Python 虚拟环境
cd /opt/yuqing/engines
python3 -m venv venv
source venv/bin/activate
pip install --no-index --find-links=wheels/ -r requirements.txt
scrapling install --chromium
```

#### 4. 配置数据库

```bash
sudo -u postgres psql

CREATE DATABASE yuqing_platform;
CREATE USER yuqing WITH PASSWORD 'your-secure-password';
GRANT ALL PRIVILEGES ON DATABASE yuqing_platform TO yuqing;
\q
```

#### 5. 运行迁移

```bash
cd /opt/yuqing
./bin/yuqing-cli migrate platform
```

#### 6. 配置 systemd

```bash
# 复制 unit 文件
sudo cp scripts/systemd/*.service /etc/systemd/system/

# 创建 drop-in 配置
sudo mkdir -p /etc/systemd/system/yuqing-server.service.d
sudo tee /etc/systemd/system/yuqing-server.service.d/bootstrap-admin.conf <<EOF
[Service]
Environment="YUQING_BOOTSTRAP_ADMIN_EMAIL=admin@pangu.com"
EnvironmentFile=/opt/yuqing/config/engines.env
EOF

# 启动服务
sudo systemctl daemon-reload
sudo systemctl enable yuqing-{server,query,insight,report}
sudo systemctl start yuqing-{server,query,insight,report}
```

#### 7. 配置 nginx

```bash
# 有域名走 HTTPS
sudo cp scripts/nginx-ssl.conf /etc/nginx/sites-available/yuqing
sudo ln -s /etc/nginx/sites-available/yuqing /etc/nginx/sites-enabled/

# 修改域名
sudo sed -i "s/yuqing.pangu-cloud.com/your-domain.com/g" /etc/nginx/sites-enabled/yuqing

# 测试并重载
sudo nginx -t
sudo systemctl reload nginx
```

#### 8. 申请 SSL 证书

```bash
# 安装 acme.sh
curl https://get.acme.sh | sh

# 申请证书
~/.acme.sh/acme.sh --issue -d your-domain.com --webroot /opt/yuqing/web/dist

# 安装证书
~/.acme.sh/acme.sh --install-cert -d your-domain.com \
  --key-file /etc/nginx/ssl/yuqing.key \
  --fullchain-file /etc/nginx/ssl/yuqing.crt \
  --reloadcmd "systemctl reload nginx"
```

### 验收清单

```bash
# 1. 检查服务状态
systemctl status yuqing-server
systemctl status yuqing-query
systemctl status yuqing-insight
systemctl status yuqing-report

# 2. 测试 API 健康
curl https://your-domain.com/api/v1/health

# 3. 注册测试账号
curl -X POST https://your-domain.com/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","password":"Test123!","name":"Tester"}'

# 4. 创建分析任务（需先获取 token）
curl -X POST https://your-domain.com/api/v1/analyses \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"name":"测试","keywords":["测试"],"sources":["weibo"]}'

# 5. 检查热榜
curl https://your-domain.com/api/v1/trends \
  -H "Authorization: Bearer <token>"
```

## 🏭 yuqing2 生产环境专章（CentOS Stream 9）

> 环境：101.96.209.90:22352（jet，sudo），域名 yuqing2.pangu-cloud.com，4C/3.6Gi/40G  
> 与 Ubuntu 流程**不通用**：dnf 而非 apt、PG15 需 PGDG 源、nginx 为 oneinstack 源码版...

### 环境差异对比

| 项目 | Ubuntu 24.04 | CentOS Stream 9 (yuqing2) |
|------|-------------|---------------------------|
| 包管理器 | apt | dnf |
| PostgreSQL | apt 官方源 | PGDG 源 + `--nobest` |
| nginx | apt 二进制包 | oneinstack 源码编译 |
| nginx 配置 | sites-available/enabled | vhost include 机制 |
| Node.js | 系统 PATH | /usr/local/node/bin（需全路径） |
| MySQL | 无 | root/nishi250（用户既有，勿动） |
| Redis | 7.x | 8.4（空密码） |

### PostgreSQL 15 安装（PGDG）

```bash
# CentOS Stream 9 需 PGDG 源（默认仓库无 PG15）
sudo dnf install -y https://download.postgresql.org/pub/repos/yum/reporpms/EL-9-x86_64/pgdg-redhat-repo-latest.noarch.rpm

# ⚠️ 必须 --nobest：CentOS 9 默认 OpenSSL 3.x，PG15 依赖 OpenSSL 1.1.1
sudo dnf install -y --nobest postgresql15-server postgresql15-contrib

# 初始化与启动
sudo /usr/pgsql-15/bin/postgresql-15-setup initdb
sudo systemctl enable postgresql-15
sudo systemctl start postgresql-15

# 配置密码
sudo -u postgres psql
ALTER USER postgres PASSWORD 'Yuq2Pg_2026!';
CREATE DATABASE yuqing_platform;
\q

# 允许本地密码认证（编辑 /var/lib/pgsql/15/data/pg_hba.conf）
# 改 peer → md5
sudo systemctl restart postgresql-15
```

### nginx vhost 配置（oneinstack）

```bash
# oneinstack 源码版，配置在 /usr/local/nginx/conf
# vhost 使用 include 机制

sudo tee /usr/local/nginx/conf/vhost/yuqing2.conf <<'EOF'
server {
    listen 80;
    server_name yuqing2.pangu-cloud.com;
    
    location /.well-known/acme-challenge/ {
        root /opt/yuqing/web/dist;
    }
    
    location / {
        return 301 https://$server_name$request_uri;
    }
}

server {
    listen 443 ssl http2;
    server_name yuqing2.pangu-cloud.com;
    
    ssl_certificate /etc/nginx/ssl/yuqing2.crt;
    ssl_certificate_key /etc/nginx/ssl/yuqing2.key;
    
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_buffering off;
    }
    
    location / {
        root /opt/yuqing/web/dist;
        try_files $uri $uri/ /index.html;
    }
}
EOF

# 测试并重载
/usr/local/nginx/sbin/nginx -t
/usr/local/nginx/sbin/nginx -s reload
```

### RSSHub 安装（含 Playwright）

```bash
# 1. 本地构建 RSSHub（避免服务器 OOM）
# 本地机器：
cd /path/to/RSSHub
npm ci
npm run build
tar czf rsshub-dist.tar.gz lib/ package.json package-lock.json

# 2. 上传到服务器
scp rsshub-dist.tar.gz jet@101.96.209.90:/tmp/
ssh -p 22352 jet@101.96.209.90

# 3. 解压
sudo mkdir -p /opt/rsshub
sudo tar xzf /tmp/rsshub-dist.tar.gz -C /opt/rsshub
sudo chown -R yuqing:yuqing /opt/rsshub

# 4. 安装 Playwright Chromium（npmmirror CDN）
cd /opt/rsshub
export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=0
export PLAYWRIGHT_CHROMIUM_DOWNLOAD_HOST=https://registry.npmmirror.com/-/binary/playwright
/usr/local/node/bin/npx playwright install chromium

# ⚠️ 手动安装系统依赖（--with-deps 在 CentOS 9 会卡死）
sudo dnf install -y atk cups-libs gtk3 libXcomposite libXdamage \
  libXrandr pango alsa-lib nss libdrm mesa-libgbm

# 5. 创建日志目录
sudo mkdir -p /opt/rsshub/logs
sudo chown yuqing:yuqing /opt/rsshub/logs

# 6. 创建 systemd unit
sudo tee /etc/systemd/system/yuqing-rsshub.service <<'EOF'
[Unit]
Description=RSSHub for Yuqing Trends
After=network.target

[Service]
Type=simple
User=yuqing
WorkingDirectory=/opt/rsshub
Environment="NODE_ENV=production"
Environment="LISTEN_INADDR_ANY=0"
Environment="PORT=1200"
ExecStart=/usr/local/node/bin/node lib/index.js
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# 7. 启动
sudo systemctl daemon-reload
sudo systemctl enable yuqing-rsshub
sudo systemctl start yuqing-rsshub

# 8. 验证
curl http://127.0.0.1:1200/weibo/hot/search | jq '.item | length'
# 应返回 20
```

### LLM 供应商切换（中转站）

```bash
# 生产已切中转站（智谱余额 1113 弃用）
# engines.env + platform_settings 双配置

# 1. 更新 engines.env
sudo tee /opt/yuqing/config/engines.env <<'EOF'
LLM_BASE_URL=https://sub.geiliapi.com/v1
LLM_API_KEY=sk-49a3abb16f63ac81d7372c0adeca5029996d505cc0aebcb117ee6614b79ae6a4
LLM_MODEL=deepseek-v4.1-flash
BOCHA_API_KEY=sk-d49b...
EOF

# 2. 更新 platform_settings（零重启生效）
sudo -u postgres psql -d yuqing_platform <<'EOF'
INSERT INTO platform_settings (key, value) VALUES
  ('llm_api_key', 'sk-49a3abb16f63ac81d7372c0adeca5029996d505cc0aebcb117ee6614b79ae6a4'),
  ('llm_base_url', 'https://sub.geiliapi.com/v1'),
  ('llm_model', 'deepseek-v4.1-flash')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
EOF

# 3. 重启 insight/report/server
sudo systemctl restart yuqing-insight yuqing-report yuqing-server

# 4. 验证
journalctl -u yuqing-server -n 20 | grep "LLM_MODEL"
# 应显示 deepseek-v4.1-flash
```

### 完整验收清单

```bash
# 1. 检查 8 个服务
systemctl status yuqing-server yuqing-worker \
  yuqing-query yuqing-insight yuqing-report yuqing-forum \
  yuqing-media yuqing-rsshub

# 2. 测试 API
curl https://yuqing2.pangu-cloud.com/api/v1/health

# 3. 测试热榜（三平台）
curl https://yuqing2.pangu-cloud.com/api/v1/trends \
  -H "Authorization: Bearer <admin-token>" | jq '.platforms[] | {name, status, count: (.items | length)}'
# 期望：微博 20 / B站 10 / 知乎 20

# 4. E2E 全链路（需 admin 账号）
# 登录 → 新建分析 → 采集 → 五维研判 → 报告生成
# 实测耗时：357s（采集 1-2min / 五维 3-5min / 报告 1min）

# 5. 检查数据库迁移
sudo -u postgres psql -d yuqing_platform -c "\dt"
# 应包含：users, tenants, analyses, credit_balance, orders 等表

# 6. 检查 nginx 日志
tail -f /usr/local/nginx/logs/access.log
tail -f /usr/local/nginx/logs/error.log
```

### 已知坑位与规避

| 坑位 | 现象 | 规避 |
|------|------|------|
| sudo PATH 无 node | systemd 找不到 node | ExecStart 写全路径 /usr/local/node/bin/node |
| Playwright 浏览器路径 | service 用户 HOME=/opt/yuqing | PLAYWRIGHT_BROWSERS_PATH=/opt/yuqing/.cache |
| RSSHub 日志目录 | winston 写不了 | 启动前 mkdir -p /opt/rsshub/logs |
| ProtectSystem=strict | 预建目录失败 | 启动前手动创建全部目录 |
| pgrep -f 自匹配 | 检测脚本误判 | pgrep 排除自身：pgrep -f "pattern" \| grep -v $$ |

## 🔧 配置文件参考

### platform/config.yaml

```yaml
server:
  port: 8080
  mode: release  # debug / release

store:
  driver: postgres  # memory / postgres

database:
  host: localhost
  port: 5432
  database: yuqing_platform
  user: yuqing
  password: your-secure-password
  sslmode: disable
  max_open_conns: 25
  max_idle_conns: 5

redis:
  addr: localhost:6379
  password: ""
  db: 0

engines:
  query:
    url: http://127.0.0.1:8000
  insight:
    url: http://127.0.0.1:8002
    timeout: 420s  # GLM 思考型需长超时
  report:
    url: http://127.0.0.1:8003
    timeout: 420s

trends:
  rsshub_base: http://127.0.0.1:1200
  refresh_interval: 5m
```

### engines/common/config.py

```python
import os

LLM_API_KEY = os.getenv("LLM_API_KEY", "")
LLM_BASE_URL = os.getenv("LLM_BASE_URL", "https://open.bigmodel.cn/api/paas/v4")
LLM_MODEL = os.getenv("LLM_MODEL", "glm-5.3-flash")
LLM_TIMEOUT = int(os.getenv("LLM_TIMEOUT", "300"))

BOCHA_API_KEY = os.getenv("BOCHA_API_KEY", "")
BOCHA_BASE_URL = "https://api.bochaai.com/v1"
```

## 🔄 迁移顺序（关键）

```bash
# ⚠️ 部署顺序硬约束：先迁移再起服务

# 1. 停止旧服务
sudo systemctl stop yuqing-server yuqing-worker

# 2. 运行迁移
cd /opt/yuqing
./bin/yuqing-cli migrate platform

# 3. 确认迁移版本
sudo -u postgres psql -d yuqing_platform -c "SELECT version FROM goose_db_version ORDER BY id DESC LIMIT 1;"
# 应显示最新版本号（当前 0006）

# 4. 启动新服务
sudo systemctl start yuqing-server yuqing-worker
```

**迁移 0006**（收费体系）包含：
- `credit_balance` 表（租户额度余额）
- `credit_transactions` 表（额度流水）
- `orders` 表（支付订单）

未先迁移直接启动 → 服务崩溃（找不到表）。

## 📊 监控与日志

### systemd 日志

```bash
# 实时查看
journalctl -u yuqing-server -f

# 过去 1 小时
journalctl -u yuqing-server --since "1 hour ago"

# 错误日志
journalctl -u yuqing-server -p err

# 多服务
journalctl -u yuqing-server -u yuqing-insight -f
```

### nginx 日志

```bash
# Ubuntu
tail -f /var/log/nginx/access.log
tail -f /var/log/nginx/error.log

# CentOS (oneinstack)
tail -f /usr/local/nginx/logs/access.log
tail -f /usr/local/nginx/logs/error.log
```

### 应用日志

```bash
# Go 日志（输出到 systemd）
journalctl -u yuqing-server --output cat

# Python 引擎日志
tail -f /opt/yuqing/engines/query_engine/logs/app.log
tail -f /opt/yuqing/engines/insight_engine/logs/app.log
```

### 性能监控

```bash
# 进程资源占用
ps aux | grep yuqing

# 内存使用
free -h

# 磁盘使用
df -h

# 数据库连接数
sudo -u postgres psql -d yuqing_platform -c "SELECT count(*) FROM pg_stat_activity;"

# Redis 连接数
redis-cli INFO clients
```

## 🔐 安全加固

### 1. 防火墙

```bash
# UFW (Ubuntu)
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable

# firewalld (CentOS)
sudo firewall-cmd --permanent --add-service=ssh
sudo firewall-cmd --permanent --add-service=http
sudo firewall-cmd --permanent --add-service=https
sudo firewall-cmd --reload
```

### 2. fail2ban（防暴力破解）

```bash
# 安装
sudo apt install -y fail2ban  # Ubuntu
sudo dnf install -y fail2ban  # CentOS

# 配置
sudo tee /etc/fail2ban/jail.local <<'EOF'
[sshd]
enabled = true
port = 22
logpath = /var/log/auth.log
maxretry = 3
bantime = 3600
EOF

sudo systemctl enable fail2ban
sudo systemctl start fail2ban
```

### 3. PostgreSQL 安全

```bash
# 仅监听本地
# /var/lib/pgsql/15/data/postgresql.conf
listen_addresses = 'localhost'

# 密码强度
# /var/lib/pgsql/15/data/pg_hba.conf
# 改 trust → md5，改 peer → md5

sudo systemctl restart postgresql-15
```

### 4. 敏感文件权限

```bash
sudo chmod 600 /opt/yuqing/config/*.env
sudo chmod 600 /etc/nginx/ssl/*.key
sudo chown yuqing:yuqing /opt/yuqing/config/*.env
```

## 📖 相关文档

- [[快速开始|Quick-Start]] - 本地开发环境
- [[架构设计|Architecture]] - 理解部署架构
- [[配置参考|Configuration]] - 完整配置文件说明
- [[故障排查|Troubleshooting]] - 常见问题解决
