# Repository Guidelines

## Instructions & Current Scope

Latest user decisions override historical guidance. Read `CLAUDE.md` and relevant plans/contracts/reviews before editing.

The user approved docs/planning/PRD_RESILIENT_COLLECTION_AND_REPORT_DELIVERY_2026-10-29.html on 2026-10-30 and authorized implementation, GitHub publication, production deployment and actual acceptance tests. Follow its scope and evidence requirements. Custom prompts remain deferred. External Search/Fetch cannot replace independent discovery plus local Scrapling fetching.

## Project Structure & Style

`platform/`: Go entrypoints, adapters, migrations and tests. `engines/`: Python/FastAPI and `tests/`. `web/`: React/TypeScript, assets and Playwright `e2e/`. `scripts/` and `.github/workflows/`: operations/verification.

Use LF, `gofmt`/tabs, TypeScript two-space, Python four-space indentation; PascalCase components, camelCase TypeScript, snake_case Python. Wire adapters in `platform/internal/app/container.go`; use thin handlers, typed errors (`{code,message,details,request_id}`), ULIDs, non-null arrays and centralized catalogs. UI requests use `web/src/api/`. Preserve 盘古舆情/internal `yuqing` identifiers.

## Build & Test Commands

**Builds, compiler checks, dependency preparation and suites run only on GitHub-hosted runners:** `.github/workflows/build-release.yml` and `.github/workflows/postgresql-tests.yml`. No local/deployment/self-hosted `go build/test/vet/run`, `make build/test`, `npm run build/dev`, `tsc`, Vite builds/prebundling or equivalent wrappers.

Hosted commands: `platform/`: `make test` (race), `make lint` (vet), `make build`; `web/`: `npm ci`, `npm run lint` (Oxlint), `npm run build`; pytest/Playwright suites.

Locally: edits, lightweight syntax checks, Git, artifact transfer/verification. Servers: unpack/configure, prebuilt migrations, restart/health-check; missing artifacts fail.

## Testing & Evidence

Use RED→GREEN: Go `Test…`/`*_test.go`, pytest `test_*`, Playwright `*.spec.ts`. PG changes require concurrency/rollback/reconstruction contracts. Mocks/skips alone cannot prove acceptance; blockers prevent release.

Report acceptance requires actual server tasks, provider responses, downloads, hashes and usage. Diagnose generic 503 errors from evidence. Preserve source/date/quote checks; distinguish proposals, tests and production results.

## Commits, PRs & Release

Use `feat:`, `fix:`, `test:`, `docs:`. Develop on `dev`; reviewed `dev → prod` PRs establish production. Preserve `main`/`beta/*`. PRs include behavior, tasks, exact-SHA hosted results and UI evidence. Inspect secrets; fix CI with new commits.

Deploy successful exact-commit `prod` artifact `yuqing-release-<SHA>`; verify archive `.sha256`, `BUILD_COMMIT`, unpacked `SHA256SUMS`. Preserve rollback artifacts; install hashed assets before atomically replacing `index.html`.

## Approved Scope & Business Rules

Account work follows `docs/planning/ADMIN_USER_CENTER_DEVELOPMENT_PLAN.html`; evidence: `docs/dev/ADMIN_USER_CENTER_DELIVERY_STATUS.html`. Separate accounts/tenants/subscriptions. Devices/invitations remain deferred; password/status security remains mandatory.

Closure requires seven-day withdrawal, unsettled-order blockers, sole-member-team confirmation and resolved notifications. Preserve others’ assets, anonymized financial ledgers and file-retention rules.

Only verified immutable initial `admin@pangu.com` UID is billing-exempt; others follow plans. Record actual usage/cost; unknown remains unknown/pending. Preserve atomic billing/paired refunds; never restore the global PG beta bypass.

## Security & Documentation

Reuse private connection records; never expose/commit credentials or read `credentials.local.md` into context. Sandbox notifications; real email/SMS requires authorized recipients. Supplier delivery/mobile payments require verification. Human deliverables: HTML; agent instructions: Markdown.

Consult `docs/dev/ADMIN_ACCOUNT_API_CONTRACT.html`, `docs/dev/reviews/`, `docs/ops/DEPLOYMENT_RUNBOOK.md`, `docs/dev/ACCOUNT_CLOSURE_OPERATIONS.md`, `docs/dev/LLM_USAGE_BRIDGE_OPERATIONS.md`; UI/manual monitoring also follows `design/design-spec.md` and `docs/planning/v0.2.4/PRD-dashboard-manual-monitoring.md`. Saved drafts do not imply active scheduling/delivery.
