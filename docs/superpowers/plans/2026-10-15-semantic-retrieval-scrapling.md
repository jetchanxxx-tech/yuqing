# Semantic Retrieval and Scrapling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace lexical all-keyword admission with an auditable, topic-aware retrieval pipeline that searches each selected platform, attempts bounded Scrapling body recovery, and admits only evidence-backed documents.

**Architecture:** The accepted run first stores an immutable research-intent snapshot. Python query performs per-platform Bocha retrieval, bounded Scrapling extraction, hard source/date checks, and a metered semantic-v2 admission call. Go validates and persists the versioned coverage contract before the existing two-direct-document report gate. The UI exposes platform counts, fetch status, semantic decisions, and truthful unprocessed/insufficient states.

**Tech Stack:** Python 3.11/FastAPI/pytest/Scrapling, Go 1.25/Gin/pgx/goose, PostgreSQL 15, React 19/TypeScript/Ant Design, GitHub-hosted Actions, systemd deployment.

**Spec:** `docs/planning/SEMANTIC_RETRIEVAL_SCRAPLING_DESIGN_2026-10-15.html`

## Global Constraints

- All compiler, build, dependency-preparation, Go test/vet, frontend build/tsc, and Playwright execution runs on GitHub-hosted runners only.
- Preserve tenant isolation, immutable accepted `run_id`, atomic credit consumption/refund, and the existing two distinct direct-URL minimum.
- Never treat a Bocha excerpt as a full article, video transcript, or comment; preserve source/date provenance and quote-to-document binding.
- New runs use `semantic-v2`; historical `lexical-v1` records remain readable and are never silently reclassified.
- Hard source, public-URL, exclusion-word, and date bounds cannot be overridden by the LLM.
- No custom prompt templates, open-ended search agents, model routing, caching, notification delivery, or automatic rerun of historical tasks in this release.
- Credentials remain in runtime secret stores only; never write keys, passwords, or tokens to source, fixtures, logs, plans, or reports.

## Review Focus

- A material mentioning only `iPhone18 Pro` is direct evidence for a task whose related keywords are `iPhone18`, `iPhone18pro`, `iPhone18max`; test the shared topic without requiring every token.
- A material about another brand that happens to contain `18`, `Pro`, or `Max` is irrelevant; test false-positive protection.
- A selected platform must receive its own Bocha request and quota; test single-source and mixed-source runs and assert source types from URLs.
- A valid candidate with a JavaScript shell must get one bounded Scrapling attempt; test clean-body fallback, browser absence, timeout, and no background fetch after cancellation.
- A date-conflicting or unparseable document must remain in candidate audit only; test it cannot enter direct/context material pools.
- A model response with an unknown ID, fabricated quote, duplicate decision, or invalid JSON must fail closed; test protocol rejection and preserved audit counts.
- `49 context + 2 direct` must retain both direct documents; test direct-first allocation and per-platform rotation.

## Ownership and Interfaces

| Workstream | Owner | Files owned | Contract produced |
|---|---|---|---|
| Research intent and query contract | Python/Go retrieval owner | `engines/query_engine/main.py`, `engines/common/retrieval_intent.py` (new), `platform/internal/engine/contract.go`, `platform/internal/engine/real.go` | `SearchRequest` carries `run_id`, immutable intent snapshot, selected platforms, date/exclusion bounds; response is `semantic-v2` coverage. |
| Scrapling extraction | Python retrieval owner | `engines/common/scraper.py`, new platform extractor module, `engines/requirements.txt`, `engines/tests/test_scraper.py` | `fetch_candidate(url, platform, deadline) -> FetchOutcome` with cleaned text, final URL, status/reason, truncation and date provenance. |
| Semantic admission | Python LLM owner | `engines/common/relevance.py` or new `engines/common/semantic_admission.py`, `engines/query_engine/main.py`, `engines/tests/test_relevance.py`, new admission tests | `SemanticDecision` has `document_id`, `classification`, `reason`, `matched_entities`, `evidence_spans`; hard-boundary failures never reach the model. |
| Usage and persistence | Go platform owner | `platform/internal/business/analysis/{pipeline.go,service.go,store_pg.go,documents_pg.go}`, next goose migration, `platform/internal/app/pipeline.go` | Durable intent/coverage/material audit tied to tenant + analysis + run; accepted docs only feed insight/report. |
| UI and report provenance | Web owner | `web/src/api/analyses.ts`, `web/src/pages/AnalysisNewPage.tsx`, `web/src/pages/AnalysisDetailPage.tsx`, report templates, Playwright specs | Shows system-understood topic, per-platform funnel, fetch/semantic status, actual time budget, and truthful insufficiency. |
| Release and operations | Release owner | `.github/workflows/build-release.yml`, `.github/workflows/postgresql-tests.yml`, `docs/ops/DEPLOYMENT_RUNBOOK.md`, evidence HTML | Exact-SHA hosted checks, artifact hashes, browser asset verification, migration/backup/health/rollback evidence. |

No owner may edit another workstream's files without a reviewed contract change. All changes land on `dev` in small commits and are merged to `prod` only through the reviewed PR gate.

## Tasks

### Task 1: Freeze the versioned contracts and fixtures

**Files:** Create `engines/tests/fixtures/semantic_admission.json`, `platform/internal/engine/semantic_contract_test.go`; modify `platform/internal/engine/contract.go`, `platform/internal/business/analysis/pipeline.go` tests only.

**Interfaces:** Define `AdmissionVersion = "semantic-v2"`; define typed coverage fields for per-platform candidate/body/accepted/unprocessed counts; define the decision enum `direct|context|irrelevant|uncertain`.

- [ ] Write RED tests for `iPhone18` family, synonym/related phrase, competitor false positive, source mismatch, invalid date, invalid decision ID, fabricated quote, and direct-first allocation.
- [ ] Run focused Python and Go tests on the hosted runner and record the expected failures. Local execution may inspect files only.
- [ ] Implement only the shared JSON structs/constants and fixture loader needed by later tasks; do not change runtime behavior yet.
- [ ] Re-run focused tests to verify contract serialization and exact enum validation pass.
- [ ] Commit `test: define semantic retrieval contracts`.

### Task 2: Persist the immutable research-intent snapshot

**Files:** Modify `platform/internal/business/analysis/service.go`, API DTO/handler, `platform/internal/business/analysis/store_pg.go`, `platform/internal/business/analysis/runs.go`, `web/src/api/analyses.ts`, `web/src/pages/AnalysisNewPage.tsx`; create the next migration after `0026_analysis_scenario_template.sql` and matching PG tests.

**Interfaces:** `CreateAnalysisRequest.ResearchQuestion string`; run snapshot fields include original keywords, normalized entities/relations, focus, bounded query expressions, model/version, and created timestamp. Snapshot is immutable after run acceptance.

- [ ] Add RED API, memory, PG reconstruction, tenant isolation, rerun-new-snapshot, and billing rollback tests.
- [ ] Add optional `research_question` validation without changing existing required fields; persist the snapshot only after the accepted run/credit reservation.
- [ ] Ensure old rows deserialize with an explicit `legacy/unknown` snapshot marker; never synthesize a semantic explanation.
- [ ] Add UI field and confirmation display; do not issue a preview LLM call before `run_id` exists.
- [ ] Run focused hosted Go/PG and frontend checks; commit `feat: persist research intent snapshots`.

### Task 3: Implement per-platform Bocha retrieval and fair quotas

**Files:** Modify `engines/query_engine/main.py`, `engines/common/scraper.py`, `engines/tests/test_query_search.py`, `engines/tests/test_mixed_source_search.py`; add provider request fixtures and platform quota tests.

**Interfaces:** `search_and_fetch` receives the immutable intent and returns platform-tagged candidates. Each selected platform gets at least one query; maximum 8 expressions per platform, 40 provider requests per task, 50 provider results per request, 100 unique candidate URLs per task, and 50 final admitted materials.

- [ ] Write RED tests asserting separate `include`/query calls for each selected platform, no first-source labeling, per-platform rotation, duplicate URL/hash handling, 429 one-time retry, 5xx bounded retry, and permanent 4xx fail-closed behavior.
- [ ] Implement a request scheduler with one provider request per second and at most two in flight; record `queried`, `failed`, `unprocessed`, and `candidate_truncated` per platform.
- [ ] Apply hard source classification from final redirected URL and reject private/loopback/metadata destinations before fetch.
- [ ] Preserve the existing Bocha freshness format for closed date windows and retain date conflict/unparseable candidates only in audit coverage.
- [ ] Run query HTTP entrypoint tests and commit `feat: retrieve selected platforms independently`.

### Task 4: Restore bounded Scrapling body recovery

**Files:** Modify `engines/common/scraper.py`, `engines/requirements.txt`; create `engines/common/platform_extractors.py`, offline HTML fixtures, and `engines/tests/test_scrapling_recovery.py`.

**Interfaces:** `fetch_candidate(url, source_type, deadline) -> FetchOutcome(content, final_url, fetch_status, fetch_reason, truncated, content_kind, published_at, source_published_at)`. Static timeout 15 seconds, dynamic timeout 25,000 ms, static concurrency 4, browser concurrency 1, per-task dynamic maximum 20 pages, 8 MiB response cap, 200,000 cleaned-character cap.

- [ ] Write RED fixture tests for article extraction, script/style/navigation removal, JavaScript shell recovery, platform-specific selectors, redirect/source revalidation, response/body caps, browser missing, timeout, and cancellation cleanup.
- [ ] Implement static-first extraction and a bounded dynamic fallback using the installed Scrapling API. Do not pass millisecond values to second-based APIs or use unsupported fetcher options.
- [ ] Return a truthful failure reason; if the body fails but the Bocha excerpt is adequate and hard bounds pass, retain it as `summary` content with `body_unavailable` provenance.
- [ ] Run Scrapling fixture tests on the hosted runner with the pinned Chromium artifact; commit `feat: recover source bodies with bounded scrapling`.

### Task 5: Replace lexical admission with semantic-v2 evidence decisions

**Files:** Modify `engines/common/relevance.py`, `engines/query_engine/main.py`, `engines/common/llm_client.py`; create `engines/common/semantic_admission.py`, `engines/tests/test_semantic_admission.py`.

**Interfaces:** `classify_materials(intent, materials, usage_context) -> list[SemanticDecision]`; model sees the topic snapshot and cleaned body/adequate excerpt, never untrusted instructions from the material. Every decision binds to one input ID and verbatim evidence spans.

- [ ] Write RED tests using an independent hand-labeled set: minimum 30 direct, 20 context, 30 irrelevant, 20 uncertain; include model-output protocol attacks and truncated/timeout responses.
- [ ] Implement hard-boundary checks before the model, metered `query/relevance` calls with accepted `run_id`, explicit output budget 16,384 and one bounded 32,768 retry only for a confirmed truncation within remaining deadline.
- [ ] Validate exact IDs, one decision per material, evidence substring/offset, source/date eligibility, and no fabricated quote; malformed or incomplete output is `uncertain`/failed admission, never automatic pass.
- [ ] Partition direct/context pools and enforce at least two distinct direct URLs before full insight/report. Preserve all audit reasons and unprocessed counts.
- [ ] Run the hand-labeled audit; release target is direct precision >=95%, recall >=90%, and zero source/date/evidence-boundary escapes. Commit `feat: add metered semantic evidence admission`.

### Task 6: Wire Go transport, pipeline, persistence and usage accounting

**Files:** Modify `platform/internal/engine/{contract.go,real.go}.go`, `platform/internal/app/pipeline.go`, `platform/internal/business/analysis/{pipeline.go,service.go,store_pg.go,documents_pg.go}` and add Go unit/PG concurrency tests plus migration rollback/reconstruction tests.

**Interfaces:** Go accepts only `semantic-v2` for new runs; `SearchWithCoverage` carries `run_id` and intent, validates counts and decision IDs, persists coverage before failure, and stores only admitted direct/context documents for downstream analysis.

- [ ] Write RED transport tests for request propagation, unsupported-version rejection, nil/non-null arrays, count mismatch, and source/date fields; write RED pipeline tests for 0/1/2 direct documents, context-only, mixed platform, cancellation, stale run, and refund.
- [ ] Implement typed transport validation and fail closed on protocol mismatch. Keep handlers thin and DI in `platform/internal/app/container.go` only.
- [ ] Persist audit coverage/material metadata in the next positive migration; add PG tests for concurrent runs, rollback, tenant isolation, restart/reconstruction, and old lexical rows.
- [ ] Connect query semantic calls to the existing durable usage outbox/authorization bridge; no provider call is allowed without an accepted run and durable pre-call intent.
- [ ] Run hosted Go race/vet/PG checks; commit `feat: enforce semantic retrieval contract in pipeline`.

### Task 7: Surface provenance and time truthfully in the UI/report

**Files:** Modify `web/src/api/analyses.ts`, `web/src/pages/AnalysisNewPage.tsx`, `web/src/pages/AnalysisDetailPage.tsx`, report engine/template; create or modify `web/e2e/semantic-retrieval.spec.ts`.

- [ ] Write RED Playwright tests for the topic snapshot, per-platform funnel, body/summary provenance, direct/context/uncertain labels, date exclusions, live stage/elapsed/deadline, and insufficient-evidence explanation.
- [ ] Implement API types as non-null arrays where possible; show unprocessed/failed separately from “not relevant”; never claim a platform was covered when it was not queried.
- [ ] Replace fixed “5–10 minutes”/“采集1–2分钟” text with actual stage progress and configured maximum; preserve refresh idempotence.
- [ ] Add report source rows with document ID, original/final URL, platform, date provenance, content kind, and quote location; context does not enter direct metrics.
- [ ] Run hosted frontend lint/build and Playwright; commit `feat: expose retrieval evidence provenance`.

### Task 8: Release, production smoke, and rollback evidence

**Files:** Modify workflows/runbook only if needed; create `docs/dev/SEMANTIC_RETRIEVAL_SCRAPLING_ACCEPTANCE_2026-10-15.html` and release evidence under `docs/dev/evidence/semantic-retrieval-<SHA>/`.

- [ ] Push the complete dev series and wait for the exact SHA `build-release.yml` jobs: Go race tests/vet/binaries, Python tests, frontend lint/build, PostgreSQL tests, and Playwright.
- [ ] Review the artifact manifest, external archive `.sha256`, `BUILD_COMMIT`, unpacked `SHA256SUMS`, and pinned browser asset hash. Missing artifacts or skipped checks block release.
- [ ] Create a reviewed `dev -> prod` PR; merge only after all required checks pass on the merge commit. Back up PG/source/config and preserve the prior release before deployment.
- [ ] On the server, verify/unpack the exact artifact, apply the embedded migration before starting services, restart only after schema validation, then run health checks and one dedicated real Bocha + selected-platform + real-model smoke task. Do not rerun the user’s historical task.
- [ ] Record actual platform counts, body/summary fallbacks, direct/context decisions, usage events, elapsed time, and report source IDs. If any gate fails, stop and restore the previous hashed release/config; never build on the server.
- [ ] Commit `docs: record semantic retrieval release evidence` only after the acceptance document is complete.

## CI and Deployment Gates

1. No local compile/build/test claim is valid. The authoritative result is the hosted workflow for the exact commit.
2. A release is blocked by any skipped PG contract, failed security/provenance test, missing browser artifact, hand-labeled semantic target miss, or unverified real-provider smoke.
3. Production deployment requires reviewed `dev -> prod`, successful exact-prod SHA checks, archive and unpacked manifest verification, migration rehearsal/backup, service health, and a bounded real smoke report.
4. Rollback means stop services, restore the prior verified source/binaries/web release and configuration, validate schema compatibility, restart, and retain the failed release for diagnosis. Do not reverse an applied migration destructively; restore DB only under the approved backup procedure.

## Self-Review Result

The plan covers every design section: intent snapshot (Task 2), platform retrieval and quotas (Task 3), Scrapling limits (Task 4), semantic admission and evidence (Task 5), budgets/usage (Tasks 5-6), persistence and source provenance (Tasks 2, 6-7), UI truthfulness (Task 7), and hosted release/rollback (Task 8). The only deliberate non-goals are the custom-template and third-stage optimizations explicitly deferred by the user.
