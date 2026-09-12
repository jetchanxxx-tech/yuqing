# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**盘古舆情** — AI 原生 SaaS 舆情监测平台，面向中小企业与个人品牌。以 Scrapling 自适应爬虫 + Bocha AI 搜索实现真实数据采集，用多 Agent 辩论协作将"监测"升级为"研判"。

参考开源项目 BettaFish（GPL-2.0）架构灵感，从零构建平台层（Go），引擎层独立重写（Python，规避 GPL 传染）。爬虫层集成 Scrapling（BSD-3-Clause）。

> ⚠️ **仓库是公开的（github.com/jetchanxxx-tech/yuqing）** —— 绝不提交任何凭据。详见「凭据与安全」。

## Tech Stack

- **Platform**: Go 1.25, Gin, pgx v5, goose migrations, golang-jwt v5, argon2id
- **Engines**: Python 3.11+, FastAPI × 5 (query/media/insight/report/forum), Scrapling (爬虫)
- **Frontend**: React 19 + TypeScript + Vite 8 + Ant Design 5（含 v5-patch-for-react-19）+ ECharts 5（自写按需封装）+ TanStack Query + React Router 7 + Zustand
- **Database**: PostgreSQL 15（database-per-tenant 隔离；MVP 阶段为内存 store，见下）
- **Cache**: Redis 7
- **Search**: Bocha AI API（OpenAI 兼容）
- **Deploy**: Go 交叉编译二进制 + systemd，无容器。Nginx 反代 + 静态文件。

## Commands

```bash
# ── Go (platform/) ────────────────────────────────────────
cd platform
make test                            # go test ./... -count=1 -race （353 用例 / 20 包）
go test ./... -count=1               # 不带 -race 的快速跑
go test -run TestRegister ./internal/platform/auth/   # 单个测试
go test ./internal/api/v1/ -count=1  # 单个包（契约测试）
make test-cover                      # 覆盖率
make build                           # 交叉编译 linux/amd64 → bin/{yuging-server,yuging-worker,yuging-cli}
make lint                            # = go vet ./...
go run ./cmd/server                  # 开发模式 (:8080) — 内存 store，无需 PG/Redis

# ── 前端 (web/) ───────────────────────────────────────────
cd web
npm ci && npm run build              # = tsc -b && vite build → dist/
npm run dev                          # Vite dev :5173（proxy /api → 127.0.0.1:8080）
npm run lint                         # oxlint

# ── E2E (web/e2e/) ────────────────────────────────────────
npx playwright test --config e2e/playwright.config.ts     # 本地 dev server 冒烟
npx playwright test --config e2e/production.config.ts     # 打生产站点（23 用例）
# 生产套件凭据**只能**来自环境变量，缺失时 spec 直接抛错：
#   E2E_EMAIL / E2E_PASSWORD / E2E_BASE_URL（默认 https://yuqing.pangu-cloud.com）
#   E2E_BOCHA_KEY 可选，用于 5.3 数据源写入验证

# ── Python engines (engines/) ─────────────────────────────
cd engines
python3 -m venv venv && source venv/bin/activate
pip install -r requirements.txt      # 含 scrapling[fetchers]
scrapling install --chromium         # 首次 ~150MB
python3 -m pytest tests/ -v          # 10 用例（Scrapling 封装）
# 开发：在具体引擎目录下 `uvicorn main:app --port 8000`
# 生产（systemd）：WorkingDirectory 与 PYTHONPATH 均为 /opt/pangu-source，
#   ExecStart=/opt/yuging/engines/venv/bin/uvicorn engines.<name>_engine.main:app
# 端口：8000=query 8001=media 8002=insight 8003=report 8004=forum
BOCHA_API_KEY=sk-xxx uvicorn main:app --port 8000

# ── 演示与部署 ────────────────────────────────────────────
双击 demo/index.html                  # "雅阁后排" 7 步产品演示（零依赖，离线可用）
sudo YUGING_DOMAIN=yuqing.pangu-cloud.com bash scripts/deploy.sh   # 幂等部署
```

## Architecture: Modular Monolith

`platform/cmd/` 下三个 Go 二进制，由 `platform/Makefile` 交叉编译：

| Binary | Role |
|--------|------|
| `yuging-server` | HTTP API + **分析管线（同进程）** |
| `yuging-worker` | Queue 消费者（当前无分析任务可消费，见「分析管线」） |
| `yuging-cli` | 运维 CLI |

### 组合根（DI 唯一装配点）

`platform/internal/app/container.go` 的 `Build(cfg, logger)` 是全部服务实现的唯一装配处：

- 共享 tenant store：`auth.NewSharedTenantStore` 让注册与 admin 读同一份数据
- Report 套餐 gating 双闭包注入：`planCodeFor` + `planProvider`，未知租户 fail-closed
- Alert EmailSender 为 nil → 静默丢弃（SMTP 未接）
- Platform Settings：`settings.MemoryStore` 从环境变量种子（`BOCHA_API_KEY`），admin 可在线覆盖
- 引导管理员：`YUGING_BOOTSTRAP_ADMIN_EMAIL` 指定的邮箱注册即得 `platform_admin`
- **管线仅当 `cfg.Engines.Query.URL != ""` 时启动** —— 测试用的精简配置不含该地址，强行启动会让管线发真实 HTTP 并快速失败，破坏断言 `queued` 的用例

### 分析管线（关键）

`platform/internal/business/analysis/pipeline.go` + `platform/internal/app/pipeline.go`。

```
POST /analyses → store 写入(state=queued) → queue 发布 TaskMessage{analysis_id, tenant_id}
   → 同进程 Pipeline.Handle：
        acquiring_budget(10) → fetching(25) → analyzing(60) → generating_report(85) → completed(100)
        任一步失败 → failed（超时归为错误码 "timeout"）
```

**为什么必须在 server 进程内**：内存 store 与内存 queue 都是**进程私有**的。独立 worker 进程既收不到 server 发布的消息，也看不到 server 创建的任务 —— 这正是「任务永久停在 queued」的根因（实测 worker 收到任务数为 0）。接入 PostgreSQL store + 远程队列后可搬回独立 worker，管线自身无需改动。

其他约定：
- `TaskMessage` 必须携带 `tenant_id`：store 按租户分桶，仅凭 analysis_id 无法定位。`DecodeTaskMessage` 兼容历史的裸 ID 格式，但调用方会拒绝无 tenant_id 的消息并记 warn
- 管线对已终态任务幂等（重复投递直接跳过）
- 进度百分比常量定义在 `pipeline.go`，前端据此渲染进度条
- `analyzing` 调 `InsightAnalyzer`（情感/话题/摘要），`generating_report` 调 `ReportGenerator`（HTML 报告）。两者**失败不致命**：任务仍 completed，warning 记录降级原因（采集结果不能因分析失败丢弃）。引擎未配置（URL 为空）时对应步骤跳过并记 warning
- 洞察/报告经 `app/pipeline.go` 的 adapter 适配（engine → analysis 接口），business 层不 import engine 包

## Layer Boundaries (critical)

```
internal/
├── platform/    ← 平台库 (tenants, users, billing, settings, apikey)
│   auth, tenant, user, usage, billing, settings, apikey
├── business/    ← 租户库 (analyses, documents, reports)
│   analysis, datasource, crawler, sentiment, report, dashboard, alert
├── engine/      ← Go 契约 + HTTP transports (real.go → Python /search)
├── api/         ← thin handlers: DTO 校验 → service → respondError 信封
│   middleware/  ← authn(JWT/ApiKey 双认证), tenant, RBAC, ratelimit, audit
└── pkg/         ← db, queue, llm, storage, search, cache, id, errors, observ
```

**Platform vs Business 双向禁止跨层 import**。已知例外仅 1 处：`business/report → platform/billing`（定价目录下沉 `pkg/` 是长期方案，当前用函数注入缓解，见 `platform/REVIEW_REPORT.md` §8.4）。

## Engine 实现状态（勿假设"引擎可用"）

Python 引擎**不是**同等完成度。改动前先确认：

| 引擎 | 端口 | 状态 |
|------|------|------|
| `query_engine` | 8000 | ✅ **真实实现** — Bocha 搜索 + Scrapling 抓取，实测收集 19 条真实中文文档 |
| `insight_engine` | 8002 | ✅ **真实实现** — DeepSeek 情感分析 + 话题聚类 + 研判摘要（2 次 LLM 调用：情感+话题、摘要） |
| `report_engine` | 8003 | ✅ **真实实现** — DeepSeek 研判 + HTML 模板渲染；LLM 失败降级为纯数据报告 |
| `forum_engine` | 8004 | ⚠️ Mock — 预置"雅阁后排"4 Agent × 3 轮辩论 |
| `media_engine` | 8001 | ⚠️ Mock — 5 条预置多模态结果 |

`/analyses/:id/result` 返回真实 `summary`/`sentiments`(计数+明细)/`topics`/`report`(HTML)/`warning`（降级原因）。

## Key Design Patterns

### 数据采集链路（Scrapling + Bocha）

```
Go Pipeline (state=fetching)
  → engineFetcher → RealCrawlerEngine.Search()  [HTTP, bochaKeyFunc 注入]
  → Python query_engine /search
     1. Bocha API 搜索关键词 → URL 列表
     2. Scrapling 抓取正文 (Fetcher 静态 / DynamicFetcher JS / StealthyFetcher 反爬)
     3. adaptive=True 自适应选择器 + content_hash 去重
     4. 正文抓取失败 → 退回用 Bocha snippet 兜底，保证结果不丢
  → []Document 回传 Go → Service.AddDocuments() 存入 documentStore
```

- `engines/common/scraper.py` `PageScraper` — Scrapling 封装（错误降级、去重、source 路由）
- **Bocha 接口事实**（踩坑后确认）：端点 `https://api.bochaai.com/v1/web-search`（**不是** `/v1/ai/search`，那个返回 404）；响应结构 `data.webPages.value[]`，字段是 `name`/`url`/`snippet`（**不是** `title`）
- Bocha key 三级来源：环境变量 `BOCHA_API_KEY` → 平台 settings（`PUT /api/v1/admin/settings`，零重启生效）→ 请求参数。Admin UI 在「管理后台 → 数据源配置」
- 文档存在 `analysis.documentStore`（内存），`Documents()` 返回 `[]` 而非 nil —— 前端 `.map()` 遇 null 会崩

### 服务层（全部内存 store，TDD）

| 服务 | 核心职责 |
|------|---------|
| `auth.Service` | Register(User+Tenant+Member 三连+SetQuota 1M)/Login/Authenticate/Refresh |
| `tenant.Service` | Get/List/Suspend/Resume（状态机校验） |
| `analysis.Service` | Create/Get/List/Cancel/Rerun + `advance`/`markFailed`（管线写路径）；`Create` 发布 `TaskMessage` |
| `report.Service` | CreateFromAnalysis/DownloadURL — 套餐 format gating fail-closed |
| `dashboard.Service` | 实时计算 Overview/Trend（zero-value 数组防 ECharts null） |
| `alert.Service` | Create/Check（negPct ≥ threshold 触发 + sender.Send） |
| `apikey.Service` | CreateKey(pangu_+ULID, 只返回一次)/ValidateKey(fail-closed)/RevokeKey(跨租户 404) |
| `usage.Meter` | Record/BudgetStatus/Aggregate（全租户聚合供 /admin/usage） |

⚠️ 所有 store 均为**内存实现**，进程重启即丢失全部账号、任务与文档。PostgreSQL 迁移 SQL 已备但未接线。

### LLM Metered Provider (API resale)

每次 LLM 调用：预算检查（hard_cap fail-closed）→ Chat → `Meter.Record`。成本**按 token 类别分档**（prompt × 输入价 + completion × 输出价），双字段 `cost_micro_cny` / `billed_micro_cny`。Free/Pro = hard_cap，Business = overage，Enterprise = 无 cap。`FakeProvider` 返回固定"雅阁后排"摘要（token 绕过，联调前勿动）。

### Analysis State Machine

```
draft → queued → acquiring_budget → fetching → analyzing → generating_report → completed
any active state → failed | canceled
```

`Transition` 是唯一写路径；终态不可跳转（`ErrConflict`）。

### SSE 实时推送

`GET /analyses/:id/events` — text/event-stream，`SSEPollInterval`（默认 1s）轮询状态机，state/progress 变化发 `event: progress`，终态发 `event: final` 后关闭。客户端断开（ctx.Done）停止。多实例部署时换 Redis pub/sub。nginx 需 `X-Accel-Buffering: no`。

### 认证双通道

`middleware.AuthAny`：`pangu_` 前缀分流到 ApiKey 验证（映射最小权限 `api_service` 角色），其余走 JWT。API Key 存 SHA-256 哈希，创建时仅返回一次原始值。

### 测试分层

| 层 | 位置 | 规模 |
|----|------|------|
| 单元 | 各包 `*_test.go` | TDD，表驱动 |
| 契约 | `api/v1/contract_test.go`、`result_test.go` | 锁 endpoint 响应结构 + RBAC 403 + gap registry |
| 集成 | `test/integration/` | 全链路/计费/隔离/并发 |
| Python | `engines/tests/test_scraper.py` | 10 用例：Scrapling 去重/降级/路由 |
| E2E | `web/e2e/production.spec.ts` | 23 用例，打生产站点 |

## MVP Scope

| # | Feature | Status |
|---|---------|--------|
| F01-F10 | 认证/任务/采集/看板/摘要/报告/计费/保留/后台/告警 | ✅ |
| F11 | 多 Agent 辩论 (ForumEngine) | ⚠️ Python mock（4Agent×3轮） |
| F12 | 多模态 (MediaEngine) | ⚠️ Python mock（5 预置结果） |
| F13 | SSE 实时推送 | ✅ 轮询实现 |
| F14 | API Key 管理 | ✅ pangu_ 格式 |
| F15 | /admin/usage 聚合 | ✅ Meter.Aggregate |
| F16 | 数据源在线配置（Admin UI） | ✅ Bocha + DeepSeek |
| F17 | 情感分析 / 话题聚类 / 报告生成 | ✅ DeepSeek 真实调用，管线全链路已接入 |
| — | PostgreSQL store（持久化） | ❌ 内存 store，重启即丢数据 |
| — | LLM 调用平台侧计量（MeteredProvider 接真实调用） | ❌ Python 引擎直连 DeepSeek，Go 侧计量未接线 |

## 部署与运维

```bash
sudo YUGING_DOMAIN=<域名> bash scripts/deploy.sh     # 幂等：已装组件 [SKIP]，已有配置不覆盖
```

- `scripts/deploy.sh` — Ubuntu 24.04，无 Docker。检测并跳过已装的 nginx/postgresql/redis/go/node；显式 `-o bin/yuging-*` 生成二进制（`go build -o dir/ ./cmd/...` 会产出 `server`/`worker`/`cli`，与 unit 名不匹配导致服务静默启动失败）
- `scripts/nginx-ssl.conf` / `nginx-http.conf` — 有域名走 HTTPS，否则 HTTP-only。SSL 版含 `/.well-known/acme-challenge/` 直通location。nginx 1.24 用 `listen 443 ssl http2`（参数形式，`http2 on;` 指令 1.25 才有）
- `scripts/systemd/*.service` — 7 个 unit：`yuging-{server,worker,query,media,insight,report,forum}`
- **证书**：acme.sh（Gitee 镜像安装，get.acme.sh 境内不通）。其 cron 每日检查，到期前 30 天自动续期并 reload nginx
- 引导管理员经 `yuging-server.service.d/bootstrap-admin.conf` drop-in 注入，保证重建环境可复现
- 运维手册 `docs/OPS_MANUAL.html`（架构/服务清单、服务管理、日志排查、配置变更、证书管理、备份恢复、巡检、故障排查）；部署日志 `docs/DEPLOYMENT_LOG.html`（15 个问题 + 根因 + 4 条教训）

## 凭据与安全

- **仓库公开**，历史上有过 Bocha key 泄露（`docs/DEPLOYMENT.html`，commit `00cf3d1` 之前）—— 已替换为占位符，但**该 key 需在 open.bochaai.com 轮换**
- 真实凭据只存在于 `credentials.local.md`（**已 gitignore，勿提交、勿读取进上下文再写出**）
- E2E spec 凭据只从 `E2E_EMAIL`/`E2E_PASSWORD` 读，**禁止**加硬编码兜底默认值
- 提交前自查：`git diff --cached` 里不得出现 `sk-`、密码、token

## Conventions

- IDs: ULID (`pkg/id`)
- Errors: `pkg/errors` sentinel + `Wrap()`，信封 `{code, message, details, request_id}`
- HTTP handlers: thin — DTO 校验 → service → `respondError`。v1 路由全部经 `v1.Services` 注入
- 套餐特性：`billing.DefaultPlans()[code]` + feature key，不硬编码
- TDD: 先写失败测试 RED → 最小实现 GREEN → 重构。**禁止先写实现再补测试**
- Code review: 5 角度审查，报告留 `platform/REVIEW_REPORT.md`（§5/§8 等章节记录已知遗留项）
- 前端: 页面数据经 `web/src/api/*.ts` 统一 axios（401 自动 refresh），错误信封经 ApiErrorHandler；图表色 负面 `#FF2442` / 中性 `#9ca3af` / 正面 `#02b940`
- 项目品牌：**盘古舆情**（README/前端/demo/docs 均用此名；Go module 名 `yuging` 保持内部标识不变）
- 面向人交付的文档用 **HTML**（`docs/*.html`），不用 Markdown —— 用户明确要求过
- 提交前把关：`make test` 全绿 + `npm run build` 通过 + `go vet` 无警告
