# 盘古舆情 · 规范化部署手册（Runbook）

> 适用：在全新 Ubuntu 24.04 服务器上从零部署盘古舆情到生产可用。
> 也可供其他智能体/运维人员照步骤执行。所有历史踩坑已内嵌为「⚠️ 坑位」提示。
> 最后验证：2026-09-15（生产 47.120.20.10）

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

```bash
cd /opt/yuqing
YUQING_CONFIG=/opt/yuqing/config/config.yaml bin/yuqing-cli migrate platform
# 应输出 applied: 0001..0005（0005 = analyses.dimensions 列；
# 缺它新 server 所有 analyses 查询报 column does not exist）
```

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
