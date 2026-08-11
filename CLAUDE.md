# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

AI 原生 SaaS 舆情监测平台 — 面向中小企业与个人品牌的舆情监测与分析工具。用多 Agent 辩论协作将"监测"升级为"研判"。

参考开源项目 BettaFish（GPL-2.0）的架构灵感，从零构建平台层（Go），引擎层独立重写（Python，规避 GPL 传染）。

## Tech Stack

- **Platform**: Go 1.22+, Gin, pgx v5, goose migrations, go-redis
- **Engines**: Python 3.11, FastAPI × 5 (query/media/insight/report/forum)
- **Frontend**: React 18, TypeScript, Vite, Ant Design 5, ECharts 5, TanStack Query, Zustand
- **Database**: PostgreSQL 15 (database-per-tenant isolation)
- **Cache**: Redis 7
- **Deploy**: Go cross-compiled binaries + systemd, no containers. Nginx reverse proxy + static files.

## Commands

```bash
# Go (platform/)
cd platform
go build -o bin/ ./cmd/...           # build server + worker + cli
go test ./...                         # all tests
go test -run TestAuth ./internal/platform/auth/
go vet ./...                          # lint
go run ./cmd/server                   # dev: api process (:8080)
go run ./cmd/worker                   # dev: worker process

# Frontend (web/)
cd web
npm ci && npm run build               # production build → dist/
npm run dev                           # Vite dev server

# Python engines (engines/{name}_engine/)
python -m venv venv && source venv/bin/activate
pip install -r requirements.txt
uvicorn main:app --port 8000          # dev per engine

# CLI admin
./bin/yuging-cli migrate platform     # run platform migrations
./bin/yuging-cli migrate --all-tenants
./bin/yuging-cli provision-tenant <name>
```

## Architecture: Modular Monolith

Two Go binaries from one codebase, compiled from `platform/cmd/`:

| Binary | Role |
|--------|------|
| `yuging-server` | Stateless HTTP API (auth, tenant, billing, REST handlers) |
| `yuging-worker` | Queue consumers + cron (analysis orchestration, metering rollups, invoice gen) |

API processes never hold job state — all cross-process state flows through the `Queue` interface. For MVP both run on the same machine, workers scale out by running more instances against a remote queue.

## Layer Boundaries (critical)

```
internal/
├── platform/    ← only accesses the platform DB (tenants, users, billing)
│   auth, tenant, user, usage, billing
├── business/    ← only accesses tenant DBs via db.Manager.Tenant(ctx, id)
│   analysis, datasource, crawler, sentiment, report, dashboard
├── engine/      ← Go-side contracts + HTTP transports to Python engines
├── api/         ← HTTP handlers (thin: DTO validation only, delegates to services)
│   middleware/  ← authn, tenant resolution, RBAC, rate limit, audit
└── pkg/         ← shared infrastructure
    db, queue, llm, storage, search, cache, id, errors, observ
```

**Platform vs Business enforcement**: platform packages never import business stores, business packages never import platform stores. This is a convention enforced at code review.

## Key Design Patterns

### Interface-first modularity
Every module exposes a Go interface. Concrete implementations are wired once in `internal/app/container.go` (hand-rolled DI). Extracting a module to a microservice = replacing one interface impl with an HTTP/gRPC client.

### Config-driven scale-out
All extension points (`queue.driver`, `storage.driver`, `db.readWriteSplit`, `search.driver`) are config toggles. MVP defaults all use in-process/local — zero external dependencies beyond PG + Redis. See `config.example.yaml`.

### Database-per-tenant
- Platform DB: `yuging_platform` (tenants, users, plans, billing, usage)
- Tenant DBs: `yuging_t_{ulid}` (analyses, documents, reports, dashboards)
- `db.Manager` resolves pools by tenant ID, lazily creates on first access, LRU-evicts at `maxPools` (50)
- Goose migrations embedded via `go:embed`; tenant migrations run automatically on `Provision()`

### LLM Metered Provider (API resale model)
Every LLM call passes through `llm.MeteredProvider`:
1. Fast budget check (Redis counters, ~1ms)
2. Chat → external API (OpenAI-compatible: DeepSeek, Kimi, Moonshot, etc.)
3. `Meter.Record(UsageEvent)` → Redis INCR + async queue → `usage_daily` rollup
4. Dual cost fields: `cost_micro_cny` (platform cost) + `billed_micro_cny` (user charge)

Budget semantics per plan tier: Free/Pro = `hard_cap`, Business = `overage` (billed at tier rate), Enterprise = no cap.

### Analysis State Machine
```
draft → queued → acquiring_budget → fetching → analyzing → generating_report → completed
any active state → failed | canceled
```
Sweeper cron compensates for memory-queue message loss by requeuing stale `running` tasks.

## MVP Scope (P0 — 11 items)

Authentication (JWT/RBAC) → Task management → 5-source collection → Dashboard → LLM summary → Reports (HTML/MD) → Billing & metering → Data retention → Admin panel → Email alerts.

**Explicitly deferred**: Multi-agent debate (P1 week 2), multimodal/PDF (P1), private deployment (P2).

## Conventions

- IDs: ULID (`pkg/id`), sortable, generated without coordination
- Errors: typed sentinel errors mapped to API envelope `{code, message, details, request_id}`
- Logging: `log/slog` JSON to stdout, `request_id` propagated via middleware
- Migrations: goose embedded, platform + tenant two sets. Adding a tenant column = one migration file + `cli migrate --all-tenants`
- HTTP handlers: thin — validate DTOs, call service, map errors to envelope. Business logic never lives in `api/v1/`
- Subscription features: evaluated via `plan.Features(planCode)` helper, never hard-coded in handlers
