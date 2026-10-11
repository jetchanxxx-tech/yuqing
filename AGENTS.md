# Repository Guidelines

## Rule Precedence & Reading

Current user instructions and newer confirmed decisions override historical guidance. Read `CLAUDE.md` and relevant plans/contracts/reviews before editing. Verify implementation/deployment claims against source and hosted evidence; superseded local-build, unlimited-account, nonrevoking-password and incomplete-insight guidance must not override this policy.

## Structure & Coding Style

`platform/` contains Go entrypoints, services, adapters, migrations and adjacent `*_test.go`/integration tests. `engines/` contains Python/FastAPI and `tests/`; `web/` contains React/TypeScript, `src/api/`, assets and Playwright `e2e/`. `scripts/` and `.github/workflows/` handle operations/verification.

Use LF, Go `gofmt`/tabs, TypeScript two-space and Python four-space indentation; PascalCase components, camelCase TypeScript and snake_case Python. Wire interfaces/adapters in `platform/internal/app/container.go`; keep handlers thin, errors typed (`{code,message,details,request_id}`), IDs ULID, arrays non-null and catalog checks centralized. Route UI requests through `web/src/api/`. Preserve 盘古舆情 branding/internal `yuqing` identifiers.

## Build & Test Commands

**Compilation, bundling, compiler checks and compiling dependency preparation run only on GitHub-hosted runners through `.github/workflows/build-release.yml` and `.github/workflows/postgresql-tests.yml`.** Never locally/on development or deployment hosts/self-hosted runners: `go build/test/vet/run`, `make build/test`, `npm run build/dev`, `tsc`, Vite builds/prebundling or equivalent wrappers.

Runner commands: `platform/`: `make test` (race), `make lint` (vet), `make build` (binaries); `web/`: `npm ci`, `npm run lint` (Oxlint), `npm run build`; workflows also run pytest/Playwright.

Local work permits editing/review, lightweight syntax checks, Git and artifact download/checksum/upload. Deployment hosts only verify/unpack, configure, run prebuilt migrations, restart and health-check; missing artifacts must fail.

## Testing & PRs

Use RED→GREEN, table-driven `Test…`/`*_test.go`, pytest `test_*` and Playwright `*.spec.ts`. New PG behavior needs real concurrency/rollback/reconstruction contracts. Test actual entrypoints; skips/mocks alone cannot prove acceptance. Define observable outcomes; unresolved blockers prevent release.

Use `feat:`, `fix:`, `test:`, `docs:` commits. PRs describe behavior, reference tasks and provide exact-SHA hosted results plus relevant UI evidence. Review diffs for secrets.

## Branches & Release

Develop on `dev`; reviewed `dev → prod` PRs establish production. Preserve `main`/`beta/*`. Failed/incomplete CI requires new-commit fixes, never local compilation. Production requires the successful exact-commit `prod` run: download `yuqing-release-<SHA>`, verify archive `.sha256`, `BUILD_COMMIT` and unpacked `SHA256SUMS`. Preserve rollback artifacts; install hashed assets before atomically replacing `index.html`.

## Approved Scope & Business Rules

Execute authorized tasks sequentially using `docs/planning/ADMIN_USER_CENTER_DEVELOPMENT_PLAN.html`; record evidence in `docs/dev/ADMIN_USER_CENTER_DELIVERY_STATUS.html`. Separate account and tenant/subscription management. Device management/invitations are deferred; password/status security remains required. Never label proposals or undeployed changes as deployed.

Closure requires seven-day withdrawal, unsettled-order blockers and explicit sole-member-team confirmation. Preserve others’ assets, financial ledgers with personal-data anonymization and existing file retention; unresolved notification attempts remain blockers.

Only verified immutable UID of the initial `admin@pangu.com` account is billing-exempt—not token email, role or tenant. Others follow selected plans; everyone records actual usage/provider cost. Preserve atomic billing/paired refunds; never reinstate the global PG beta bypass or fabricate financial facts.

## Security & Documentation

Never commit credentials/tokens or load `credentials.local.md` into context. Notifications use sandbox tests; real email/SMS needs separately authorized recipients. Supplier delivery/mobile-payment confirmation remains unverified until separately tested. Human-facing deliverables use HTML; agent instructions use Markdown.

Consult `docs/dev/ADMIN_ACCOUNT_API_CONTRACT.html`, `docs/dev/reviews/`, `docs/ops/DEPLOYMENT_RUNBOOK.md`, `docs/dev/ACCOUNT_CLOSURE_OPERATIONS.md`, `docs/dev/LLM_USAGE_BRIDGE_OPERATIONS.md`; UI/manual-monitoring changes also follow `design/design-spec.md` and `docs/planning/v0.2.4/PRD-dashboard-manual-monitoring.md`. Saved drafts do not imply active scheduling/delivery.
