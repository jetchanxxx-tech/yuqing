"""Authenticated accepted-run retrieval: shared topic → platforms → bodies → evidence."""
import asyncio
from collections import deque
from datetime import date, datetime
import hashlib
import hmac
import inspect
import os
import time
from urllib.parse import urlsplit, urlunsplit
from zoneinfo import ZoneInfo

from fastapi import FastAPI, Header, HTTPException, Request
from pydantic import BaseModel, Field
import httpx

from engines.common.llm_client import LLM_MODEL, build_client
from engines.common.relevance import usable_excerpt
from engines.common.retrieval_intent import plan_research_intent, validate_intent
from engines.common.scraper import (PageScraper, FetchOutcome, SOURCE_DOMAINS, BOCHA_API_KEY as DEFAULT_BOCHA_KEY,
    ProviderBudget, ProviderBudgetExhausted, _classify_by_domain, _hash_content, _is_public_http_url, _source_display_name)
from engines.common.semantic_admission import ADMISSION_VERSION, classify_materials, SemanticAdmissionIncomplete
from engines.common.usage_bridge import configuration, usage_lifespan

app = FastAPI(title="Query Engine", version="0.4.0", lifespan=usage_lifespan)
_scraper = None


def get_scraper():
    global _scraper
    if _scraper is None:
        _scraper = PageScraper()
    return _scraper


class SearchRequest(BaseModel):
    keywords: list[str] = Field(default_factory=list)
    sources: list[str] = Field(default_factory=lambda: ["news"])
    max_results: int = 50
    date_from: str = ""
    date_to: str = ""
    analysis_id: str = ""
    tenant_id: str = ""
    run_id: str = ""
    title: str = ""
    research_question: str = ""
    analysis_type: str = ""
    scenario_template_id: str = ""
    exclude_words: list[str] = Field(default_factory=list)
    research_intent: dict = Field(default_factory=dict)
    research_intent_version: str = ""
    # Only the trusted Go service may inject live provider configuration.
    bocha_api_key: str = ""
    llm_api_key: str = ""
    llm_base_url: str = ""
    llm_model: str = ""


class SearchResponse(BaseModel):
    documents: list[dict]
    total_count: int
    sources: list[dict]
    coverage: dict


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "query", "version": "0.4.0", "admission_version": ADMISSION_VERSION,
            "scrapling": get_scraper()._available, "bocha": bool(os.getenv("BOCHA_API_KEY") or DEFAULT_BOCHA_KEY)}


def _validate(req):
    normalized_keywords = list(dict.fromkeys(word.strip() for word in req.keywords if word.strip()))
    req.sources = list(dict.fromkeys(req.sources))
    if not req.run_id or not req.analysis_id:
        raise HTTPException(422, "An accepted analysis_id/run_id is required")
    if not 1 <= len(normalized_keywords) <= 8 or len(req.keywords) > 16 or any(len(word) > 300 for word in req.keywords) or req.max_results < 1:
        raise HTTPException(422, "One to eight nonempty keywords and positive max_results are required")
    if not req.sources or any(source not in {*SOURCE_DOMAINS, "news"} for source in req.sources):
        raise HTTPException(422, "Requested source is unavailable")
    if len(req.research_question) > 2000 or len(req.title) > 500:
        raise HTTPException(422, "Research context is too long")
    try:
        lower = date.fromisoformat(req.date_from) if req.date_from else None
        upper = date.fromisoformat(req.date_to) if req.date_to else None
    except ValueError as exc:
        raise HTTPException(422, "date_from/date_to must be ISO dates") from exc
    if lower and upper and lower > upper:
        raise HTTPException(422, "date_from must not exceed date_to")
    if not (req.bocha_api_key or os.getenv("BOCHA_API_KEY") or DEFAULT_BOCHA_KEY):
        raise HTTPException(503, "Bocha API key required; preset test URLs are not real sources")
    if not (req.llm_api_key or os.getenv("LLM_API_KEY") or os.getenv("DEEPSEEK_API_KEY")):
        raise HTTPException(503, "Semantic admission requires a configured model")


@app.post("/search")
async def search(req: SearchRequest, request: Request, x_internal_token: str = Header(default="")):
    secret = os.getenv("YUQING_BILLING_SERVICE_TOKEN", "")
    if not secret or not x_internal_token or not hmac.compare_digest(x_internal_token, secret):
        raise HTTPException(401, "Internal service authentication required")
    _validate(req)
    task = asyncio.create_task(_execute(req))

    async def disconnected():
        while not task.done():
            if await request.is_disconnected():
                task.cancel()
                return
            await asyncio.sleep(0.25)
    watcher = asyncio.create_task(disconnected())
    try:
        return await task
    finally:
        watcher.cancel()
        if not task.done():
            task.cancel()
        await asyncio.gather(watcher, return_exceptions=True)


async def persist_research_intent(req, intent, model):
    base, token = configuration()
    async with httpx.AsyncClient(timeout=15, trust_env=False) as client:
        response = await client.post(base + f"/internal/v1/analyses/{req.analysis_id}/research-intent",
            headers={"X-Billing-Service-Token": token}, json={"run_id": req.run_id, "tenant_id": req.tenant_id,
                "intent": intent, "version": ADMISSION_VERSION, "model": model})
        response.raise_for_status()
        body = response.json()
    if body.get("version") != ADMISSION_VERSION or not isinstance(body.get("intent"), dict):
        raise ValueError("research_snapshot_protocol")
    stored = validate_intent(body["intent"], req, model)
    if stored != intent:
        raise ValueError("research_snapshot_changed")
    return stored


async def persist_candidate_audit(req, coverage):
    base, token = configuration()
    async with httpx.AsyncClient(timeout=20, trust_env=False) as client:
        response = await client.post(base + f"/internal/v1/analyses/{req.analysis_id}/retrieval-audit",
            headers={"X-Billing-Service-Token": token}, json={"run_id": req.run_id, "tenant_id": req.tenant_id, "coverage": coverage})
        response.raise_for_status()


def _canonical_url(url):
    try:
        value = urlsplit(url)
        return urlunsplit((value.scheme.lower(), value.netloc.lower(), value.path or "/", value.query, ""))
    except ValueError:
        return str(url)


def _fair_groups(groups):
    queues = [deque(items) for items in groups if items]
    while queues:
        next_round = []
        for queue in queues:
            if queue:
                yield queue.popleft()
            if queue:
                next_round.append(queue)
        queues = next_round


def _fair_documents(rows, sources):
    return list(_fair_groups([[row for row in rows if row["source_type"] == source] for source in sources]))


def _day(value):
    try:
        if not value:
            return ""
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        if parsed.tzinfo is not None:
            parsed = parsed.astimezone(ZoneInfo("Asia/Shanghai"))
        return parsed.date().isoformat()
    except (ValueError, AttributeError):
        return ""


def _date_reason(row, req):
    values = [value for value in row.get("publication_candidates", []) if value]
    days = [_day(value) for value in values]
    if len(set(day for day in days if day)) > 1:
        return "date_conflict"
    if req.date_from or req.date_to:
        if not days or any(not day for day in days):
            return "date_unverifiable"
        if (req.date_from and days[0] < req.date_from) or (req.date_to and days[0] > req.date_to):
            return "date_out_of_range"
    return ""


def _coverage(coverage):
    rows = coverage["candidate_audit"]
    coverage["unusable_count"] = sum(row.get("reason") == "unusable_content" for row in rows)
    coverage["irrelevant_count"] = sum(row.get("semantic_classification") == "irrelevant" for row in rows)
    coverage["source_mismatch_count"] = sum(row.get("reason") in {"source_mismatch", "source_redirect_mismatch"} for row in rows)
    coverage["unverifiable_date_count"] = sum(row.get("reason") in {"date_unverifiable", "date_conflict"} for row in rows)
    coverage["out_of_range_date_count"] = sum(row.get("reason") == "date_out_of_range" for row in rows)
    coverage["excluded_word_count"] = sum(row.get("reason") == "excluded_word" for row in rows)
    coverage["duplicate_count"] = sum(row.get("reason") == "duplicate_content" for row in rows)
    coverage["scanned_count"] = sum(row.get("fetch_status") in {"fetched", "failed"} for row in rows)
    coverage["excluded_word_ratio"] = coverage["excluded_word_count"] / len(rows) if rows else 0.0
    accepted = [row for row in rows if row["status"] == "accepted"]
    coverage["accepted_count"] = len(accepted)
    coverage["direct_count"] = sum(row.get("semantic_classification") == "direct" for row in accepted)
    coverage["context_count"] = len(accepted) - coverage["direct_count"]
    coverage["candidate_truncated"] = any(row.get("reason") in {"candidate_budget", "result_limit", "body_deadline", "semantic_deadline"} for row in rows)
    coverage["truncated_count"] = sum(row.get("reason") == "result_limit" for row in rows)
    coverage["date_filter"] = ("partial" if coverage["unverifiable_date_count"] else "applied") if coverage["date_from"] or coverage["date_to"] else "not_requested"
    for platform in coverage["per_platform"]:
        source_rows = [row for row in rows if row.get("requested_source") == platform["source"]]
        queries = [row for row in coverage["per_keyword"] if row["source"] == platform["source"]]
        platform.update(
            queries=len(queries), provider_candidates=sum(row.get("provider_candidates", 0) for row in queries),
            processed_count=sum(row.get("fetch_status") in {"fetched", "failed"} for row in source_rows),
            body_count=sum(row.get("content_kind") == "body" for row in source_rows),
            summary_count=sum(row.get("content_kind") == "summary" and row.get("status") in {"accepted", "rejected", "semantic_pending"} for row in source_rows),
            direct_count=sum(row.get("semantic_classification") == "direct" and row["status"] == "accepted" for row in source_rows),
            context_count=sum(row.get("semantic_classification") == "context" and row["status"] == "accepted" for row in source_rows),
            rejected_count=sum(row["status"] in {"filtered", "rejected"} for row in source_rows),
            unprocessed_count=sum(row["status"] in {"unprocessed", "candidate", "semantic_pending", "semantic_partial"} for row in source_rows),
            failed_count=sum(row["status"] == "failed" for row in queries),
            fetch_failed_count=sum(row.get("fetch_status") == "failed" for row in source_rows),
            status="failed" if queries and all(row["status"] == "failed" for row in queries) else "partial" if any(row["status"] != "ok" for row in queries) or any(row["status"] == "unprocessed" for row in source_rows) else "queried" if queries else "unprocessed",
        )
    return coverage


async def _execute(req):
    loop = asyncio.get_running_loop()
    started = loop.time()
    deadline = started + 900
    limit = min(req.max_results, 50)
    coverage = {"admission_version": ADMISSION_VERSION, "research_intent": {}, "provider_candidates": 0,
        "unusable_count": 0, "irrelevant_count": 0, "source_mismatch_count": 0, "accepted_count": 0,
        "direct_count": 0, "context_count": 0, "candidate_truncated": False, "per_keyword": [],
        "per_platform": [{"source": source, "status": "unprocessed"} for source in req.sources], "candidate_audit": [],
        "date_from": req.date_from, "date_to": req.date_to, "requested_max_results": req.max_results,
        "result_limit": limit, "candidate_limit": 100, "keywords_searched": [], "failed_keywords": [],
        "keyword_search": "applied", "phase": "planning", "phase_elapsed_seconds": {},
        "filter_limitations": "Public webpage bodies or provider summary/snippet excerpts only; no video transcription or complete comments. Unprocessed material is not a negative relevance decision.",
        "warning": "Requested result limit capped at 50" if req.max_results > 50 else ""}
    scraper = get_scraper()
    by_url = {}
    raw = {}
    batches = {}
    provider_budget = ProviderBudget()
    model = req.llm_model or LLM_MODEL
    llm = build_client(req.llm_api_key, req.llm_base_url, timeout=900)

    async def checkpoint():
        coverage["elapsed_seconds"] = round(loop.time() - started, 3)
        _coverage(coverage)
        await persist_candidate_audit(req, coverage)

    try:
        async with asyncio.timeout_at(deadline - 30):
            if req.research_intent and req.research_intent_version == ADMISSION_VERSION:
                intent = validate_intent(req.research_intent, req, model)
            else:
                intent = await plan_research_intent(llm, model, req, deadline=min(deadline - 60, loop.time() + 120))
            intent = await persist_research_intent(req, intent, model)
            coverage["research_intent"] = intent
            coverage["phase_elapsed_seconds"]["planning"] = round(loop.time() - started, 3)
            coverage["phase"] = "searching"
            await checkpoint()
            network_start = loop.time()
            network_end = min(deadline - 60, network_start + 180)
            queries = intent["search_queries"][:8]
            freshness = req.date_from if req.date_from and req.date_from == req.date_to else f"{req.date_from}..{req.date_to}" if req.date_from and req.date_to else "noLimit"

            async def recall(source, query_index, expression):
                entry = {"keyword": expression, "source": source, "status": "unprocessed", "provider_candidates": 0}
                coverage["per_keyword"].append(entry)
                try:
                    found = await scraper._bocha_search(expression, req.bocha_api_key or os.getenv("BOCHA_API_KEY") or DEFAULT_BOCHA_KEY,
                        include="|".join(SOURCE_DOMAINS.get(source, ())), count=50, freshness=freshness,
                        deadline=network_end, budget=provider_budget, source=source)
                    batches[(source, query_index)] = []
                    entry.update(status="ok", provider_candidates=len(found))
                    coverage["provider_candidates"] += len(found)
                    for candidate in found:
                        url = _canonical_url(str(candidate.get("url") or ""))
                        if not url:
                            continue
                        source_type = _classify_by_domain(url)
                        if url not in by_url:
                            record = {"id": hashlib.sha256(url.encode()).hexdigest()[:16], "url": url, "final_url": url,
                                "source_type": source_type, "requested_source": source, "title": str(candidate.get("title") or "")[:1000],
                                "source_name": str(candidate.get("site_name") or _source_display_name(source_type)),
                                "content": "", "summary_excerpt": str(candidate.get("summary") or candidate.get("snippet") or "")[:2000],
                                "content_kind": "summary", "fetch_status": "not_attempted", "fetch_reason": "",
                                "published_at": str(candidate.get("published_at") or ""),
                                "date_sources": {"provider": str(candidate.get("published_at") or ""), "body": ""},
                                "publication_candidates": [str(candidate.get("published_at") or "")],
                                "status": "candidate", "reason": "", "semantic_classification": "", "semantic_reason": "",
                                "evidence_spans": [], "observed_spans": [], "truncated": False}
                            by_url[url] = record
                            raw[url] = candidate
                            coverage["candidate_audit"].append(record)
                        else:
                            record = by_url[url]
                            published = str(candidate.get("published_at") or "")
                            if published and published not in record["publication_candidates"]:
                                record["publication_candidates"].append(published)
                            if source_type == source:
                                record["requested_source"] = source
                                raw[url] = candidate
                        batches[(source, query_index)].append(record)
                except ProviderBudgetExhausted:
                    entry.update(reason="supplier_request_budget")
                except asyncio.CancelledError:
                    entry.update(reason="network_deadline")
                    raise
                except TimeoutError:
                    entry.update(reason="network_deadline")
                except Exception:
                    entry.update(status="failed", reason="provider_request_failed")

            tasks = [asyncio.create_task(recall(source, index, expression))
                     for index, expression in enumerate(queries) for source in req.sources]
            try:
                await asyncio.wait_for(asyncio.gather(*tasks), max(0.01, network_end-loop.time()))
            except TimeoutError:
                pass
            finally:
                for task in tasks:
                    if not task.done():
                        task.cancel()
                await asyncio.gather(*tasks, return_exceptions=True)
            coverage["provider_calls"] = provider_budget.calls
            for platform in coverage["per_platform"]:
                platform["provider_calls"] = provider_budget.per_source.get(platform["source"], 0)
            coverage["phase_elapsed_seconds"]["searching"] = round(loop.time()-network_start, 3)
            coverage["keywords_searched"] = list(dict.fromkeys(row["keyword"] for row in coverage["per_keyword"]))
            coverage["failed_keywords"] = list(dict.fromkeys(row["keyword"] for row in coverage["per_keyword"] if row["status"] == "failed"))
            coverage["keyword_search"] = "partial" if any(row["status"] != "ok" for row in coverage["per_keyword"]) else "applied"
            if not any(row["status"] == "ok" for row in coverage["per_keyword"]):
                await checkpoint()
                raise HTTPException(502, {"code": "search_provider_failed", "message": "All platform searches failed or exceeded budget", "coverage": coverage})
            selected = []
            seen = set()
            # First rotate platforms, then each platform's query expressions.
            source_queues = [[row for row in _fair_groups([batches.get((source, index), []) for index in range(len(queries))])]
                             for source in req.sources]
            for row in _fair_groups(source_queues):
                if row["url"] in seen:
                    continue
                seen.add(row["url"])
                if not _is_public_http_url(row["url"]):
                    row.update(status="filtered", reason="unsafe_url")
                elif row["source_type"] not in req.sources or row["source_type"] != row["requested_source"]:
                    row.update(status="filtered", reason="source_mismatch")
                elif len(selected) < 100:
                    selected.append(row)
                else:
                    row.update(status="unprocessed", reason="candidate_budget")
            await checkpoint()
            await persist_research_intent(req, intent, model)
            coverage["phase"] = "fetching_bodies"
            body_start = loop.time()
            body_end = min(deadline - 60, body_start + 300)
            dynamic_count = 0

            async def fetch(row):
                nonlocal dynamic_count
                candidate = raw[row["url"]]
                summary = str(candidate.get("summary") or candidate.get("snippet") or "")[:200_000]
                # A clearly out-of-window provider date cannot be rescued by
                # selecting another favourable date from the page.
                date_reason = _date_reason(row, req)
                if date_reason == "date_out_of_range" or date_reason == "date_conflict":
                    row.update(status="filtered", reason=date_reason)
                    return
                result = scraper.fetch_candidate(row["url"], row["source_type"], "static", deadline=body_end, before_fetch=lambda: persist_research_intent(req, intent, model))
                result = await result if inspect.isawaitable(result) else result
                # Reject hard network/source failures before considering a
                # browser. Rendering must never retry a forbidden target.
                attempted_final_url = _canonical_url(result.final_url or row["url"])
                refused = result.reason.startswith("unsafe") or result.reason in {"source_redirect_mismatch", "source_mismatch"}
                final_mismatch = not _is_public_http_url(attempted_final_url) or _classify_by_domain(attempted_final_url) != row["source_type"]
                if refused or final_mismatch:
                    row.update(fetch_status=result.status, fetch_reason=result.reason, final_url=attempted_final_url,
                               status="filtered", reason="source_redirect_mismatch" if final_mismatch or "source" in result.reason else "unsafe_url")
                    return
                needs_render = result.reason in {"empty_body", "verification_or_shell"} or (
                    result.status == "fetched" and not usable_excerpt(result.content))
                if needs_render or (result.status != "fetched" and scraper._source_mode(row["source_type"]) != "static"):
                    if dynamic_count < 20 and loop.time() < body_end:
                        dynamic_count += 1
                        result = scraper.fetch_candidate(row["url"], row["source_type"], "dynamic", deadline=body_end, before_fetch=lambda: persist_research_intent(req, intent, model))
                        result = await result if inspect.isawaitable(result) else result
                    elif dynamic_count >= 20:
                        row["dynamic_unprocessed_reason"] = "browser_budget"
                row.update(fetch_status=result.status, fetch_reason=result.reason, final_url=_canonical_url(result.final_url or row["url"]), truncated=result.truncated)
                if result.reason in {"unsafe_destination", "unsafe_redirect", "unsafe_or_unselected_url", "source_redirect_mismatch"}:
                    row.update(status="filtered", reason="source_redirect_mismatch" if "source" in result.reason else "unsafe_url")
                    return
                if not _is_public_http_url(row["final_url"]) or _classify_by_domain(row["final_url"]) != row["source_type"]:
                    row.update(status="filtered", reason="source_redirect_mismatch")
                    return
                if result.published_at:
                    row["date_sources"]["body"] = result.published_at
                    row["publication_candidates"].extend(result.published_candidates or [result.published_at])
                row["published_at"] = row["date_sources"]["body"] or row["date_sources"]["provider"]
                row["content"] = result.content if result.status == "fetched" else summary
                row["content_kind"] = "body" if result.status == "fetched" else "summary"
                row["author"] = result.author
                row["content_hash"] = _hash_content(row["content"])
                date_reason = _date_reason(row, req)
                if date_reason:
                    row.update(status="filtered", reason=date_reason)
                elif any(word.strip().casefold() in (row["title"] + " " + row["content"]).casefold() for word in req.exclude_words if word.strip()):
                    row.update(status="filtered", reason="excluded_word")
                elif not usable_excerpt(row["content"]):
                    row.update(status="filtered", reason="unusable_content")
                elif result.status == "not_attempted" and result.reason == "fetch_deadline" and not usable_excerpt(summary):
                    row.update(status="unprocessed", reason="body_deadline")
                else:
                    row.update(status="semantic_pending")

            tasks = [asyncio.create_task(fetch(row)) for row in selected]
            try:
                await asyncio.wait_for(asyncio.gather(*tasks), max(0.01, body_end-loop.time()))
            except TimeoutError:
                for row in selected:
                    if row["status"] == "candidate":
                        row.update(status="unprocessed", reason="body_deadline")
            finally:
                for task in tasks:
                    if not task.done():
                        task.cancel()
                await asyncio.gather(*tasks, return_exceptions=True)
            coverage["dynamic_fetches"] = dynamic_count
            coverage["phase_elapsed_seconds"]["fetching_bodies"] = round(loop.time()-body_start, 3)
            hashes = set()
            finals = set()
            eligible = []
            for row in selected:
                if row["status"] == "semantic_pending":
                    if row["content_hash"] in hashes or row["final_url"] in finals:
                        row.update(status="filtered", reason="duplicate_content")
                    else:
                        hashes.add(row["content_hash"])
                        finals.add(row["final_url"])
                        eligible.append(row)
            await checkpoint()
            await persist_research_intent(req, intent, model)
            coverage["phase"] = "semantic_admission"
            semantic_start = loop.time()
            semantic_end = min(deadline - 60, semantic_start + 240)

            async def batch_observed(decisions):
                _apply_decisions(decisions, eligible, partial=True)
                await checkpoint()

            try:
                validation = await classify_materials(llm, model, intent,
                    {row["id"]: row["content"] for row in eligible},
                    usage_context={"run_id": req.run_id, "engine": "query", "phase": "relevance"},
                    deadline=semantic_end, on_batch=batch_observed,
                    material_metadata={row["id"]: {"title": row["title"], "content_kind": row["content_kind"]} for row in eligible})
                if not validation.valid:
                    raise SemanticAdmissionIncomplete("semantic_protocol_rejected",
                        protocol_errors=validation.protocol_errors, batch_document_ids=tuple(row["id"] for row in eligible))
            except SemanticAdmissionIncomplete as exc:
                coverage["semantic_admission_status"] = "incomplete"
                if exc.protocol_errors:
                    coverage["semantic_protocol_errors"] = {
                        "batch_document_ids": list(exc.batch_document_ids), "errors": list(exc.protocol_errors)}
                _apply_decisions(exc.decisions, eligible, partial=True)
                for row in eligible:
                    row.update(status="unprocessed", reason="semantic_deadline" if "incomplete" in str(exc) else "semantic_protocol_rejected")
                await checkpoint()
                raise HTTPException(502, {"code": "semantic_admission_incomplete", "message": "Semantic admission not completed; no report materials accepted", "coverage": coverage}) from exc
            decisions = validation.accepted + validation.context + validation.rejected
            _apply_decisions(decisions, eligible)
            direct = _fair_documents([row for row in eligible if row["semantic_classification"] == "direct"], req.sources)
            context = _fair_documents([row for row in eligible if row["semantic_classification"] == "context"], req.sources)
            admitted = (direct + context)[:limit]
            admitted_ids = {row["id"] for row in admitted}
            for row in eligible:
                if row["semantic_classification"] in {"direct", "context"}:
                    row.update(status="accepted" if row["id"] in admitted_ids else "unprocessed",
                               reason="" if row["id"] in admitted_ids else "result_limit")
            coverage["phase_elapsed_seconds"]["semantic_admission"] = round(loop.time()-semantic_start, 3)
            coverage["semantic_admission_status"] = "completed"
            coverage["phase"] = "completed"
            await checkpoint()
            documents = [{key: value for key, value in row.items() if key not in {"status", "reason", "summary_excerpt", "publication_candidates", "requested_source"}}
                         for row in admitted]
            sources = [{"name": source, "doc_count": sum(row["source_type"] == source for row in admitted),
                        "status": "ok" if any(row["source_type"] == source for row in admitted) else "partial"}
                       for source in req.sources]
            return SearchResponse(documents=documents, total_count=len(documents), sources=sources, coverage=coverage)
    except HTTPException:
        raise
    except asyncio.CancelledError:
        coverage["phase"] = "canceled"
        for row in coverage["candidate_audit"]:
            if row["status"] in {"candidate", "semantic_pending", "semantic_partial"}:
                row.update(status="unprocessed", reason="canceled")
        try:
            await asyncio.shield(asyncio.wait_for(checkpoint(), timeout=20))
        except Exception:
            pass
        raise
    except Exception:
        coverage["phase"] = "failed"
        for row in coverage["candidate_audit"]:
            if row["status"] in {"candidate", "semantic_pending", "semantic_partial"}:
                row.update(status="unprocessed", reason="stage_deadline_or_dependency_failure")
        try:
            await asyncio.wait_for(checkpoint(), timeout=20)
        except Exception:
            pass
        raise HTTPException(503, {"code": "retrieval_stage_incomplete", "message": "Retrieval could not complete its authenticated durable stages", "coverage": _coverage(coverage)})


def _apply_decisions(decisions, rows, partial=False):
    indexed = {row["id"]: row for row in rows}
    for decision in decisions:
        if decision.document_id not in indexed:
            continue
        row = indexed[decision.document_id]
        row.update(semantic_classification=decision.classification, semantic_reason=decision.reason,
            evidence_spans=list(decision.evidence_spans), observed_spans=[{"start": start, "end": end} for start, end in decision.observed_spans],
            status="semantic_partial" if partial else "rejected" if decision.classification in {"irrelevant", "uncertain"} else "semantic_pending",
            reason="" if decision.classification in {"direct", "context"} else decision.classification)
