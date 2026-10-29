# Resilient collection and delivery implementation contract

Authority: user approved `docs/planning/PRD_RESILIENT_COLLECTION_AND_REPORT_DELIVERY_2026-10-29.html` on 2026-10-30, including GitHub publication, deployment and actual acceptance. Older approval pauses are superseded. No local/server compilation or test suites; all RED/GREEN through hosted workflows.

## Shared wire fields

- New analysis inputs: `seed_urls: string[]` (default empty, max 10, public selected-source HTTP(S) only), `delivery_policy: best_available | full_only` (rollout default via `YUQING_RESILIENT_DELIVERY=1`; legacy tests without flag retain strict behavior).
- Analysis detail/result and report list: `delivery_level: full | limited | brief | empty` (empty string for legacy), `delivery_reasons: string[]`, `resume_from_run_id: string` when applicable.
- New runs keep existing lifecycle states. Terminal delivered artifacts use `completed` with an explicit delivery level, NOT failed disguised as success. `empty` is a diagnostic receipt, never a successful full report. Full-only policy can retain failed state while preserving a brief artifact.
- Current-run fields and report snapshot fields must be immutable across rerun. Completed non-full runs atomically refund exactly the current consume; full retains consumption. Forward migration changes the refund integrity trigger ONLY for explicit non-full levels, preserving old normal completed-run prohibitions.
- `coverage.delivery_hint=brief` means query could not finish semantic admission; returned documents are raw usable materials with `semantic_classification=uncertain`, NOT direct/context. Hard date/source/exclusion/safety checks still apply. Coverage includes `failure`, `discovery_channels[]`, `per_platform[]`, `candidate_audit[]`, original input and elapsed times.
- Collector entries: `{source, channel, status, discovered_count, body_count, summary_count, accepted_count, failed_count, reason}`. Channels: `deepseek_search`, `deepseek_fetch`, `bocha_search`, `local_discovery`, `seed`, `retained`. Individual candidates preserve discovery chain, text version/hash, fetched timestamp, original/provider/body dates, fetch attempts and semantic result. No credentials.

## Python query request configuration

- Extend existing authenticated `/search`: new inputs plus `resilient: bool=false`, `search_provider: deepseek|bocha`, `search_api_key`, `search_base_url` (default official DeepSeek), `fetch_provider`. Go injects current platform settings, never the browser.
- DeepSeek: independent raw HTTP POST `/v1/tools/search`, `/v1/tools/fetch`. Query <=128 Unicode INCLUDING operators, count <=50, need_content true; no Bocha include/freshness sent to DeepSeek. Actual-domain and Shanghai date checks independent of operators.
- Search-with-content result and Fetch Markdown remain versioned provider body; summary/snippet are summary, never source literal quotes. Fetch has no published/final URL field; do not fabricate metadata.
- Failures preserve useful materials in best_available mode. Missing model does not prevent public discovery/fetch. Basic unenhanced plan is marked as such, not fabricated semantic understanding. Failures in accounting/auth/cancellation never turn into free supplier calls.
- Independent discovery must operate without any supplier: public feeds/list pages/sitemaps/verified public search/seed URLs, selected platforms only. Reserve 30 of 100 candidate slots when available; reclaim unused slots. At most 60 discovery pages, 100 processed candidates,50 final,20 dynamic renders. URL/network protections remain.

## Tool billing bridge (platform + Python query)

- POST `/internal/v1/billing/tool-authorizations`, same loopback service authentication header as LLM bridge; body `{call_id,run_id,provider,operation,attempt,request_hash}`. provider=deepseek, operation=search|fetch; validate active current run/actor/entitlements. Response `{call_id,permit,expires_at,duplicate}`. No tokens involved.
- POST `/internal/v1/billing/tool-usage-events`, service token plus `X-Tool-Call-Permit`; body `{event_id,call_id,event_version:1,attempt,provider,operation,outcome,http_status,result_count,usage_status,provider_request_id}`. status `reported|unknown`; unknown request outcome never fabricated as unbilled. Outbox precedes spend; replay idempotent; cost only per verified CNY price version. Successful nonempty Search/Fetch has documented expected cost5000 micro-CNY, reconciled status separate from supplier invoice. Events remain separate from token ledger.
- Python wrapper in `engines/common/tool_usage.py`; platform endpoints/PG schema in platform ownership. Query tests use MockTransport; hosted Python→Go→PG contract tests must cover actual bridge.

## Analysis/report contracts

- `/analyze` receives `resilient: bool=false`; preserves verified successful independent components and material coverage when another is refused. Content refusal is terminal per request; no refusal-evading rewrites/provider cycling. Partial dimensions cannot silently present unread batch conclusions as complete.
- `/generate` adds `delivery_level`, `delivery_reasons`, `resilient`, `unverified_documents` (raw retained candidates), `delivery_policy`; response returns same grade/reasons plus existing report/content/material_coverage/warning.
- Explicit `brief|empty` rendering must require NO LLM key/calls and expose no inferred sentiment/risk. `limited` uses verified components, with actual denominators. Full writing failure may deterministically compile ALL verified complete components into full if citation/material accounting remains complete. Otherwise downgrade.
- Go owns authoritative grading/settlement; Python cannot upgrade unsafe/unread evidence. Go must have an independent escaped deterministic renderer for last-resort Python report unavailability. HTML/Word use saved immutable snapshot.
- Resumption uses new accepted run and normal credit policy. Reuse material versions and successful immutable analysis components only when same bounds/model/inputs remain valid; never revive an already refunded run or overwrite old snapshot. Authenticated resume endpoint and stale-run/cancel contracts required.

## Ownership

- Collector worker: `engines/query_engine/**`, new `engines/common/{discovery,deepseek_tools,tool_usage}.py`, query/collector tests. No common LLM, insight, report or Go edits.
- Platform worker: all `platform/**` including forward migration0031+, transports, configuration, internal tool bridge, resume, authoritative delivery/refunds/tests. Coordinate via this contract; don't edit Python/web.
- Delivery worker: `engines/insight_engine/**`, `engines/report_engine/**`, common `{llm_client,model_budget,materials}.py`, their Python tests. No query/Go/web edits.
- Root: web/**, hosted workflow/acceptance scripts, docs/integration, credential configuration and release operations. Workers never publish/merge/deploy or run local suites. Shared workspace: do not revert others' edits.
