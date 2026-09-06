# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

AI 原生 SaaS 舆情监测平台 — 面向中小企业与个人品牌的舆情监测与分析工具。用多 Agent 辩论协作将"监测"升级为"研判"。

参考开源项目 BettaFish（GPL-2.0）的架构灵感，从零构建平台层（Go），引擎层独立重写（Python，规避 GPL 传染）。

## Tech Stack

- **Platform**: Go 1.25, Gin, pgx v5, goose migrations, golang-jwt v5, argon2id
- **Engines**: Python 3.11, FastAPI × 5 (query/media/insight/report/forum) — 骨架阶段
- **Frontend**: React 19 + TypeScript + Vite 8 + Ant Design 5（含 v5-patch-for-react-19）+ ECharts 5（自写按需封装 `web/src/lib/echarts.ts`，非 echarts-for-react）+ TanStack Query + React Router 7
- **Database**: PostgreSQL 15 (database-per-tenant isolation)
- **Cache**: Redis 7
- **Deploy**: Go cross-compiled binaries + systemd, no containers. Nginx reverse proxy + static files.

## Commands

```bash
# Go (platform/)
cd platform
go build -o bin/ ./cmd/...           # 3 个二进制: server/worker/cli
go test ./... -count=1               # 254 tests, 19 packages
go test -run TestRegister ./internal/platform/auth/   # 单包单测试
go test ./test/integration/ -count=1 # 集成测试（全链路/计费/隔离/并发）
go vet ./...                         # lint
go run ./cmd/server                  # dev: api (:8080) — 内存 store，无需 DB

# 前端 (web/)
cd web
npm ci && npm run build              # = tsc -b && vite build → dist/
npm run dev                          # Vite dev (proxy /api → 127.0.0.1:8080)
npx tsc -b                           # 类型检查
npm run lint                         # oxlint

# Python engines (engines/{name}_engine/)
python -m venv venv && source venv/bin/activate
pip install -r requirements.txt
uvicorn main:app --port 8000

# 离线演示 (demo/)
双击 demo/index1.html                # "雅阁后排" 7 步产品演示（零依赖）
```

## Architecture: Modular Monolith

两个 Go 二进制从 `platform/cmd/` 编译。API 进程不持有任务状态 — 跨进程状态全部通过 `Queue` 接口流转。

| Binary | Role |
|--------|------|
| `yuging-server` | Stateless HTTP API — `app.Build(cfg)` 组合根注入服务图 → `api.NewRouter(cfg, logger, deps)` |
| `yuging-worker` | Queue consumers + cron（当前订阅 analysis.tasks / usage.events，处理管线待实现） |

### 组合根（DI 唯一装配点）

`platform/internal/app/container.go` 的 `Build(cfg)` 是全部服务实现的唯一装配处：

- 内存 store 共享：`auth.NewSharedTenantStore(authStore, tenantStore)` 让注册时创建的租户与 admin 列表/挂起读同一份数据（模拟 PG 模式的单一 platform tenants 表）
- Report 套餐 gating 通过两个闭包注入：`planCodeFor(tenantID)` + `planProvider(planCode)`，未知租户 fail-closed 按 free 档
- Alert 的 EmailSender 为 nil 时静默丢弃（MVP 无真实邮件）

## Layer Boundaries (critical)

```
internal/
├── platform/    ← only accesses the platform DB (tenants, users, billing)
│   auth, tenant, user, usage, billing
├── business/    ← only accesses tenant DBs via db.Manager.Tenant(ctx, id)
│   analysis, datasource, crawler, sentiment, report, dashboard, alert
├── engine/      ← Go-side contracts + HTTP transports to Python engines
├── api/         ← HTTP handlers (thin: DTO validation only, delegates to services)
│   middleware/  ← authn, tenant resolution, RBAC, rate limit, audit
└── pkg/         ← shared infrastructure
    db, queue, llm, storage, search, cache, id, errors, observ
```

**Platform vs Business enforcement**: platform packages never import business stores, business packages never import platform stores. 已知例外（REVIEW_REPORT.md §8）：`business/report` 对 `platform/billing` 的依赖已通过函数注入缓解，定价目录建议下沉 pkg/。

## Key Design Patterns

### 服务层已全部实现（内存 store，TDD）

每个服务 = interface + memory store + service，全部经 TDD 开发（254 测试）：

| 服务 | 核心职责 |
|------|---------|
| `auth.Service` | Register（User+Tenant+Member 三连 + SetQuota 1M）/Login/Authenticate/Refresh。Login 失败统一 ErrUnauthorized，email 小写归一化 |
| `tenant.Service` | Get/List/Suspend/Resume（状态机校验，非法转换报错） |
| `analysis.Service` | Create/Get/List/Cancel/Rerun + **Transition**（worker 推进状态机的唯一写路径） |
| `report.Service` | CreateFromAnalysis/DownloadURL — 套餐 format gating fail-closed |
| `dashboard.Service` | 从 analysisSvc.List 实时计算 Overview/Trend（zero-value 数组防前端 ECharts null 崩溃） |
| `alert.Service` | Create/Check（negPct ≥ threshold 触发 + sender.Send） |

### Fake 实现（LLM/爬虫 token 绕过 — 最后联调前勿动）

- `pkg/llm/fake.go` `FakeProvider`：返回固定"雅阁后排"摘要（Usage 420/180），可被 MeteredProvider 包裹走完整计量链路，零外部调用
- `engine/fake.go` `FakeCrawlerEngine`：Crawl 返回 nil

用户提供真实 token 后替换为 HTTP transport，业务代码零改动。

### LLM Metered Provider (API resale model)

每次 LLM 调用经过 `llm.MeteredProvider`：预算检查（hard_cap fail-closed）→ Chat → `Meter.Record`。成本**按 token 类别分档计价**（prompt × 输入单价 + completion × 输出单价），双字段：`cost_micro_cny`（平台成本）+ `billed_micro_cny`（用户计费）。

预算语义：Free/Pro = `hard_cap`，Business = `overage`，Enterprise = 无 cap。

### Analysis State Machine

```
draft → queued → acquiring_budget → fetching → analyzing → generating_report → completed
any active state → failed | canceled
```

`analysis.Service.Transition` 是状态推进的唯一写路径；终态不可再跳转（ErrConflict）。

### 测试分层

| 层 | 位置 | 内容 |
|----|------|------|
| 单元 | 各包 `*_test.go` | TDD 开发，表驱动 |
| 契约 | `api/v1/contract_test.go` | 40 用例，锁 endpoint 响应结构 + RBAC 403 |
| 集成 | `test/integration/` | 56 用例：全链路/计费/套餐gating/多租户隔离/并发 |
| E2E | `web/e2e/demo-smoke.spec.ts` | 已写未运行（@playwright/test 未安装） |

测试报告与遗留项见 `platform/TEST_REPORT.md`（§4 契约缺口历史）和 `platform/REVIEW_REPORT.md`（§8 遗留 10 项）。

## MVP Scope (P0)

| # | Feature | Status |
|---|---------|--------|
| F01 | 认证与多租户 | ✅ 完整（含 /auth/me、admin RBAC 守卫） |
| F02 | 监测任务管理 | ✅ CRUD + 状态机（Transition 导出） |
| F03 | 5源数据采集 | ✅ 契约 + FakeCrawler（真实爬虫待联调） |
| F04 | 实时数据看板 | ✅ 前端 ECharts + 后端实时计算 |
| F05 | 单 Agent LLM 摘要 | ✅ MeteredProvider + FakeProvider |
| F06 | 报告生成 | ✅ 套餐 gating + 4 格式 |
| F07 | 计费与用量计量 | ✅ 4 档套餐 + 分档计价 |
| F08 | 数据保留策略 | ✅ config |
| F09 | 管理后台 | ✅ 租户列表/挂起/恢复（RBAC 守卫） |
| F10 | 邮件告警 | ✅ 阈值规则 + MockSender |
| — | 遗留 10 项 | 见 REVIEW_REPORT.md §8（result 聚合/SSE/token 吊销等） |
| — | Python 引擎 + 论坛辩论 | 🔜 待 token 联调 |

## Conventions

- IDs: ULID (`pkg/id`)
- Errors: `pkg/errors` sentinel + `Wrap()`，信封 `{code, message, details, request_id}`
- HTTP handlers: thin — DTO 校验（400 语义含 email/password 规则）→ 调 service → `respondError` 映射信封。v1 路由全部经 `v1.Services` 注入，禁止 handler 内 new 服务
- 套餐特性：`billing.DefaultPlans()[code]` + feature key（如 `reports:pdf`），不硬编码
- TDD: 测试先行确认 RED → 最小实现 GREEN → 重构。禁止先写实现
- Code review: 5 角度（逐行/Go陷阱/跨文件/wrapper/约定），审查报告留 REVIEW_REPORT.md
- 前端: 页面数据经 `web/src/api/*.ts` 走统一 axios 实例（401 自动 refresh），错误信封经 `ApiErrorHandler` 全局处理；图表颜色 负面 #FF2442 / 中性 #9ca3af / 正面 #02b940
