# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**盘古舆情** — AI 原生 SaaS 舆情监测平台，面向中小企业与个人品牌。以 Scrapling 自适应爬虫 + Bocha AI 搜索实现真实数据采集，用多 Agent 辩论协作将"监测"升级为"研判"。

参考开源项目 BettaFish（GPL-2.0）架构灵感，从零构建平台层（Go），引擎层独立重写（Python，规避 GPL 传染）。爬虫层集成 Scrapling（BSD-3-Clause）。

## Tech Stack

- **Platform**: Go 1.25, Gin, pgx v5, goose migrations, golang-jwt v5, argon2id
- **Engines**: Python 3.11+, FastAPI × 5 (query/media/insight/report/forum), Scrapling (爬虫)
- **Frontend**: React 19 + TypeScript + Vite 8 + Ant Design 5（含 v5-patch-for-react-19）+ ECharts 5（自写按需封装）+ TanStack Query + React Router 7
- **Database**: PostgreSQL 15 (database-per-tenant isolation)
- **Cache**: Redis 7
- **Search**: Bocha AI API (OpenAI-compatible)
- **Deploy**: Go cross-compiled binaries + systemd, no containers. Nginx reverse proxy + static files.

## Commands

```bash
# Go (platform/)
cd platform
make build                          # cross-compile linux/amd64 → bin/
go test ./... -count=1               # 332 tests, 21 packages
go test -run TestRegister ./internal/platform/auth/
go test ./test/integration/ -count=1
go vet ./...
go run ./cmd/server                  # dev: api (:8080) — 内存 store，无需 DB

# 前端 (web/)
cd web
npm ci && npm run build              # = tsc -b && vite build → dist/
npm run dev                          # Vite dev (proxy /api → 127.0.0.1:8080)
npx tsc -b

# Python engines (engines/{name}_engine/)
python3 -m venv venv && source venv/bin/activate
pip install -r ../requirements.txt   # 含 scrapling[fetchers]
scrapling install --chromium          # 首次 ~150MB
uvicorn main:app --port 8000
BOCHA_API_KEY=sk-xxx uvicorn main:app --port 8000  # 真实搜索

# Python 测试
cd engines && python3 -m pytest tests/ -v

# 离线演示 (demo/)
双击 demo/index.html                 # "雅阁后排" 7 步产品演示（零依赖）
```

## Architecture: Modular Monolith

两个 Go 二进制从 `platform/cmd/` 编译：

| Binary | Role |
|--------|------|
| `yuging-server` | Stateless HTTP API — `app.Build(cfg)` 组合根注入服务图 → `api.NewRouter(cfg, logger, deps)` |
| `yuging-worker` | Queue consumers + cron（analysis.tasks / usage.events 消费，管线待实现） |

### 组合根（DI 唯一装配点）

`platform/internal/app/container.go` 的 `Build(cfg)` 是全部服务实现的唯一装配处：

- 共享 tenant store：`auth.NewSharedTenantStore` 让注册与 admin 读同一份数据
- Report 套餐 gating 双闭包注入：`planCodeFor` + `planProvider`，未知租户 fail-closed
- Alert EmailSender nil 静默丢弃
- Platform Settings：`settings.MemoryStore` 从环境变量种子（BOCHA_API_KEY），admin 可在线覆盖
- API Key Service + Usage Meter 注入

## Layer Boundaries (critical)

```
internal/
├── platform/    ← 平台库 (tenants, users, billing, settings, apikey)
│   auth, tenant, user, usage, billing, settings, apikey
├── business/    ← 租户库 (analyses, documents, reports)
│   analysis, datasource, crawler, sentiment, report, dashboard, alert
├── engine/      ← Go 契约 + HTTP transports (real.go → Python /search)
├── api/         ← thin handlers: DTO 校验 → service → respondError 信封
│   middleware/  ← authn(JWT/ApiKey双认证), tenant, RBAC, ratelimit, audit
└── pkg/         ← db, queue, llm, storage, search, cache, id, errors, observ
```

**Platform vs Business enforcement**: 双向禁止跨层 import。已知例外：`business/report` 依赖 `platform/billing`（函数注入缓解，REVIEW_REPORT §8）。

## Key Design Patterns

### 数据采集链路（Scrapling + Bocha）

```
Go Analysis Service (state=fetching)
  → RealCrawlerEngine.Crawl()  [HTTP, bochaKeyFunc 注入]
  → Python query_engine /search
     1. Bocha API 搜索关键词 → URL 列表
     2. Scrapling 抓取正文 (Fetcher静态 / DynamicFetcher JS / StealthyFetcher反爬)
     3. adaptive=True 自适应选择器 + content_hash 去重
  → Document 列表回传 Go → raw_documents 入库
```

- `engines/common/scraper.py` `PageScraper` — Scrapling 封装（错误降级、去重、source 路由）
- Bocha key 三级来源：环境变量 BOCHA_API_KEY → 平台 settings（Admin PUT /admin/settings）→ 请求参数
- Fake 兜底：无 Scrapling 时返回空文档，不崩溃

### 服务层（全部内存 store，TDD，332 tests）

| 服务 | 核心职责 |
|------|---------|
| `auth.Service` | Register(User+Tenant+Member 三连+SetQuota 1M)/Login/Authenticate/Refresh |
| `tenant.Service` | Get/List/Suspend/Resume（状态机校验） |
| `analysis.Service` | Create/Get/List/Cancel/Rerun + **Transition**（worker 唯一写路径） |
| `report.Service` | CreateFromAnalysis/DownloadURL — 套餐 format gating fail-closed |
| `dashboard.Service` | 实时计算 Overview/Trend（zero-value 数组防 ECharts null） |
| `alert.Service` | Create/Check（negPct ≥ threshold 触发 + sender.Send） |
| `apikey.Service` | CreateKey(pangu_+ULID, 只返回一次)/ValidateKey(fail-closed)/RevokeKey(跨租户 404) |
| `usage.Meter` | Record/BudgetStatus/Aggregate（全租户聚合供 /admin/usage） |

### LLM Metered Provider (API resale)

每次 LLM 调用：预算检查（hard_cap fail-closed）→ Chat → `Meter.Record`。成本**按 token 类别分档**（prompt × 输入价 + completion × 输出价），双字段 `cost_micro_cny` / `billed_micro_cny`。Free/Pro = hard_cap，Business = overage，Enterprise = 无 cap。`FakeProvider` 返回固定"雅阁后排"摘要（token 绕过，联调前勿动）。

### Analysis State Machine

```
draft → queued → acquiring_budget → fetching → analyzing → generating_report → completed
any active state → failed | canceled
```

`Transition` 是唯一写路径；终态不可跳转（ErrConflict）。

### SSE 实时推送

`GET /analyses/:id/events` — text/event-stream，`SSEPollInterval`（默认 1s）轮询状态机，state/progress 变化发 `event: progress`，终态发 `event: final` 后关闭。客户端断开（ctx.Done）停止。多实例部署时换 Redis pub/sub。nginx 需 `X-Accel-Buffering: no`。

### 认证双通道

`middleware.AuthAny`：`pangu_` 前缀分流到 ApiKey 验证（映射最小权限 `api_service` 角色），其余走 JWT。API Key 存 SHA-256 哈希，创建时仅返回一次原始值。

### 测试分层（332 tests）

| 层 | 位置 | 内容 |
|----|------|------|
| 单元 | 各包 `*_test.go` | TDD，表驱动 |
| 契约 | `api/v1/contract_test.go` | 锁 endpoint 响应结构 + RBAC 403 + gap registry |
| 集成 | `test/integration/` | 全链路/计费/隔离/并发 |
| Python | `engines/tests/` | Scrapling 去重/降级/路由测试 |
| E2E | `web/e2e/` | 已写未运行（@playwright/test 未装） |

## MVP Scope (P0 + 扩展 = 16/16 完成)

| # | Feature | Status |
|---|---------|--------|
| F01-F10 | 认证/任务/采集/看板/摘要/报告/计费/保留/后台/告警 | ✅ |
| F11 | 多 Agent 辩论 (ForumEngine) | ✅ Python mock（4Agent×3轮） |
| F12 | 多模态 (MediaEngine) | ✅ Python mock（5 预置结果） |
| F13 | SSE 实时推送 | ✅ 轮询实现 |
| F14 | API Key 管理 | ✅ pangu_ 格式 |
| F15 | /admin/usage 聚合 | ✅ Meter.Aggregate |
| — | 遗留 8 项 | REVIEW_REPORT §8（result 真实聚合/token 吊销等） |
| — | Python 引擎真实 LLM 调用 | 🔜 待 API key |

## Conventions

- IDs: ULID (`pkg/id`)
- Errors: `pkg/errors` sentinel + `Wrap()`，信封 `{code, message, details, request_id}`
- HTTP handlers: thin — DTO 校验 → service → `respondError`。v1 路由全部经 `v1.Services` 注入
- 套餐特性：`billing.DefaultPlans()[code]` + feature key，不硬编码
- TDD: 测试先行 RED → 最小实现 GREEN → 重构。禁止先写实现
- Code review: 5 角度审查，报告留 REVIEW_REPORT.md
- 前端: 页面数据经 `web/src/api/*.ts` 统一 axios（401 自动 refresh），错误信封经 ApiErrorHandler；图表色 负面 #FF2442 / 中性 #9ca3af / 正面 #02b940
- 项目品牌：盘古舆情（README/前端/demo/docs 均用此名；Go module 名 yuging 保持内部标识不变）
- 部署：Ubuntu 24 + 已有 nginx + deploy.sh 幂等（[SKIP] 已有组件）；BOCHA_API_KEY 环境变量或 Admin Settings 在线配置
