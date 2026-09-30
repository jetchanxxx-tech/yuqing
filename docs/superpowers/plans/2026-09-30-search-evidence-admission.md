# Search Evidence Admission Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent unrelated or unusable search results from entering analyses while exposing evidence and truthful insufficiency outcomes.

**Architecture:** Python query owns versioned admission and coverage; Go validates the protocol and persists accepted documents plus coverage, then guards analysis/report creation. React shows the actual funnel and source capability truth.

**Tech Stack:** Python FastAPI/pytest, Go/Gin/pgx/goose, PostgreSQL, React/TypeScript/Playwright.

**Spec:** `docs/superpowers/specs/2026-09-30-search-evidence-admission.md`

## Global Constraints

- Keep tenant isolation, terminal idempotency, and refund-on-failure semantics.
- Do not treat a search excerpt as an article, video comment, or verifiable quote.
- Existing analyses retain unknown admission provenance; do not bulk-rerun.
- No secret, destructive migration, or production build.

---

### Task 1: Relevance and quality admission
**Files:** Create `engines/common/relevance.py`, `engines/tests/test_relevance.py`; modify `engines/common/scraper.py`, `engines/query_engine/main.py`, their tests.
**Interfaces:** `admit(keyword,title,excerpt)->(bool,reason)` and query `coverage.admission_version` / count fields.
- [ ] Write GS8/GL8/FP8/Gundam/SPA, Chinese phrase, whitespace, multi-keyword failing fixtures; run pytest red.
- [ ] Implement normalized quality and identifier/phrase matching; apply before global result cap; run focused pytest green.
- [ ] Bound Bocha candidate count to documented 1..50; verify the HTTP request and truncation accounting; rerun query tests.

### Task 2: Go persistence and insufficiency
**Files:** Modify `platform/internal/engine/real.go`, `platform/internal/app/pipeline.go`, `platform/internal/business/analysis/pipeline.go`, `service.go`, `store_pg.go`, API result handler; add `platform/migrations/platform/0013_analysis_admission_coverage.sql`, Go unit/PG contract tests.
**Interfaces:** Search coverage typed/versioned; analysis `retrieval_coverage` JSONB, `insufficient_relevant_evidence` failure code.
- [ ] Write failing transport, 0/1/2-document, refund, no-insight, restart/tenant, unknown historical coverage tests; run Go red.
- [ ] Add compatible migration + typed transport, validate admission version and counters; fail closed on protocol errors.
- [ ] Persist coverage before insufficiency failure; only accepted documents reach SaveDocuments/insight; run Go green and disposable PG migration tests.

### Task 3: Product display and topic integrity
**Files:** Modify `web/src/pages/AnalysisNewPage.tsx`, `web/src/pages/AnalysisDetailPage.tsx`, `web/src/api/analyses.ts`, `web/e2e/analysis-entry.spec.ts`; Go topic contract and tests where feasible.
- [ ] Write failing UI tests for accurate label, 8-keyword cap, no unsupported supplementary OR tags and coverage/error display.
- [ ] Render searchable public-web label and funnel from persisted coverage; distinguish candidate/excerpt/accepted/unknown legacy.
- [ ] Test topic document-ID grounding and count; run frontend build and Playwright.

### Task 4: Integration and release gate
- [ ] Run `go test ./... -count=1`, `go vet ./...`, `python -m pytest tests/ -v`, `npm run build`, `npm run lint`, and Playwright; distinguish inherited vet warnings and skipped PG tests.
- [ ] Verify disposable PG migrations + store contracts, real Bocha smoke and labeled false-positive/false-negative audit.
- [ ] Review scoped diff, document remaining risks, then separately authorize commit/PR/deploy with live diff and rollback backup.
