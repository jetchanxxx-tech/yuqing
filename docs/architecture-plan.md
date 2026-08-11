# SaaS 舆情监测平台 — 架构与实施计划

## Context

基于 BettaFish（微舆）多智能体舆情分析系统的架构灵感，构建一个可独立部署、支持 SaaS 订阅计费的舆情监测平台。BettaFish 拥有 5 引擎协作架构（Query/Media/Insight/Report/ForumEngine + MindSpider 爬虫），但它是 GPL-2.0 单实例 Python Flask 应用，缺乏多租户、计费、认证等 SaaS 必需能力。本项目从零构建平台层，引擎层独立重写（规避 GPL 传染）。

**用户核心约束：**
1. 单服务器优先部署，但代码设计支持横向扩展（nginx 轮询、读写分离、MQ 等配置驱动）
2. Database-per-tenant 多租户隔离
3. 接入第三方大模型 API（DeepSeek/Kimi/Moonshot 等），平台集中采购 → 加价计费给用户（转售模式）
4. 爬虫策略平台管理、用户不可选（傻瓜化）
5. MVP 聚焦单用户全流程跑通 + 计费 + 报表 + 数据面板

---

## 1. 架构总览：模块化单体（Go）

```
nginx (单服务器)
  /api/* → api:8080      / → 静态前端

┌──────────────────────┬─────────────────────────┐
│ Go process 1: api    │ Go process 2: worker    │
│ 无状态 HTTP 层       │ 队列消费 + 定时任务      │
│ auth/tenant/billing/ │ analysis/report/crawler │
│ usage REST handlers  │ 编排引擎                  │
└──────────────────────┴─────────────────────────┘
         │                        │
         └────────┬───────────────┘
                  │
    Queue 接口: memory(MVP) | redis | rabbitmq | nats
                  │
    ┌─────────────┴──────────────┐
    │ Python 引擎 (FastAPI x5)   │
    │ query/media/insight/       │
    │ report/forum               │
    └────────────────────────────┘
                  │
    PostgreSQL 15 + Redis 7 + 本地存储/MinIO
```

**核心原则：**
- 两个二进制、一个仓库。`api`(无状态 HTTP) 和 `worker`(队列消费+编排)，MVP 同一台机器上 systemd 管理两个进程
- **接口优先模块化**。每个模块暴露 Go interface，`internal/app/container.go` 显式依赖注入
- **Platform vs Business 分层**。`internal/platform/*` 只访问平台库，`internal/business/*` 只访问租户库
- **配置驱动一切**。读写分离、队列驱动、存储驱动、搜索引擎等均为配置开关
- **API 层永无状态**。任务状态通过 Queue 流转，api 只做翻译和校验

---

## 2. 技术栈

| 层 | 选择 | 原因 |
|---|---|---|
| API | Go 1.22+ / Gin | 单二进制、高并发、低内存 |
| DB | PostgreSQL 15 / pgx v5 | 成熟稳定，保留 MySQL 可替换性（标准 SQL） |
| 迁移 | goose (内嵌) | 平台 + 租户模板两套迁移 |
| 缓存 | Redis 7 / go-redis | 计数器、锁、限流、会话黑名单 |
| 队列 | 自建 Queue 接口: memory(MVP默认) / redis / rabbitmq / nats | 零依赖 MVP，扩展仅改配置 |
| LLM | LLMProvider 接口: OpenAI 兼容 API（DeepSeek/Kimi/Moonshot/Gemini 等云端大模型） | 平台转售模式，集中采购 API → 加价计量计费 |
| 存储 | ObjectStorage 接口: local(MVP) / s3(MinIO) | 配置切换 |
| 搜索 | Search 接口: pg tsvector(MVP默认) / elasticsearch(可选) | 默认禁用 ES |
| 引擎 | Python 3.11 + FastAPI × 5 | NLP/多模态生态，独立进程 |
| 部署 | Go 交叉编译二进制 + PostgreSQL/Redis/Nginx/Python 均原生安装 | 无容器依赖, systemd 守护, 部署脚本化 |
| 前端 | React 18 + TS + Vite + Ant Design 5 + ECharts 5 + TanStack Query + Zustand | 中文 B2B 标配，构建为静态文件由 nginx 托管 |

---

## 3. 目录结构

```
舆情监测/
├── platform/                  # Go 主项目
│   ├── cmd/
│   │   ├── server/main.go     # api 进程
│   │   ├── worker/main.go     # worker 进程
│   │   └── cli/main.go        # 运维 CLI (租户管理/回填等)
│   ├── internal/
│   │   ├── app/               # container.go (DI), server.go, worker.go
│   │   ├── config/            # 类型化配置，YAML + env merge, Validate()
│   │   ├── platform/          # 平台层（只能访问 platform DB）
│   │   │   ├── auth/          # JWT, RBAC, argon2id
│   │   │   ├── tenant/        # 生命周期, DB 自动创建/迁移
│   │   │   ├── user/          # 成员、邀请、角色
│   │   │   ├── usage/         # LLM Token 计量、预算、汇总
│   │   │   └── billing/       # 套餐、订阅、账单、支付网关
│   │   ├── business/          # 业务层（只能访问 tenant DB）
│   │   │   ├── analysis/      # 编排 + 状态机
│   │   │   ├── datasource/    # 平台配置的数据源（租户只读）
│   │   │   ├── crawler/       # 爬虫任务管理 + 数据摄入（深度设计另排）
│   │   │   ├── sentiment/     # 情感编排 + 聚合
│   │   │   ├── report/        # 报告生命周期 + 导出
│   │   │   └── dashboard/     # 指标聚合 + 缓存
│   │   ├── engine/            # 引擎契约 + HTTP 传输
│   │   │   ├── contract.go    # Query/Media/Insight/Report/Forum 接口
│   │   │   └── httpx/         # HTTP client, 重试, 熔断
│   │   ├── api/
│   │   │   ├── router.go      # /api/v1 路由树
│   │   │   ├── middleware/    # authn, tenant, rbac, ratelimit, audit
│   │   │   └── v1/            # handlers per resource (thin, DTO only)
│   │   └── pkg/
│   │       ├── db/            # Manager, 租户解析器, 读写分离
│   │       ├── queue/         # Queue 接口 + memory/redis/rabbitmq/nats
│   │       ├── llm/           # Provider 接口 + metered wrapper
│   │       ├── storage/       # ObjectStorage 接口 + local/s3
│   │       ├── search/        # Search 接口 + pg/elasticsearch
│   │       ├── cache/         # Redis 包装
│   │       ├── id/            # ULID
│   │       ├── errors/        # 类型错误 + envelope codes
│   │       └── observ/        # slog JSON + request_id
│   ├── migrations/
│   │   ├── platform/          # 0001_init.sql ...
│   │   └── tenant/            # 0001_init.sql ... (租户模板)
│   └── go.mod
├── engines/                   # Python FastAPI × 5
│   ├── query_engine/          # 多源搜索 + 去重
│   ├── media_engine/          # 视频/图片/评论多模态
│   ├── insight_engine/        # 深度分析 + 情感 + 话题
│   ├── report_engine/         # 模板 IR → HTML/PDF/MD/DOCX
│   ├── forum_engine/          # 多 Agent 论坛协调
│   └── common/                # 共享: 认证中间件, LLM client, models
├── web/                       # React 前端
├── scripts/                   # 部署运维脚本
│   ├── deploy.sh               # 一键部署
│   ├── migrate.sh              # 数据库迁移
│   ├── backup.sh               # 备份
│   ├── nginx.conf              # nginx 配置模板
│   └── systemd/                # systemd unit 文件
├── config.example.yaml         # 配置模板
├── Makefile                    # build targets: build-all, test, lint, deploy
└── docs/                       # API 契约, 运维手册
```

---

## 4. 多租户数据隔离

**Database-per-tenant**，租户注册时自动创建独立数据库：

```
bettafish_platform  (平台元数据)
bettafish_t_{ulid}  (每个租户一个独立 DB)
```

- `pkg/db.Manager.Platform(ctx)` → 平台库连接池
- `pkg/db.Manager.Tenant(ctx, tenantID)` → 租户 DB（Write/Read/WithTx）
- 租户池 LRU 驱逐（maxPools: 50），懒加载创建
- goose 迁移内嵌二进制，注册时自动运行租户模板迁移

---

## 5. 核心基础设施 (`pkg/*`)

### 5.1 配置 (`config.go`)
单一 YAML 文件 + `APP_` 前缀 env 覆盖，所有扩展开关在此：
- `db.readWriteSplit: false` → Read() 返回 primary（MVP 默认）
- `queue.driver: memory` → 进程内通道（MVP 默认）
- `storage.driver: local` → 本地文件系统
- `search.driver: pg` → PostgreSQL tsvector
- `llm.models[]` → 模型列表 + 定价配置
- `rateLimit.enabled: true`

### 5.2 Queue 接口
```go
type Queue interface {
    Publish(ctx, topic, msg) error
    Subscribe(ctx, topic, handler) error
    Close() error
}
```
- memory: 进程内 sync.Mutex + goroutine，重启丢消息（分析任务有 sweeper 补偿）
- redis: LPUSH/BRPOP + claim 超时重入队
- rabbitmq/nats: 完整 ACK + DLX

### 5.3 LLM Provider + Metered Wrapper（API 转售模式）

**模式**: 平台统一采购多家大模型 API → 统一 OpenAI 兼容接口封装 → 按平台定价计费给用户（含利润 margin）

每次 LLM 调用的数据路径：
```
worker/engine → llm.MeteredProvider (包装 LLMProvider)
  1. 快速预算检查: Redis MGET 计数器 (约1ms)
     status: ok | warn(≥80%) | exceeded(hard cap) → ErrBudgetExceeded
  2. Chat() → 后端 API provider (DeepSeek/Kimi/Moonshot/Gemini...)
     所有 API 调用统一走 OpenAI 兼容格式
  3. Meter.Record(UsageEvent{tenant, model, tokens, cost})
     ├─ Redis INCRBY: usage:{tenant}:{period}:{model}:{in|out}
     └─ Publish → queue topic usage.events (异步)
  4. 返回 response + usage
```

**成本模型（转售加价）:**
```
平台成本: cost_in = API厂商原始价格 × token 消耗
用户计费: cost_out = 平台定价 × token 消耗  (含利润 margin)
利润 = cost_out - cost_in
```
- 每个模型在 `llm.models[]` 中配置: `apiBaseUrl`, `apiKey`, `inputCostPerM`(平台进价), `outputCostPerM`(平台进价), `userInputPricePerM`(用户售价), `userOutputPricePerM`(用户售价)
- 成本存储: `cost_micro_cny`(平台成本) + `billed_micro_cny`(用户计费) 双字段
- 支持动态切换底层 API 供应商（如 DeepSeek 不可用时自动 fallback 到备选 provider）

**预算语义：**
- Free/Pro: `hard_cap` → 超限拒绝新任务
- Business: `overage` → 超额按 overage_rate 计费
- Enterprise: 无硬限制，管理员可 BlockSpend

---

## 6. 订阅套餐设计 (WorkBuddy/Trae 风格)

| 功能 | Free 体验版 | Pro 专业版 (¥99/月) | Business 企业版 (¥499/月) | Enterprise 旗舰版 |
|---|---|---|---|---|
| 月度分析次数 | 5 | 50 | 500 | 无限制 |
| LLM Token 配额 | 1M | 10M | 100M | 无限制(公平使用) |
| 预算模式 | hard cap | hard cap | overage 计费 | 无 cap |
| 并发分析数 | 1 | 2 | 5 | 20+ |
| 团队席位 | 1 | 3 | 20 | 无限制 |
| 数据源 | 5个 | 全部公开 | 全部+优先 | 全部+自定义 |
| 数据保留 | 30天 | 90天 | 1年 | 永久 |
| 导出格式 | HTML | HTML+MD | +PDF+DOCX | +PPTX+定时推送 |
| API | ✗ | ✗ | 只读 | 完整+Webhooks |
| 自定义模型 | ✗ | ✗ | ✗ | BYOK vLLM/Ollama |

套餐变更：升级按比例计费；降级下周期生效；取消到周期结束。

---

## 7. 分析流程状态机

```
draft → queued → acquiring_budget → fetching → analyzing → generating_report → completed
any active state → failed | canceled
```

Worker 管道（consumer on `analysis.tasks`）：
1. `acquiring_budget`: Redis 预留估计 Token，不足 → failed (BUDGET_EXCEEDED)
2. `fetching`: QueryEngine + MediaEngine + Crawler 调度 → raw_documents (content_hash 去重)
3. `analyzing`: InsightEngine (情感+话题+趋势) + ForumEngine (深度分析多 Agent 辩论)
4. `generating_report`: ReportEngine → HTML/PDF/MD/DOCX → Object Storage
5. SSE 进度推送 → 前端实时展示

并发控制：Redis `SetNX tenant:{id}:analysis:slots`，按套餐 `max_concurrent` 控制
补偿：sweeper cron 重新入队超时 running 任务

---

## 8. 数据库设计概览

### 平台库 (`migrations/platform/`)
- tenants, users, tenant_members
- plans (套餐定义，features_json 存储特性开关)
- subscriptions, billing_periods
- invoices
- usage_events (按月分区), usage_daily (账单权威汇总)
- api_keys, audit_logs

### 租户模板 (`migrations/tenant/`)
- data_sources (平台管理的只读副本)
- analyses, task_steps
- raw_documents (content_hash UNIQUE 去重)
- sentiment_results, topics, alerts
- reports, report_templates, crawl_jobs
- dashboard_daily (预聚合加速)

### 读写分离策略
- `Write()`: 所有写路径（分析创建、数据摄入、计量、状态变更）
- `Read()`: 面板、列表页等可容忍副本延迟的读
- `readWriteSplit=false` 时 Read() = Write() = primary

---

## 9. 前端页面

| 页面 | 关键功能 |
|---|---|
| Login/Register | 注册自动创建租户 |
| Dashboard | 概览卡片 + 趋势图(ECharts) + 来源分布 + 热门话题 + 告警 |
| NewAnalysisPage | 4步向导: 类型 → 关键词/来源/日期 → 成本预估 → 提交 |
| AnalysisListPage | 列表+筛选+排序+分页 |
| AnalysisDetailPage | 实时状态时间线(SSE) + 结果查看 + 情感/话题/文档列表 |
| ReportHistoryPage | 报告历史 + 格式下载(按套餐限制) |
| PlanSelectionPage | 套餐对比卡片 + 升级流程 |
| UsagePage | Token 用量仪表 + 周期快照 |
| InvoicesPage | 账单列表 + PDF 下载 |
| Settings | Profile, Members, API Keys(Enterprise) |
| AdminPanel | 租户管理, 平台用量, 套餐管理 (platform_admin) |

---

## 10. API 设计

- `/api/v1` 前缀，破坏性变更 → `/api/v2` 共存
- 认证: `POST /auth/register|login|refresh|logout`
- REST 资源: analyses, reports, dashboard, billing, members, admin
- 错误信封: `{code, message, details, request_id}`
- 限流: Redis 滑动窗口，按租户+套餐 `rate_limit` 配置
- SSE: `/analyses/{id}/events` 实时进度
- swag 注释 → OpenAPI 3.1 → CI 生成前端类型

---

## 11. 部署（二进制 + systemd，无容器）

### 整体结构
```
/opt/yuging/                     # 安装根目录
├── bin/
│   ├── yuging-server            # Go api 二进制
│   ├── yuging-worker            # Go worker 二进制
│   └── yuging-cli               # 运维 CLI
├── engines/                     # Python 引擎 (venv + systemd)
│   ├── query_engine/
│   ├── media_engine/
│   ├── insight_engine/
│   ├── report_engine/
│   ├── forum_engine/
│   └── common/
├── web/                         # 前端静态文件 (nginx 托管)
│   └── dist/
├── config/
│   └── config.yaml              # 统一配置
├── data/                        # 本地存储 (reports, uploads, logs)
│   ├── storage/
│   └── logs/
├── migrations/
│   ├── platform/
│   └── tenant/
└── scripts/
    ├── deploy.sh                # 一键部署脚本
    ├── migrate.sh               # 数据库迁移
    └── backup.sh                # 备份脚本
```

### 组件安装清单

| 组件 | 安装方式 | 说明 |
|---|---|---|
| Nginx | 包管理器 (apt/yum) | 反向代理 + 静态文件 + SSL 终端 |
| PostgreSQL 15 | 包管理器或官方 repo | 平台库 + 租户库 |
| Redis 7 | 包管理器或源码编译 | 缓存/队列/计数 |
| Go 二进制 | `GOOS=linux GOARCH=amd64 go build` 交叉编译 | 两个二进制: server + worker |
| Python 引擎 | venv + pip install -r requirements.txt | systemd 守护 |
| 前端 | `npm run build` → 静态文件 | nginx 托管 |
| SSL | Let's Encrypt certbot | 自动续期 |

### systemd 服务定义

```
/etc/systemd/system/
├── yuging-server.service    # Go api 进程
├── yuging-worker.service    # Go worker 进程
├── yuging-query.service     # Python query_engine
├── yuging-media.service     # Python media_engine
├── yuging-insight.service   # Python insight_engine
├── yuging-report.service    # Python report_engine
└── yuging-forum.service     # Python forum_engine
```

### 部署脚本设计原则

**幂等性要求**：脚本可重复执行，已安装/已运行的组件自动跳过，**绝不覆盖系统已有配置和服务**。

```
每个步骤的标准模式:
  check() → 检测组件是否已满足（进程存在? 版本正确? 配置正确?）
  skip()  → 已满足则打印 "[SKIP] 组件名 already installed/running, skip"
  run()   → 未满足则执行安装
```

### 主部署脚本 (`scripts/deploy.sh`)

```bash
#!/bin/bash
set -euo pipefail

APP_ROOT="/opt/yuging"
CONFIG_SRC="./config.example.yaml"
CONFIG_DST="$APP_ROOT/config/config.yaml"
LOG_FILE="$APP_ROOT/data/logs/deploy.log"

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; NC='\033[0m'
log_info()  { echo -e "${GREEN}[INFO]${NC}  $*" | tee -a "$LOG_FILE"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $*" | tee -a "$LOG_FILE"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" | tee -a "$LOG_FILE"; }
log_skip()  { echo -e "${YELLOW}[SKIP]${NC} $*" | tee -a "$LOG_FILE"; }

# ====== 每个步骤使用统一的幂等模式 ======

# 1. Nginx 检测安装
check_nginx() {
    if command -v nginx &>/dev/null && nginx -v 2>&1 | grep -q "nginx"; then
        log_skip "nginx already installed: $(nginx -v 2>&1)"
        return 0
    fi
    return 1
}

# 2. PostgreSQL 检测安装
check_postgres() {
    if systemctl is-active --quiet postgresql 2>/dev/null; then
        local ver=$(psql --version 2>/dev/null | grep -oP '\d+\.\d+')
        log_skip "PostgreSQL $ver already running, skip install"
        return 0
    fi
    return 1
}

# 3. Redis 检测安装
check_redis() {
    if systemctl is-active --quiet redis-server 2>/dev/null; then
        log_skip "Redis already running: $(redis-cli --version)"
        return 0
    fi
    return 1
}

# 4. 平台数据库检测创建（不覆盖已有库）
check_platform_db() {
    if sudo -u postgres psql -lqt 2>/dev/null | grep -q "yuging_platform"; then
        log_skip "Platform DB 'yuging_platform' already exists, skip creation"
        return 0
    fi
    return 1
}

# 5. Go 二进制编译（仅文件不存在时重新编译）
check_go_binaries() {
    if [ -x "$APP_ROOT/bin/yuging-server" ] && [ -x "$APP_ROOT/bin/yuging-worker" ]; then
        log_skip "Go binaries already compiled, skip build"
        log_info "  (run 'make build' manually to recompile)"
        return 0
    fi
    return 1
}

# 6. Python venv + 引擎依赖检测
check_python_engines() {
    local all_ok=true
    for engine in query media insight report forum; do
        if [ -f "$APP_ROOT/engines/${engine}_engine/venv/bin/python" ]; then
            log_skip "Python venv for ${engine}_engine already exists"
        else
            all_ok=false
        fi
    done
    $all_ok && return 0 || return 1
}

# 7. 前端构建检测
check_frontend() {
    if [ -f "$APP_ROOT/web/dist/index.html" ]; then
        log_skip "Frontend dist/ already exists, skip build"
        log_info "  (run 'cd web && npm run build' manually to rebuild)"
        return 0
    fi
    return 1
}

# 8. nginx 配置检测（已存在则不覆盖）
check_nginx_config() {
    if [ -f /etc/nginx/sites-enabled/yuging.conf ] || [ -f /etc/nginx/conf.d/yuging.conf ]; then
        log_warn "nginx config for yuging already exists, skip overwrite"
        log_info "  (manual merge needed if config changed: diff scripts/nginx.conf /etc/nginx/conf.d/yuging.conf)"
        return 0
    fi
    return 1
}

# 9. systemd 服务注册检测
check_systemd_services() {
    if systemctl list-unit-files yuging-server.service 2>/dev/null | grep -q "yuging-server"; then
        log_skip "systemd services already registered, skip"
        return 0
    fi
    return 1
}

# 10. SSL 证书检测
check_ssl() {
    if [ -f "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" ]; then
        log_skip "SSL cert for $DOMAIN already exists"
        return 0
    fi
    return 1
}

# ==== 主流程 ====
main() {
    mkdir -p "$APP_ROOT"/{bin,engines,web,config,data/{storage,logs},scripts}

    check_nginx        || { apt-get install -y nginx; log_info "nginx installed"; }
    check_postgres     || { apt-get install -y postgresql-15; systemctl enable --now postgresql; }
    check_redis        || { apt-get install -y redis-server; systemctl enable --now redis-server; }
    check_platform_db  || { sudo -u postgres createdb yuging_platform; sudo -u postgres createuser yuging; }
    check_go_binaries  || { cd platform && CGO_ENABLED=0 go build -o "$APP_ROOT/bin/" ./cmd/...; }
    check_python_engines || { init_all_python_venvs; }
    check_frontend     || { cd web && npm ci && npm run build && cp -r dist "$APP_ROOT/web/"; }
    check_nginx_config || { cp scripts/nginx.conf /etc/nginx/conf.d/yuging.conf; nginx -t && systemctl reload nginx; }
    check_ssl          || { certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos; }
    check_systemd_services || { register_and_start_services; }

    # 数据库迁移 (幂等: goose 只执行未运行的迁移)
    "$APP_ROOT/bin/yuging-cli" migrate platform

    # 配置处理 (已存在则不覆盖, 仅提示)
    if [ -f "$CONFIG_DST" ]; then
        log_warn "Config already exists at $CONFIG_DST, skip overwrite"
        log_info "  (diff $CONFIG_SRC $CONFIG_DST to review changes)"
    else
        cp "$CONFIG_SRC" "$CONFIG_DST"
        log_warn "Please edit $CONFIG_DST with your actual values"
    fi

    # 健康检查
    sleep 2
    for svc in yuging-server yuging-worker; do
        systemctl is-active --quiet "$svc" && log_info "$svc: OK" || log_error "$svc: FAILED"
    done
    curl -sf http://localhost:8080/api/v1/health && log_info "Health check: PASS" || log_error "Health check: FAIL"
}

main "$@"
```

### nginx 配置要点
```nginx
server {
    listen 443 ssl http2;
    server_name your-domain.com;
    root /opt/yuging/web/dist;       # 前端静态文件
    location / {
        try_files $uri /index.html;   # SPA fallback
    }
    location /api/ {
        proxy_pass http://127.0.0.1:8080;  # Go api
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_buffering off;                # SSE 支持
    }
}
```

### 扩展：多机部署后的变更点

当单机无法满足时，下图所示拆分仅需**修改 nginx upstream + 配置 yaml**，无需改代码：

```
                 nginx (LB)
                 /        \
     server-1:api        server-2:api       ← 横向扩展
          |                   |
   server-3:postgres    server-4:postgres-ro ← 读写分离
          |                   |
   server-5:redis       server-6:python-engines
```

**配置切换清单（不改代码）：**
- `server.addr` → api 监听地址
- `db.replicas[]` + `db.readWriteSplit: true` → 读写分离
- `queue.driver: redis` (单机) → `rabbitmq` (多机)
- `storage.driver: local` → `s3` (MinIO 独立部署)
- `engines.*.url` → Python 引擎独立部署地址

---

## 12. 开发阶段

### Phase 1 — 核心平台 (auth + tenant + 基础分析流程)
- 仓库骨架 (Go + web + scripts/deploy.sh)
- config, pkg/db (Manager + resolver + 读写分离), queue memory 驱动, ULID, errors/observ
- auth (register/login/refresh/JWT/RBAC)
- tenant provisioning (自动建库 + 迁移)
- user/members
- analysis create/list/detail + 状态机
- query-engine (2 个数据源 MVP: RSS + 1 个搜索 API) + 摄入 + 去重
- 前端: login/register, dashboard 外壳, 分析向导桩, 详情页

### Phase 2 — 计费 + 订阅 + 计量
- plans 目录, subscription 生命周期 + cron 轮转
- llm.MeteredProvider, Redis 计数器
- usage.events 消费 + usage_daily 汇总
- 预算预检 + hard-cap 执行
- 账单生成 (manual 网关 + PDF)
- 前端: 套餐选择, 订阅管理, 用量仪表, 账单

### Phase 3 — 分析引擎 + 报告
- insight-engine (情感+话题+趋势)
- report-engine (模板 IR → HTML/MD/PDF/DOCX)
- report service + 存储导出
- 格式按套餐限制
- 前端: 完整向导, SSE 实时进度, 结果视图(情感/话题可视化), 报告查看器+下载

### Phase 4 — Dashboard + 高级功能
- dashboard 聚合 + 缓存 + dashboard_daily 汇总
- alerts
- media-engine 集成, forum-engine 深度分析
- 平台管理面板 (租户, 用量, 套餐)
- Enterprise API keys

### Phase 5 — 扩展能力
- rabbitmq/nats 驱动, s3 驱动
- 读写分离验证, elasticsearch 驱动
- scale 部署方案端到端验证
- 负载测试 (单机 200 并发目标)
- 运维 runbook

每个 Phase 结束交付: 单元测试 + 契约测试 + 前端 E2E smoke (Playwright)

---

## 13. 关键风险

1. **LLM API 供应商稳定性**: DeepSeek/Kimi 等 API 偶尔限流/宕机 → LLMProvider 接口支持多供应商 fallback（同模型多个 baseUrl 配置），熔断后自动切换
2. **API 成本波动**: 厂商价格变化影响利润率 → 模型定价配置热更新，账单以调用时 `userPrice` 为准
3. **Token 计数差异**: 各厂商 `usage` 字段返回不一致 → 统一以 API 返回的 usage 为准，内部不做二次计算
2. **单机 PostgreSQL 争用**: N 租户池 × 并发分析写入 → 通过 pool 上限 + 预聚合缓解
3. **BettaFish GPL-2.0**: 引擎独立进程重写，不复制代码/不链接，法律审核前不发布任何移植内容
4. **爬虫傻瓜化**: 契约和 schema 已固定；反爬/限速/robots 合规策略单独设计
5. **memory queue 消息丢失**: sweeper 补偿机制覆盖，生产环境切换到 Redis/RabbitMQ

---

## 14. 首要实现文件（入口点）

1. `platform/internal/config/config.go` — 所有扩展开关的聚集点
2. `platform/internal/pkg/db/manager.go` — 多租户 DB 解析器，读写分离
3. `platform/internal/business/analysis/service.go` — 分析状态机 + 编排
4. `platform/internal/pkg/llm/metered.go` — LLM 计量包装器（计费核心）
5. `platform/internal/api/router.go` — REST 路由 + 中间件栈

---

## Verification

每个 Phase 的验收标准：
- Phase 1: 新用户注册 → 自动创建租户 DB → 创建分析 → 状态流转到 completed，docs 可见；重启 api 后 sweeper 自动修复卡住任务
- Phase 2: 模拟用量产生正确的 daily rollup 和可计费账单；Free 租户 1M token 被硬阻断；Business 租户超额正确计费
- Phase 3: 端到端 event_analysis → docs → sentiment → topics → 可下载 PDF 报告，格式按套餐限制
- Phase 4: Dashboard 在 10 万文档的租户上次秒级渲染；管理后台可挂起租户并立即生效
- Phase 5: 同一二进制在多机部署下跑通所有驱动切换（queue/storage/search/db 均改为远程），零代码改动
