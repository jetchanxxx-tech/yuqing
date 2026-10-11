# 盘古舆情 · 规范化部署手册（Runbook）

> 适用：§1-8 为标准 Ubuntu 24.04 全新部署流程；§9 为 **yuqing2.pangu-cloud.com 生产环境**（CentOS Stream 9 / oneinstack，当前唯一生产）的专章。
> 也可供其他智能体/运维人员照步骤执行。所有历史踩坑已内嵌为「⚠️ 坑位」提示。
> 历史现场验证：2026-09-22（yuqing2 全链路 E2E 通过）；构建政策更新：2026-10-09。GitHub 工作流是否运行成功须查看对应提交的 Actions 结果。
>
> 🔴 **最高优先级铁律：所有编译、构建和编译型测试只在 GitHub 托管 runner 完成。** 本地电脑、开发主机和任何自有服务器不得执行 `go build`、`go test`（包括 `-race` / `-c`）、`go vet`、`go run`、`npm run build`、`npm run dev`（会转换并预构建依赖）、`tsc`、Vite 打包，或触发这些命令的 Makefile/脚本。RSSHub 等附属服务也适用；不得使用 self-hosted runner 构建。本地仅编辑、轻量语法检查、下载校验和上传产物；服务器仅验哈希、解包、配置、迁移、启停与健康检查。本规则覆盖下文历史记录中的「本地构建」做法。

---

## 0. 前置要求

| 项 | 要求 |
|---|---|
| 系统 | Ubuntu 24.04（其他版本需自行核对组件版本） |
| 配置 | 最低 2C4G；本地与服务器均禁止编译，Go/前端产物一律由 GitHub 托管 runner 提供 |
| 网络 | 服务器需能访问运行时依赖镜像；GitHub 可不通，由本地下载 GitHub artifact、校验后上传 |
| 域名 | 已解析到服务器 IP（HTTPS 必须；无域名则 HTTP-only） |
| 密钥 | 准备好 `BOCHA_API_KEY`（搜索）与 LLM key（智谱/DeepSeek 等，OpenAI 兼容即可） |

**凭据红线**：所有 key 只允许出现在服务器 `engines.env`（chmod 600）与本地 `credentials.local.md`（gitignored）。绝不进 git。

### 0.1 GitHub 构建与分支门禁

- `dev` 是日常开发分支，已设为 GitHub 默认分支；`prod` 记录已验证的线上版本，初始基线为 `6d6af7e85b4d154f76d57792d85030dec22c9ccb`。现有 `main`、`beta/*` 保留。
- `dev → prod` 通过 PR 评审并确认 CI 成功后合并发布。`prod` 已配置 PR 合并保护，并要求 `Go tests and binaries`、`Frontend build`、`Release archive` 三项检查，最后一项依赖 PG 检查成功。默认分支和分支保护已通过 GitHub API 核验；每次发布仍需核对实际状态和对应 SHA 的检查结果。
- `.github/workflows/build-release.yml` 在 push 到 `dev` / `prod` / `main` / `beta/**`、PR 和手动 `workflow_dispatch` 时构建；只使用 GitHub 托管 runner。Go 执行全量 `-race` 测试、vet、三个 linux/amd64 二进制构建；Node 22 执行前端 lint/build。
- `build-release.yml` 的 `postgresql` 作业通过 `workflow_call` 调用 `.github/workflows/postgresql-tests.yml` 执行真实 PG 测试；`package` 必须等待 Go、前端和 PostgreSQL 三类检查全部成功后才能生成最终部署包。`dev` / `prod` / `beta/**` 的 PG 检查由此调用；PG 工作流保留 `main` / `develop` 独立触发，并支持手动触发。部署前同时核对目标提交的构建与被调用 PG 作业结果。
- `actions/upload-artifact` 保留带提交 SHA 的产物 30 天。部署包 `yuqing-linux-amd64-${SHA}.tar.gz` 包含源码、`platform/bin`、`web/dist`、`SHA256SUMS`，并附外部 `yuqing-linux-amd64-${SHA}.tar.gz.sha256`。到期缺包时，在 GitHub 重跑目标提交工作流，不能改成本地编译。
- 开发验证选择 `dev` 对应 SHA；正式部署选择合并后 `prod` 目标 SHA 的成功构建包。PR 临时 merge SHA 的包仅用于验证，不作为正式部署包。下载时记录工作流 run ID、完整 SHA 与哈希，确保源码、二进制和前端来自同一次构建。

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

## 2. 下载 GitHub 构建包并上传

先在 GitHub Actions 核对目标 SHA 的 `build-release` 与 PostgreSQL 测试均成功，再下载该 run 的 artifact（可在网页下载，或使用 `gh run download <run-id> --name <目标SHA的artifact名称> --dir /tmp/yuqing-artifact`）。本地下载及上传过程不执行任何构建命令。

```bash
# 2.1 本地：使用记录的完整提交 SHA；在 artifact 解压后的目录执行
RELEASE_SHA=<目标完整提交SHA>
cd /tmp/yuqing-artifact
sha256sum -c "yuqing-linux-amd64-${RELEASE_SHA}.tar.gz.sha256" || exit 1
# 标准 Ubuntu 上传示例（yuqing2 的 root/端口见 §9）
scp "yuqing-linux-amd64-${RELEASE_SHA}.tar.gz" \
    "yuqing-linux-amd64-${RELEASE_SHA}.tar.gz.sha256" <user>@<server>:/tmp/

# 2.2 服务器：再次校验归档，再解包；校验失败立即停止
RELEASE_SHA=<同一个完整提交SHA>
cd /tmp
sha256sum -c "yuqing-linux-amd64-${RELEASE_SHA}.tar.gz.sha256" || exit 1
sudo mkdir -p "/opt/yuqing-releases/${RELEASE_SHA}"
sudo tar xzf "yuqing-linux-amd64-${RELEASE_SHA}.tar.gz" -C "/opt/yuqing-releases/${RELEASE_SHA}"
cd "/opt/yuqing-releases/${RELEASE_SHA}"
sha256sum -c SHA256SUMS || exit 1
# 全新部署：源码与预构建产物一并就位
sudo mkdir -p /opt/pangu-source
sudo cp -a . /opt/pangu-source/
```

归档根目录就是源码根目录；`platform/bin` 与 `web/dist` 已由 GitHub 构建好。已有生产部署先按 §8 备份，不直接覆盖。⚠️ CRLF 踩坑：GitHub 从提交构建并按 `.gitattributes` 保持 LF，禁止另用本地未提交目录替换构建包中的脚本。

---

## 3. 执行部署脚本（幂等，可反复跑）

```bash
sudo YUQING_DOMAIN=<你的域名> bash /opt/pangu-source/scripts/deploy.sh
```

脚本先确认并校验 GitHub 构建包中的三个二进制与前端产物，再安装这些产物、准备运行时环境、生成 config.yaml（已存在则**绝不覆盖**）、配置 systemd/nginx 并健康检查。缺少产物时必须终止；不得安装构建工具、调用 Go/npm/TypeScript 构建，或回退为现场编译。Python 运行时依赖只能安装预构建 wheel，缺包时回到 GitHub 处理，禁止在服务器从源码编译依赖。

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
- 发布前核对目标提交的 GitHub PostgreSQL 测试已执行且成功，不接受跳过；分支保护是否强制此门禁须核对实际设置

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
| 本地或服务器高负载失联 | 执行了编译/全量测试 | **永久禁止**；所有 Go 测试与构建只在 GitHub 托管 runner 执行，下载成功产物再部署 |
| 登录后无 platform_admin | bootstrap 邮箱不匹配 | 核对 drop-in 与注册邮箱一致 |

---

## 8. 升级已有部署

按 §0.1 核对 `prod` 目标 SHA 的 CI，按 §2 下载、上传并校验包。服务器部署前备份数据库、`/opt/pangu-source`、三个二进制、前端目录和配置；保留旧版产物供回滚。以下为标准 Ubuntu 的安装顺序，yuqing2 使用 §9 的配置路径与 nginx。

```bash
# 服务器：已完成 §2 校验，且已备份；有写入的服务先停服
RELEASE_SHA=<已验证的prod完整提交SHA>
sudo systemctl stop yuqing-server yuqing-worker
cd "/opt/yuqing-releases/${RELEASE_SHA}"
sha256sum -c SHA256SUMS || exit 1
# 移走旧源码目录，避免旧文件残留；保留这份快照以供回滚
sudo mv /opt/pangu-source "/opt/pangu-source.backup-$(date +%Y%m%d%H%M%S)"
sudo mkdir -p /opt/pangu-source
sudo cp -a . /opt/pangu-source/
sudo install -m 0755 platform/bin/yuqing-cli /opt/yuqing/bin/yuqing-cli
# 新 CLI 内嵌同一 SHA 的迁移，先迁移再启动新 server
sudo env YUQING_CONFIG=/opt/yuqing/config/config.yaml /opt/yuqing/bin/yuqing-cli migrate platform || exit 1
# deploy.sh 只安装预构建产物，不编译；迁移失败时保持停服并按备份恢复
sudo env YUQING_DOMAIN=<域名> bash /opt/pangu-source/scripts/deploy.sh || exit 1
# 自检
bash /opt/pangu-source/scripts/healthcheck.sh
```

回滚使用已备份并验证过的二进制/前端/源码；不得 checkout 旧代码后现场构建。涉及不可逆迁移时按数据库备份恢复方案执行。

> 详细运维（日志/备份/巡检/故障排查）见 `docs/ops/OPS_MANUAL.html`。

---

## 9. yuqing2 生产环境专章（CentOS Stream 9 / oneinstack，当前唯一生产）

> 环境：101.96.209.90:22352（root），域名 yuqing2.pangu-cloud.com，4C/3.6Gi/40G。
> 与 §1-8 的 Ubuntu 流程**不通用**：dnf 而非 apt、PG15 需 PGDG 源、nginx 为 oneinstack 源码版（/usr/local/nginx，vhost include 机制）、Node 为 oneinstack 版（/usr/local/node/bin，**systemd 必须写全路径**）、Python 需另装 3.11。
> 老机 47.120.20.10 的部署配置已废弃（2026-09-22 用户确认）。

SSH 登录：`ssh -p 22352 root@101.96.209.90`。本节服务器命令以 root 执行；密码通过交互提示或本地凭据输入，自动化从环境变量读取，不写入文档或部署脚本。

### 9.1 差异速查

| 项 | Ubuntu §1-8 | yuqing2 (CentOS 9) |
|---|---|---|
| 包管理 | apt | dnf（PG15 用 PGDG 源 `--nobest`，兼容系统 OpenSSL 1.1.1） |
| Python | 3.11 系统自带 | `dnf install python3.11 python3.11-pip`（系统 3.9 不够用） |
| nginx | apt 版，conf 在 /etc/nginx | 源码版 /usr/local/nginx，站点 conf 在 `conf/vhost/*.conf` |
| Node | 无要求 | oneinstack 版 /usr/local/node/bin/node（v22） |
| MySQL | 无 | **用户自有业务用，勿动**；平台用新装 PG15 |
| swap | — | 必须（`dd` 2G swapfile + fstab），用于运行时和系统维护的内存余量，不允许据此在服务器构建 |

### 9.2 部署步骤（GitHub 构建，服务器只安装产物）

```bash
# ① 在 GitHub 核对 prod 目标 SHA 的构建与 PG 测试成功，下载 artifact
# ② 本地只做 sha256 校验和上传；服务器按 §2 再验归档与 SHA256SUMS
RELEASE_SHA=<已验证的prod完整提交SHA>
scp -P 22352 "yuqing-linux-amd64-${RELEASE_SHA}.tar.gz" \
    "yuqing-linux-amd64-${RELEASE_SHA}.tar.gz.sha256" root@101.96.209.90:/tmp/
# RSSHub 若需更新，另从 GitHub 托管 runner 获得已构建的运行时包并校验；
# build-release.yml 当前项目包不包含 RSSHub，不得用本地或服务器构建补齐。
```

```bash
# ③ 服务器：swap + PG15 + Python3.11 + 用户（见会话脚本 deploy_new_p1*.py 要点）
dd if=/dev/zero of=/swapfile bs=1M count=2048 && chmod 600 /swapfile \
  && mkswap /swapfile && swapon /swapfile
dnf install -y https://download.postgresql.org/pub/repos/yum/reporpms/EL-9-x86_64/pgdg-redhat-repo-latest.noarch.rpm
dnf -qy module disable postgresql
dnf install -y --nobest postgresql15-server postgresql15-contrib   # --nobest 兼容 OpenSSL 1.1.1
/usr/pgsql-15/bin/postgresql-15-setup initdb
systemctl enable --now postgresql-15
# pg_hba：host 127.0.0.1/32 改 scram-sha-256，restart
dnf install -y python3.11 python3.11-pip
useradd -r -m -d /opt/yuqing -s /sbin/nologin yuqing

# ④ PG 库/角色/citext（密码在现场安全配置，不写入文档/仓库）
runuser -u postgres -- psql -c "CREATE USER yuqing WITH PASSWORD '***' CREATEDB;"
runuser -u postgres -- createdb -O yuqing yuqing_platform
runuser -u postgres -- psql -d yuqing_platform -c "GRANT ALL ON SCHEMA public TO yuqing; CREATE EXTENSION citext;"

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
mkdir -p /opt/rsshub/logs            # ⚠️ 坑：winston 启动时要写 logs/，缺了 crash-loop
tar xzf /tmp/rsshub-dist.tar.gz -C /opt/rsshub   # 已校验的 GitHub 构建包：dist+node_modules+package.json
chown -R yuqing:yuqing /opt/rsshub
# unit: scripts/systemd/yuqing-rsshub.service（要点：
#   ExecStart=<node全路径> /opt/rsshub/dist/index.mjs    # ⚠️ 产物是 .mjs 非 .js
#   Environment=PORT=1200
#   Environment=LISTEN_INADDR_ANY=0                      # ⚠️ 不设则绑 0.0.0.0 公网暴露
#   Environment=NODE_OPTIONS=--max-http-header-size=32768
#   MemoryHigh=700M MemoryMax=900M                       # OOM 时死 rsshub 不死 PG
#   ProtectSystem=strict + 预建 logs 目录）
systemctl enable --now yuqing-rsshub
ss -tlnp | grep 1200        # ⚠️ 必须显示 127.0.0.1:1200，出现 *:1200 = 公网暴露
```

**Playwright Chromium**（部分热榜路由需浏览器渲染）：
```bash
# ⚠️ 坑：--with-deps 在 CentOS 9 卡死 —— 系统依赖手动 dnf（nss/atk/cups-libs/mesa-libgbm/alsa-lib 等）
# ⚠️ 下载用 npmmirror CDN；⚠️ 浏览器默认装 $HOME/.cache（service 用户 home=/opt/yuqing）——
#    就让它装在 /opt/yuqing/.cache/ms-playwright，不要改 PLAYWRIGHT_BROWSERS_PATH（改了反而不生效）
env PLAYWRIGHT_DOWNLOAD_HOST=https://cdn.npmmirror.com/binaries/playwright \
  PATH=/usr/local/node/bin:$PATH /usr/local/node/bin/npx playwright install chromium-headless-shell
chown -R yuqing:yuqing /opt/yuqing/.cache
systemctl restart yuqing-rsshub
```

### 9.4 nginx 站点（oneinstack 风格 + 自签 SSL）

```bash
openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
  -keyout /usr/local/nginx/conf/ssl/yuqing2.pangu-cloud.com.key \
  -out /usr/local/nginx/conf/ssl/yuqing2.pangu-cloud.com.crt -subj "/CN=yuqing2.pangu-cloud.com"
# vhost conf：/usr/local/nginx/conf/vhost/yuqing2.pangu-cloud.com.conf
#   root /opt/yuqing/web/dist; location /api/ 反代 127.0.0.1:8080（proxy_buffering off 供 SSE）;
#   location / try_files $uri /index.html（SPA）。写完 nginx -t && nginx -s reload
chmod 755 /opt/yuqing /opt/yuqing/web /opt/yuqing/web/dist   # ⚠️ 坑：home 目录 700，nginx(www) 穿越不了 → 500
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

### 9.7 历史：v0.1.1-beta 用户中心部署记录（2026-09-22）

> 以下保留当时执行事实；其中本地构建做法已由 §0 的 GitHub 构建规则替代，不得照做。今后 CLI 重编译一律在 GitHub 托管 runner 执行。

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
| ① | yuqing-cli 用 embed FS 打包迁移 SQL | 迁移 SQL 在**编译期**嵌入二进制（go:embed）——改服务器磁盘上的迁移文件**无效**。当时用本地重编译上传，今后必须下载 GitHub 重新构建的 CLI。磁盘上 `/opt/yuqing/migrations/` 那份只是留档 |
| ② | PL/pgSQL `$$` 块必须加 StatementBegin/End 标记 | goose 默认按分号切割语句，`DO $$ ... $$` / 函数体内的分号会被腰斩，报 `42601 syntax error`。**psql 直接执行能过但 goose 失败**——两种执行器对「语句边界」的语义不同。`$$` 块前后必须加 `-- +goose StatementBegin` / `-- +goose StatementEnd` |
| ③ | yuqing-cli 必须带 YUQING_CONFIG | unit 的 WorkingDirectory=/opt/yuqing 但 config.yaml 在 `config/` 子目录，裸跑 `bin/yuqing-cli` 找不到配置。统一写法：`cd /opt/yuqing && YUQING_CONFIG=/opt/yuqing/config/config.yaml bin/yuqing-cli migrate platform` |

### 9.8 前端静态修复发布

在 GitHub Actions 构建 `prod` 目标提交，确认构建与 PG 测试成功后下载部署包；本地和服务器校验归档以及解包后的 `SHA256SUMS`。从包中取 `web/dist`，先备份线上 `/opt/yuqing/web/dist`，再将新的 hash 资源上传到该目录并保留旧 hash 资源。最后将新 `index.html` 上传到同目录临时文件，通过 `mv` 原子替换 `index.html`，避免页面引用尚未上传的资源。发布后访问页面确认修复与资源加载；回滚时恢复备份中的 `index.html`。整个流程不在本地或服务器执行前端编译。

## 10. 历史：Beta 0.2.2 的 0008 热修结构收敛

> 以下为 2026-09-28 的部署记录和数据快照；历史「本地构建」已取消，新的测试与构建必须由 GitHub 托管 runner 完成。

2026-09-28 对 `yuqing_platform` 的只读盘点：goose 最新为 7，`analyses.created_by` 有 9 条空字符串（17 条总量），`reports` 无记录，`reports.report_version` 为 `TEXT DEFAULT 'v1'`；两个创建人列均无外键和索引。这些数量仅是盘点时快照，迁移前必须重新查询。

- 不得对该库运行旧版 `0008_report_center.sql` 或手工写入 `goose_db_version=8`。Beta 0.2.2 中的 `0008` 同时覆盖干净 v7 和已经补列的 v7，先在 GitHub PostgreSQL 测试库分别运行 `TestReportCenterMigration`，再用同一 SHA 的 GitHub 构建 CLI 正式迁移。
- 先做 PostgreSQL 可恢复备份并核验备份文件；先核对空字符串所属租户存在成员、现有非空创建人属于对应租户、`reports.report_version` 可以转成整数。测试环境已验证两种路径，不能代替生产前的实时检查。
- 停止写入分析/报告后执行 `YUQING_CONFIG=/opt/yuqing/config/config.yaml /opt/yuqing/bin/yuqing-cli migrate platform`；查询 goose 版本 8、列类型、外键、索引及剩余空创建人，再恢复服务与流量。CLI 通过 `go:embed` 固化 SQL，上传磁盘上的 SQL 文件不会改变 CLI 执行内容。
- `0008` 的 Down 会删除热修前已有的创建人列，不能用 `goose down` 回滚；失败时保持服务停止，并按迁移前数据库备份恢复。所有 Go、前端构建均由 GitHub 托管 runner 完成，服务器仅安装产物与迁移。
- 固定 GitHub 提交 SHA、工作流 run ID 和构建产物哈希，分别核对线上二进制、前端及引擎源码；`/health` 返回 200 只表示服务可用，不等于分析/导出端到端验收通过。

**部署后复核（2026-09-22 全通过）**：8 服务全 active；goose_db_version=7（0001-0007 全 applied）；users 六新列 + 三新表在位；/api/v1/health 200；`PUT /auth/password`、`GET /user/profile` 未带 token → 401；`GET /auth/verify-email` 缺 token → 400（非 404）；server 近 1 小时日志零 error/panic。二进制 md5（与本地构建产物一致）：server `fc710f0b7b4a3c2ab6ce891097322b46`、worker `32780f6387227f7875b9422196b45183`、cli `e3cc2a846fe4701504f4f3cbed4da12a`。
