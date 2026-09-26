# 盘古舆情 · 规范化部署手册（Runbook）

> 适用：§1-8 为标准 Ubuntu 24.04 全新部署流程；§9 为 **yuqing2.pangu-cloud.com 生产环境**（CentOS Stream 9 / oneinstack，当前唯一生产）的专章。
> 也可供其他智能体/运维人员照步骤执行。所有历史踩坑已内嵌为「⚠️ 坑位」提示。
> 最后验证：2026-09-22（yuqing2 全链路 E2E 通过）
>
> 🔴 **最高优先级铁律：一切编译/构建只在本地完成后上传**（Go 交叉编译、前端 dist、RSSHub tarball）。服务器只做解压/配置/迁移/启停 —— 构建上服务器 = 内存打满打死 sshd（实测两次）。

---

## 0. 前置要求

| 项 | 要求 |
|---|---|
| 系统 | Ubuntu 24.04（其他版本需自行核对组件版本） |
| 配置 | 最低 2C4G；**禁止在生产服务器上编译 Go/跑 go test**（低配机会被编译 IO 拖垮宕机，实测两次）——Go 产物一律本地交叉编译后上传 |
| 网络 | 服务器需能访问 pypi（用清华镜像）、gitee（acme.sh）；GitHub 可不通（源码走本地打包上传） |
| 域名 | 已解析到服务器 IP（HTTPS 必须；无域名则 HTTP-only） |
| 密钥 | 准备好 `BOCHA_API_KEY`（搜索）与 LLM key（智谱/DeepSeek 等，OpenAI 兼容即可） |

**凭据红线**：所有 key 只允许出现在服务器 `engines.env`（chmod 600）与本地 `credentials.local.md`（gitignored）。绝不进 git。

---

## 1. 系统与数据库准备

```bash
# 1.1 以 root（或全量 sudo 的用户）执行
apt-get update -y
apt-get install -y postgresql postgresql-contrib redis-server nginx socat cron python3-venv python3-pip

# 1.2 系统用户（deploy.sh 也会自动建，但手动准备更可控）
useradd -r -m -d /opt/yuqing -s /usr/sbin/nologin yuqing

# 1.3 PostgreSQL：建角色 + 库（⚠️ 坑位 ①②见注释）
sudo -u postgres psql <<'SQL'
CREATE USER yuqing WITH PASSWORD '<数据库密码>' CREATEDB;
CREATE DATABASE yuqing_platform OWNER yuqing;
GRANT ALL PRIVILEGES ON DATABASE yuqing_platform TO yuqing;
\c yuqing_platform
GRANT ALL ON SCHEMA public TO yuqing;   -- 坑位①：PG15 默认收回 public schema 写权限，
                                         -- 不授权则迁移报 permission denied for schema public
CREATE EXTENSION IF NOT EXISTS citext;  -- 坑位②：users.email 用 CITEXT，缺扩展迁移直接失败
SQL
```

---

## 2. 源码上服务器

```bash
# 2.1 本地打包（在仓库根目录；git archive 输出恒为 LF —— .gitattributes 已强制，
#     ⚠️ 坑位③：若手动 tar 本地目录，Windows 的 CRLF 会让 deploy.sh 报
#     "set: pipefail: invalid option name"，务必用 git archive）
git archive --prefix=pangu-src/ --format=tar.gz -o pangu-src.tar.gz HEAD

# 2.2 上传（⚠️ 坑位④：scp 走 SFTP 以登录用户身份写文件，不经 sudo ——
#     /opt 归 root，直接传 /opt 会 Permission denied。先传 /tmp 再 sudo 挪）
scp pangu-src.tar.gz <user>@<server>:/tmp/

# 2.3 服务器上解压到源码目录 /opt/pangu-source（引擎的 WorkingDirectory/PYTHONPATH 指向这里）
sudo mkdir -p /opt/pangu-src-new && sudo tar xzf /tmp/pangu-src.tar.gz -C /opt/pangu-src-new
sudo rm -rf /opt/pangu-source
sudo mv /opt/pangu-src-new/pangu-src /opt/pangu-source
sudo rm -rf /opt/pangu-src-new /tmp/pangu-src.tar.gz
# ⚠️ 坑位⑤：服务器上该文件若曾以 CRLF 落盘，部署前兜底转换：
sudo sed -i 's/\r$//' /opt/pangu-source/scripts/deploy.sh /opt/pangu-source/scripts/*.conf /opt/pangu-source/scripts/systemd/*.service
```

---

## 3. 执行部署脚本（幂等，可反复跑）

```bash
sudo YUQING_DOMAIN=<你的域名> bash /opt/pangu-source/scripts/deploy.sh
```

脚本自动完成：依赖检测安装（跳过已有）→ Go 交叉编译三二进制 → 前端 npm build → Python venv + Scrapling → 生成 config.yaml（已存在则**绝不覆盖**）→ systemd units → nginx 站点 → 健康检查。

关键产物与路径：

| 产物 | 路径 |
|---|---|
| 二进制 | /opt/yuqing/bin/yuqing-{server,worker,cli} |
| 主配置 | /opt/yuqing/config/config.yaml |
| 引擎密钥 | /opt/yuqing/config/engines.env（chmod 600） |
| 前端 | /opt/yuqing/web/dist |
| nginx 站点 | /etc/nginx/conf.d/yuqing.conf |

---

## 4. 配置生产参数

### 4.1 config.yaml（首次生成后手工核对）

```yaml
store:
  driver: postgres          # ⚠️ 必改：默认 memory，重启丢全部数据
db:
  primary: "postgres://yuqing:<数据库密码>@localhost:5432/yuqing_platform?sslmode=disable"
```

### 4.2 engines.env（引擎密钥，chmod 600）

```bash
BOCHA_API_KEY=<搜索 key>
LLM_API_KEY=<智谱/其他 LLM key>
LLM_BASE_URL=https://open.bigmodel.cn/api/paas/v4   # OpenAI 兼容端点
LLM_MODEL=glm-5.3-flash                              # 思考型模型，引擎侧 max_tokens 已配 4096
```

> LLM 三项也可部署后在「管理后台 → 数据源配置」在线修改（存 PG，零重启生效）；
> engines.env 是引擎侧兜底。后台改 key 优先于环境变量。

### 4.3 数据库迁移（⚠️ 坑位⑥：顺序硬约束 —— 先迁移再起新 server）

**重要**：部署前必须先执行 schema 验证，确保数据库迁移已应用。

```bash
cd /opt/yuqing

# Step 1: 验证 schema 与代码匹配（新增的自动化验证）
bash scripts/pre-deploy-validation.sh
# 如果验证失败，会提示需要执行的迁移

# Step 2: 执行迁移（如果 Step 1 失败）
YUQING_CONFIG=/opt/yuqing/config/config.yaml bin/yuqing-cli migrate platform
# 应输出 applied: 0001..0008（最新迁移）
# - 0005 = analyses.dimensions 列
# - 0008 = analyses.created_by + reports.created_by 列（报告归属）
# 缺它会导致所有 analyses 查询报 "column created_by does not exist"

# Step 3: 再次验证（确保迁移成功）
bash scripts/pre-deploy-validation.sh
# 必须通过后才能继续部署
```

**新增的自动化保护**：
- server 启动时会自动验证 `created_by` 等关键列存在
- 如果 schema 不匹配会立即失败（fail-fast），避免运行时错误
- CI 强制执行 PostgreSQL 测试，不允许跳过

### 4.4 引导管理员（platform_admin 无法从界面造出，必须在此声明）

```bash
mkdir -p /etc/systemd/system/yuqing-server.service.d
printf '[Service]\nEnvironment="YUQING_BOOTSTRAP_ADMIN_EMAIL=<管理员邮箱>"\n' \
  > /etc/systemd/system/yuqing-server.service.d/bootstrap-admin.conf
systemctl daemon-reload
# 该邮箱注册/登录后自动获得 platform_admin 角色
```

### 4.5 启动与自检

```bash
systemctl enable --now yuqing-server yuqing-worker \
  yuqing-query yuqing-media yuqing-insight yuqing-report yuqing-forum
bash /opt/pangu-source/scripts/healthcheck.sh   # 全 [OK] 即部署成功
```

---

## 5. HTTPS（acme.sh）

```bash
# 签发（webroot 模式，挑战文件落前端目录）
~/.acme.sh/acme.sh --issue -d <域名> --webroot /opt/yuqing/web/dist --server letsencrypt
# 安装 + 续期钩子（续期成功自动 reload nginx）
sudo mkdir -p /etc/nginx/ssl/<域名>
~/.acme.sh/acme.sh --install-cert -d <域名> --ecc \
  --key-file /etc/nginx/ssl/<域名>/key.pem \
  --fullchain-file /etc/nginx/ssl/<域名>/fullchain.pem \
  --reloadcmd "systemctl reload nginx"
# 90 天周期续期 cron（每季度首日 04:00 强跑；acme.sh 内置每日检查为双保险）
echo '0 4 1 */3 * /root/.acme.sh/acme.sh --cron --home /root/.acme.sh > /dev/null 2>&1' | sudo crontab -
# 切 HTTPS 站点模板（证书就绪后 deploy.sh 重跑会自动切；手动方式：）
sudo sed -e "s|__DOMAIN__|<域名>|g" -e "s|__CERT_DIR__|/etc/nginx/ssl/<域名>|g" \
  /opt/pangu-source/scripts/nginx-ssl.conf > /etc/nginx/conf.d/yuqing.conf
sudo nginx -t && sudo systemctl reload nginx
```

---

## 6. 部署验收清单（逐项打勾）

```bash
# □ 7 服务全 active
systemctl is-active yuqing-{server,worker,query,media,insight,report,forum}
# □ API 健康
curl -s http://127.0.0.1:8080/api/v1/health
# □ 引擎健康（llm:true = key 已注入；dimensions:5 = 五维分析就绪）
curl -s http://127.0.0.1:8002/health
# □ 站点 200 且 HTTP→HTTPS 301
curl -s -o /dev/null -w '%{http_code}' https://<域名>/
# □ 账号持久化（重启 server 后再登录成功 = PG 生效）
curl -s -X POST http://127.0.0.1:8080/api/v1/auth/register -H 'Content-Type: application/json' \
  -d '{"email":"check@test.com","password":"Test12345!","name":"验收"}' > /dev/null
systemctl restart yuqing-server && sleep 3
curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"check@test.com","password":"Test12345!"}' | grep -q access_token && echo PERSIST-OK
# □ 全链路（界面创建分析 → 等 completed → 详情页五个 Tab 有真实内容；
#   五维研判 Tab 有维度结论、报告含「五维研判」章节）
```

---

## 7. 高频故障速查

| 症状 | 根因 | 处置 |
|---|---|---|
| deploy.sh 报 `pipefail: invalid option name` | 脚本被 CRLF 污染（坑位③） | `sed -i 's/\r$//' scripts/deploy.sh` |
| 迁移报 `permission denied for schema public` | 坑位① | `GRANT ALL ON SCHEMA public TO yuqing` |
| 迁移报 `column "email" ... CITEXT` | 坑位② 缺扩展 | `CREATE EXTENSION citext` |
| scp 到 /opt 被拒 | 坑位④ SFTP 不经 sudo | 传 /tmp 再 sudo 挪 |
| 任务永久 queued | engines.query.url 为空 / query 引擎未 active | 核对 config.yaml + `systemctl status yuqing-query` |
| 任务 completed 但 analyses 查询报 column does not exist | 坑位⑥ 迁移晚于新 server | 补跑 `yuqing-cli migrate platform` 再重启 |
| 分析失败：LLM 402/超时 | 供应商余额/超时 | 引擎超时已配 420s；查余额；后台可换供应商 |
| 后台「数据源配置」显示未配置 | server unit 缺 EnvironmentFile | unit 内加 `EnvironmentFile=-/opt/yuqing/config/engines.env` 后 daemon-reload |
| 服务器高负载失联 | 在生产机跑了编译/全量测试 | **永久禁止**；测试二进制本地 `go test -c` 交叉编译后上传执行 |
| 登录后无 platform_admin | bootstrap 邮箱不匹配 | 核对 drop-in 与注册邮箱一致 |

---

## 8. 升级已有部署

```bash
# 本地：打新包 → scp /tmp → 服务器：
sudo bash -c 'cd /opt/pangu-src-new && tar xzf /tmp/pangu-src.tar.gz && cd pangu-src && \
  for d in engines platform web scripts; do rm -rf /opt/pangu-source/$d && cp -r $d /opt/pangu-source/; done'
# 有新迁移：先迁移
sudo bash -c 'cd /opt/yuqing && YUQING_CONFIG=/opt/yuqing/config/config.yaml bin/yuqing-cli migrate platform'
# 删旧二进制（deploy.sh 检测到存在会跳过编译）再跑部署
sudo bash -c 'rm -f /opt/yuqing/bin/yuqing-* && YUQING_DOMAIN=<域名> bash /opt/pangu-source/scripts/deploy.sh'
# 自检
bash /opt/pangu-source/scripts/healthcheck.sh
```

> 详细运维（日志/备份/巡检/故障排查）见 `docs/ops/OPS_MANUAL.html`。

---

## 9. yuqing2 生产环境专章（CentOS Stream 9 / oneinstack，当前唯一生产）

> 环境：101.96.209.90:22352（jet，sudo），域名 yuqing2.pangu-cloud.com，4C/3.6Gi/40G。
> 与 §1-8 的 Ubuntu 流程**不通用**：dnf 而非 apt、PG15 需 PGDG 源、nginx 为 oneinstack 源码版（/usr/local/nginx，vhost include 机制）、Node 为 oneinstack 版（/usr/local/node/bin，**systemd 必须写全路径**）、Python 需另装 3.11。
> 老机 47.120.20.10 的部署配置已废弃（2026-09-22 用户确认）。

### 9.1 差异速查

| 项 | Ubuntu §1-8 | yuqing2 (CentOS 9) |
|---|---|---|
| 包管理 | apt | dnf（PG15 用 PGDG 源 `--nobest`，兼容系统 OpenSSL 1.1.1） |
| Python | 3.11 系统自带 | `dnf install python3.11 python3.11-pip`（系统 3.9 不够用） |
| nginx | apt 版，conf 在 /etc/nginx | 源码版 /usr/local/nginx，站点 conf 在 `conf/vhost/*.conf` |
| Node | 无要求 | oneinstack 版 /usr/local/node/bin/node（v22） |
| MySQL | 无 | **用户自有业务用，勿动**；平台用新装 PG15 |
| swap | — | 必须（`dd` 2G swapfile + fstab），无 swap 的 dnf/构建会 OOM |

### 9.2 部署步骤（全流程无编译，材料本地备好）

```bash
# ① 本地准备（Windows 开发机）：
#    Go: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/yuqing-{server,worker,cli} ./cmd/{server,worker,cli}
#    前端: cd web && npm run build && tar czf dist.tar.gz dist
#    RSSHub: 本地 clone + npm install --legacy-peer-deps + npm run build
#            → tar czf rsshub-dist.tar.gz dist node_modules package.json
#    源码: git archive --prefix=pangu-src/ --format=tar.gz -o src.tar.gz HEAD
# ② SFTP 全部上传至 /tmp（paramiko，见运维记忆；sshpass 在 Windows 不可用）
```

```bash
# ③ 服务器：swap + PG15 + Python3.11 + 用户（见会话脚本 deploy_new_p1*.py 要点）
sudo dd if=/dev/zero of=/swapfile bs=1M count=2048 && sudo chmod 600 /swapfile \
  && sudo mkswap /swapfile && sudo swapon /swapfile
sudo dnf install -y https://download.postgresql.org/pub/repos/yum/reporpms/EL-9-x86_64/pgdg-redhat-repo-latest.noarch.rpm
sudo dnf -qy module disable postgresql
sudo dnf install -y --nobest postgresql15-server postgresql15-contrib   # --nobest 兼容 OpenSSL 1.1.1
sudo /usr/pgsql-15/bin/postgresql-15-setup initdb
sudo systemctl enable --now postgresql-15
# pg_hba：host 127.0.0.1/32 改 scram-sha-256，restart
sudo dnf install -y python3.11 python3.11-pip
sudo useradd -r -m -d /opt/yuqing -s /sbin/nologin yuqing

# ④ PG 库/角色/citext（密码示例 Yuq2Pg_2026!，生产请自定）
sudo -u postgres psql -c "CREATE USER yuqing WITH PASSWORD '***' CREATEDB;"
sudo -u postgres createdb -O yuqing yuqing_platform
sudo -u postgres psql -d yuqing_platform -c "GRANT ALL ON SCHEMA public TO yuqing; CREATE EXTENSION citext;"

# ⑤ 源码/二进制/dist 解压就位（/opt/pangu-source 与 /opt/yuqing/bin、web/dist）
# ⑥ config.yaml 手写（driver: postgres + engines 全 127.0.0.1 + insight/report timeout 420s
#    + query timeout 180s + rsshub_base: "http://127.0.0.1:1200"），chmod 600
# ⑦ engines.env（chmod 600）：BOCHA_API_KEY / LLM_API_KEY / LLM_BASE_URL / LLM_MODEL
# ⑧ 迁移：cd /opt/yuqing && YUQING_CONFIG=... bin/yuqing-cli migrate platform   # 0001-0006
# ⑨ bootstrap drop-in + 7 个 systemd unit（源码 scripts/systemd/）→ enable --now
#    ⚠️ 全部引擎 unit 模板自带 EnvironmentFile=-/opt/yuqing/config/engines.env，勿删
```

### 9.3 RSSHub 部署（F21 热榜数据适配层）

```bash
sudo mkdir -p /opt/rsshub/logs            # ⚠️ 坑：winston 启动时要写 logs/，缺了 crash-loop
sudo tar xzf /tmp/rsshub-dist.tar.gz -C /opt/rsshub   # 本地构建的 dist+node_modules+package.json
sudo chown -R yuqing:yuqing /opt/rsshub
# unit: scripts/systemd/yuqing-rsshub.service（要点：
#   ExecStart=<node全路径> /opt/rsshub/dist/index.mjs    # ⚠️ 产物是 .mjs 非 .js
#   Environment=PORT=1200
#   Environment=LISTEN_INADDR_ANY=0                      # ⚠️ 不设则绑 0.0.0.0 公网暴露
#   Environment=NODE_OPTIONS=--max-http-header-size=32768
#   MemoryHigh=700M MemoryMax=900M                       # OOM 时死 rsshub 不死 PG
#   ProtectSystem=strict + 预建 logs 目录）
sudo systemctl enable --now yuqing-rsshub
ss -tlnp | grep 1200        # ⚠️ 必须显示 127.0.0.1:1200，出现 *:1200 = 公网暴露
```

**Playwright Chromium**（部分热榜路由需浏览器渲染）：
```bash
# ⚠️ 坑：--with-deps 在 CentOS 9 卡死 —— 系统依赖手动 dnf（nss/atk/cups-libs/mesa-libgbm/alsa-lib 等）
# ⚠️ 下载用 npmmirror CDN；⚠️ 浏览器默认装 $HOME/.cache（service 用户 home=/opt/yuqing）——
#    就让它装在 /opt/yuqing/.cache/ms-playwright，不要改 PLAYWRIGHT_BROWSERS_PATH（改了反而不生效）
sudo env PLAYWRIGHT_DOWNLOAD_HOST=https://cdn.npmmirror.com/binaries/playwright \
  PATH=/usr/local/node/bin:$PATH /usr/local/node/bin/npx playwright install chromium-headless-shell
sudo chown -R yuqing:yuqing /opt/yuqing/.cache
sudo systemctl restart yuqing-rsshub
```

### 9.4 nginx 站点（oneinstack 风格 + 自签 SSL）

```bash
sudo openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
  -keyout /usr/local/nginx/conf/ssl/yuqing2.pangu-cloud.com.key \
  -out /usr/local/nginx/conf/ssl/yuqing2.pangu-cloud.com.crt -subj "/CN=yuqing2.pangu-cloud.com"
# vhost conf：/usr/local/nginx/conf/vhost/yuqing2.pangu-cloud.com.conf
#   root /opt/yuqing/web/dist; location /api/ 反代 127.0.0.1:8080（proxy_buffering off 供 SSE）;
#   location / try_files $uri /index.html（SPA）。写完 nginx -t && nginx -s reload
sudo chmod 755 /opt/yuqing /opt/yuqing/web /opt/yuqing/web/dist   # ⚠️ 坑：home 目录 700，nginx(www) 穿越不了 → 500
```

### 9.5 LLM 中转站配置（当前：sub.geiliapi.com/v1 + deepseek-v4.1-flash）

- engines.env 三键：`LLM_BASE_URL=https://sub.geiliapi.com/v1`（**带 /v1**）、`LLM_API_KEY`、`LLM_MODEL=deepseek-v4.1-flash`
- 同时 UPDATE `platform_settings` 表同名键（⚠️ 坑：种子逻辑「键存在不覆盖」—— 首启后改 env 无效，必须直接 UPDATE 表或删键重启）
- 中转站要点：该站唯一模型 deepseek-v4.1-flash（思考型，reasoning 吃 max_tokens）；间歇 503 过载需重试；Go crawler HTTP 超时已 180s（internal/engine/real.go，勿回 60s）
- quick 模式思考档：`thinking: {"type": "low"}`（智谱/DeepSeek 思考模型不支持 disabled，官方错误 1210）

### 9.6 yuqing2 验收清单

```
□ systemctl is-active 8 服务（7 引擎/平台 + yuqing-rsshub）
□ curl 127.0.0.1:8080/api/v1/health → ok
□ curl 127.0.0.1:8002/health → dims:5 quick_dimensions:3 llm:true
□ curl 127.0.0.1:1200/weibo/search/hot?format=json → 200 且 ≥20 条
□ ss -tlnp | grep 1200 → 仅 127.0.0.1（公网暴露 = 事故）
□ 登录 → GET /trends → 三平台 items 非空
□ 创建真实分析 → completed，result 五维+摘要+报告齐备，warning 为空
□ 外网 https://yuqing2.pangu-cloud.com/ 200（自签证书浏览器需手动信任）
```

### 9.7 v0.1.1-beta 用户中心部署记录（2026-09-22）

**部署内容**：

- 迁移 0007（`platform/migrations/platform/0007_user_center_p0.sql`）：users 新列（`phone` / `trial_analysis_used` / `email_verified_at` / `avatar_url` / `timezone` / `password_changed_at`）+ 三张新表（`verification_tokens` / `sms_verification_codes` / `login_sessions`）+ platform_settings 邮件/短信配置键 seed（`email_*` / `smtp_*` / `resend_api_key` / `sms_*`）
- 三二进制（server / worker / cli，本地交叉编译上传）+ 前端 dist
- 新增 systemd drop-in `/etc/systemd/system/yuqing-server.service.d/public-base-url.conf`：注入 `YUQING_PUBLIC_BASE_URL=https://yuqing2.pangu-cloud.com` —— 邮箱验证链接的基地址改由环境变量注入，不再取请求 Host 头（防 Host 头伪造投毒验证邮件链接）

**部署顺序**（实测有效，后续升级照此）：

```
停服(server+worker) → 换二进制/dist → 事务试跑迁移（sed 提取 Up 段，BEGIN; \i up.sql; ROLLBACK; 验语法不落库）
  → 正式迁移 → 注入 drop-in + daemon-reload → 启服 → 验收
```

**⚠️ 本次三个踩坑（后续升级必读）**：

| # | 坑 | 说明 |
|---|---|---|
| ① | yuqing-cli 用 embed FS 打包迁移 SQL | 迁移 SQL 在**编译期**嵌入二进制（go:embed）——改服务器磁盘上的迁移文件**无效**，必须本地重编译 CLI 再上传。磁盘上 `/opt/yuqing/migrations/` 那份只是留档 |
| ② | PL/pgSQL `$$` 块必须加 StatementBegin/End 标记 | goose 默认按分号切割语句，`DO $$ ... $$` / 函数体内的分号会被腰斩，报 `42601 syntax error`。**psql 直接执行能过但 goose 失败**——两种执行器对「语句边界」的语义不同。`$$` 块前后必须加 `-- +goose StatementBegin` / `-- +goose StatementEnd` |
| ③ | yuqing-cli 必须带 YUQING_CONFIG | unit 的 WorkingDirectory=/opt/yuqing 但 config.yaml 在 `config/` 子目录，裸跑 `bin/yuqing-cli` 找不到配置。统一写法：`cd /opt/yuqing && YUQING_CONFIG=/opt/yuqing/config/config.yaml bin/yuqing-cli migrate platform` |

**部署后复核（2026-09-22 全通过）**：8 服务全 active；goose_db_version=7（0001-0007 全 applied）；users 六新列 + 三新表在位；/api/v1/health 200；`PUT /auth/password`、`GET /user/profile` 未带 token → 401；`GET /auth/verify-email` 缺 token → 400（非 404）；server 近 1 小时日志零 error/panic。二进制 md5（与本地构建产物一致）：server `fc710f0b7b4a3c2ab6ce891097322b46`、worker `32780f6387227f7875b9422196b45183`、cli `e3cc2a846fe4701504f4f3cbed4da12a`。
