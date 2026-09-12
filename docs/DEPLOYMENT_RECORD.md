# 盘古舆情 — 部署记录

> 首次生产部署：2026-09-12 · Ubuntu 24.04.4 LTS · 单机部署
>
> **本文件不含任何真实凭据。** 真实账号密码、API Key、服务器 IP 保存在
> 本地 `credentials.local.md`（已在 `.gitignore` 中排除，不入库）。

---

## 一、部署环境

| 项目 | 值 |
|------|-----|
| 操作系统 | Ubuntu 24.04.4 LTS |
| CPU / 内存 / 磁盘 | 2 核 / 1.7 GB / 40 GB（已加 2 GB swap） |
| 部署方式 | 二进制 + systemd（无容器） |
| 反向代理 | nginx 1.24.0（系统自带） |
| 数据库 | PostgreSQL 16.15 |
| 缓存 | Redis 7.0.15 |
| 运行时 | Go 1.25.5 / Node 22.23.2 / Python 3.12.3 |

**关于内存**：官方建议 4 GB，此实例为 1.7 GB。已创建 2 GB swap 防止构建期 OOM，
实测部署与运行正常。生产环境建议按建议配置。

---

## 二、部署步骤

```bash
# 1. 获取代码（GitHub 不可达时可改用本地打包 + scp 上传）
git clone https://github.com/jetchanxxx-tech/yuqing.git /opt/pangu-source
cd /opt/pangu-source

# 2. 执行部署（幂等，可重复运行）
export YUGING_DOMAIN=<域名或IP>
export YUGING_JWT_SECRET=$(openssl rand -hex 32)
export BOCHA_API_KEY=<你的 Bocha API Key>
export SKIP_SSL=1                      # 无域名时跳过 SSL

bash scripts/deploy.sh
```

`deploy.sh` 自动完成：系统依赖安装 → Go 二进制编译 → 前端构建 →
Python venv + Scrapling → 数据库初始化 → systemd 注册 → nginx 配置 → 健康检查。

输出中 `[SKIP]` 表示该组件已存在，自动跳过。

---

## 三、部署后状态

```
yuging-server    active    0.0.0.0:8080     Go API
yuging-worker    active                    队列消费
nginx            active    0.0.0.0:80      反向代理
postgresql       active    127.0.0.1:5432
redis-server     active    127.0.0.1:6379
```

**外部访问验证**：

| 入口 | 期望结果 |
|------|---------|
| `GET /api/v1/health` | `{"status":"ok"}` |
| `GET /` | HTTP 200，React 应用 |
| 注册 → 登录 → 创建分析 | 全链路通过 |

---

## 四、凭据管理

真实凭据**不入库**。部署时按下列方式注入：

| 凭据 | 注入方式 |
|------|---------|
| JWT 密钥 | `YUGING_JWT_SECRET` 环境变量（部署时生成） |
| 数据库密码 | `YUGING_DB_PASSWORD` 环境变量（默认 `yuging`） |
| Bocha API Key | `BOCHA_API_KEY` 环境变量，或部署后经 Admin API 在线更新 |
| 管理员账号 | 部署后通过 `POST /api/v1/auth/register` 自行注册 |

**Bocha Key 在线更新**（无需重启）：

```bash
curl -X PUT https://<你的域名>/api/v1/admin/settings \
  -H "Authorization: Bearer <platform_admin 的 access_token>" \
  -H "Content-Type: application/json" \
  -d '{"bocha_api_key":"<新 key>"}'
```

---

## 五、首次部署暴露并修复的 6 个脚本缺陷

这些问题**本地无法发现** —— `bash -n` 语法通过、shellcheck 无告警、
单元测试全绿，只在真实目标环境运行时才暴露。

| # | 缺陷 | 症状 | 根因 | 提交 |
|---|------|------|------|------|
| 1 | `mkdir` 晚于首个 `log_info` | 脚本第 39 行静默退出 | `tee` 写入不存在的目录 + `set -e` 中断 | `3d7ca42` |
| 2 | 未配置国内镜像源 | Go 依赖 `dial tcp i/o timeout` | `proxy.golang.org` 在境内不可达 | `22463d1` |
| 3 | venv 检测条件错误 | `ensurepip is not available` | 检测 `import venv` 而非 `import ensurepip` | `adabdae` |
| 4 | 残缺 venv 被误判完成 | `pip: No such file or directory` | 检测只看 `bin/python`，未看 `bin/pip` | `976559c` |
| 5 | **二进制命名不匹配** | 服务起不来、健康检查失败 | `go build -o dir/ ./cmd/...` 产出 `cli`/`server`/`worker`，systemd 引用 `yuging-*` | `72ed5e6` |
| 6 | **nginx default_server 冲突** | 外部访问返回 nginx 欢迎页 | 系统自带站点占用 `default_server` | `00cf3d1` |

### 另有 2 个环境问题

- **SSH 密码认证失败**：Windows 原生 `sshpass` 与 MSYS `ssh` 的 pty 机制不兼容，
  改用 OpenSSH 内置的 `SSH_ASKPASS` 解决。
- **GitHub 访问间歇中断**（`GnuTLS recv error`）：改用本地打包 + `scp` 上传绕过。

---

## 六、已知限制

| 限制 | 说明 | 影响 |
|------|------|------|
| **内存数据存储** | 当前用内存 store，重启服务数据重置 | 生产需接 PostgreSQL store（接口已预留） |
| Python 引擎为 mock | ForumEngine / MediaEngine 返回预置数据 | 待 LLM API key 接入后替换 |
| SSE 为轮询实现 | 1 秒轮询状态机 | 多实例部署时换 Redis pub/sub |
| 无 SSL | 未配域名，HTTP 访问 | 有域名后重跑 deploy.sh 即自动切换 HTTPS 模板 |

---

## 七、运维速查

```bash
# 服务状态
systemctl status yuging-server yuging-worker

# 实时日志
journalctl -u yuging-server -f

# 重启（配置变更后）
systemctl restart yuging-server yuging-worker

# 数据库备份
sudo -u postgres pg_dump yuging_platform > backup_$(date +%F).sql
```
