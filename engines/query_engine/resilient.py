"""Best-available collection: public discovery survives supplier/model failure."""
import asyncio
from datetime import datetime, timezone
import hashlib
import inspect
import os
from urllib.parse import urlsplit

import httpx
from fastapi import HTTPException

from engines.common.deepseek_tools import DeepSeekTools
from engines.common.llm_client import LLMContentRejected
from engines.common.relevance import usable_excerpt
from engines.common.scraper import ProviderBudget, ProviderBudgetExhausted, SOURCE_DOMAINS
from engines.common.semantic_admission import SemanticAdmissionIncomplete
from engines.common.usage_outbox import AccountingDurabilityError
from engines.common.tool_usage import ToolAuthorizationError


class CollectorAuthorizationError(RuntimeError):
    """A durable active-run checkpoint failed; successful delivery is forbidden."""


def _fatal_code(error):
    seen = set()
    while error is not None and id(error) not in seen:
        seen.add(id(error))
        if isinstance(error, AccountingDurabilityError):
            return "accounting_durability_error"
        if isinstance(error, (ToolAuthorizationError, CollectorAuthorizationError)):
            return "billing_authorization_failed"
        if isinstance(error, asyncio.CancelledError):
            return "run_canceled"
        if isinstance(error, (httpx.HTTPStatusError, httpx.RequestError)):
            if error.request.url.path.startswith("/internal/"):
                return "billing_authorization_failed"
        if getattr(error, "billing_failure", False):
            return "billing_authorization_failed"
        if isinstance(error, RuntimeError) and any(part in str(error) for part in ("billing bridge", "authorization is invalid", "accepted run", "tool usage outbox")):
            return "billing_authorization_failed"
        error = error.__cause__ or error.__context__
    return ""


def _failure(error, phase):
    code = _fatal_code(error)
    seen, current = set(), error
    rejected = False
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        rejected = rejected or isinstance(current, LLMContentRejected)
        current = current.__cause__ or current.__context__
    return {"phase": phase, "code": code or ("provider_content_rejected" if rejected else "dependency_unavailable"),
            "error_type": type(error).__name__}


async def execute(req):
    # Resolve dependencies at runtime so the existing HTTP-boundary fixtures
    # exercise the exact same filters, persistence and model implementations.
    from engines.query_engine import main as q

    loop = asyncio.get_running_loop()
    start, deadline = loop.time(), loop.time() + 900
    limit = min(50, req.max_results)
    coverage = {"admission_version": q.ADMISSION_VERSION, "research_intent": {}, "provider_candidates": 0,
        "per_keyword": [], "per_platform": [{"source": source, "status": "unprocessed"} for source in req.sources],
        "candidate_audit": [], "discovery_channels": [], "date_from": req.date_from, "date_to": req.date_to,
        "requested_max_results": req.max_results, "result_limit": limit, "candidate_limit": 100,
        "keywords_searched": [], "failed_keywords": [], "phase": "planning", "phase_elapsed_seconds": {},
        "original_input": {"keywords": req.keywords, "sources": req.sources, "date_from": req.date_from, "date_to": req.date_to,
            "exclude_words": req.exclude_words, "seed_urls": req.seed_urls, "research_question": req.research_question,
            "title": req.title, "analysis_type": req.analysis_type, "scenario_template_id": req.scenario_template_id},
        "resume_from_run_id": req.resume_from_run_id, "warning": "", "delivery_hint": "brief",
        "filter_limitations": "Public entry pages cover their lists, not all-platform search. Raw materials are unverified for topic relevance. Summaries and provider bodies retain separate provenance."}
    scraper = q.get_scraper()
    model = req.llm_model or q.LLM_MODEL
    model_key = req.llm_api_key or os.getenv("LLM_API_KEY") or os.getenv("DEEPSEEK_API_KEY")
    llm = None
    intent = None
    model_blocked = False
    accounting_lock = asyncio.Lock()
    raw, indexed = {}, {}
    provider_budget = ProviderBudget()
    dynamic_count = 0
    tool_calls = 0
    account_unavailable = False
    tools = None
    search_key = req.search_api_key or (os.getenv("DEEPSEEK_API_KEY", "") if req.search_provider == "deepseek" else "")
    bocha_key = req.bocha_api_key or os.getenv("BOCHA_API_KEY") or q.DEFAULT_BOCHA_KEY
    if search_key and (req.search_provider == "deepseek" or req.fetch_provider == "deepseek"):
        tools = DeepSeekTools(search_key, req.search_base_url)

    async def checkpoint():
        async with accounting_lock:
            coverage["elapsed_seconds"] = round(loop.time() - start, 3)
            q._coverage(coverage)
            coverage["raw_usable_count"] = sum(row["status"] == "raw_available" for row in coverage["candidate_audit"])
            for channel in coverage["discovery_channels"]:
                rows = [row for row in coverage["candidate_audit"] if row["source_type"] == channel["source"] and channel["channel"] in {part.get("channel") for part in row.get("discovery_chain", [])}]
                channel.update(body_count=sum(row.get("content_kind") in {"body", "provider_body"} and bool(row.get("content")) for row in rows),
                    summary_count=sum(row.get("content_kind") == "summary" and bool(row.get("content")) for row in rows),
                    accepted_count=sum(row["status"] == "accepted" for row in rows))
            try:
                await q.persist_candidate_audit(req, coverage)
            except Exception as error:
                raise CollectorAuthorizationError("active run checkpoint unavailable") from error

    def insert(candidate, source, channel):
        url = q._canonical_url(str(candidate.get("url") or ""))
        if not url or url == "/":
            return None
        actual_source = q._classify_by_domain(url)
        if url in indexed:
            row = indexed[url]
            row["discovery_chain"].extend(candidate.get("discovery_chain") or [{"channel": channel}])
            value = str(candidate.get("published_at") or "")[:100]
            if value and value not in row["publication_candidates"]:
                row["publication_candidates"].append(value)
            return row
        published = str(candidate.get("published_at") or "")[:100]
        retained = channel == "retained"
        content = str(candidate.get("content") or "")[:200_000] if retained else ""
        original_dates = candidate.get("date_sources") if retained else None
        row = {"id": hashlib.sha256(url.encode()).hexdigest()[:16], "url": url, "final_url": str(candidate.get("final_url") or url) if retained else url,
            "title": str(candidate.get("title") or "")[:1000], "source_type": actual_source, "requested_source": source,
            "source_name": str(candidate.get("source_name") or candidate.get("site_name") or q._source_display_name(actual_source)),
            "content": content, "content_kind": str(candidate.get("content_kind") or "summary") if retained else "summary",
            "summary_excerpt": str(candidate.get("summary") or candidate.get("snippet") or "")[:2000],
            "fetch_status": str(candidate.get("fetch_status") or "fetched") if content else "not_attempted", "fetch_reason": "",
            "published_at": published, "date_sources": original_dates or {"provider": published if channel != "seed" else "", "body": ""},
            "publication_candidates": list(candidate.get("publication_candidates") or [published]) if retained else [published],
            "status": "candidate", "reason": "", "semantic_classification": "", "semantic_reason": "",
            "evidence_spans": [], "observed_spans": [], "truncated": bool(candidate.get("truncated")),
            "discovery_channel": channel, "discovery_chain": list(candidate.get("discovery_chain") or [{"channel": channel}]),
            "fetch_attempts": list(candidate.get("fetch_attempts") or []) if retained else [],
            "text_versions": list(candidate.get("text_versions") or []) if retained else [],
            "content_version": str(candidate.get("content_version") or "") if retained else "",
            "fetched_at": str(candidate.get("fetched_at") or "") if retained else ""}
        if retained:
            row["discovery_chain"].append({"channel": "retained", "run_id": req.resume_from_run_id})
            row["retained_semantic_result"] = {key: candidate[key] for key in ("semantic_classification", "semantic_reason", "evidence_spans", "observed_spans") if key in candidate}
        if not q._is_public_http_url(url):
            row.update(status="filtered", reason="unsafe_url")
        elif actual_source not in req.sources or actual_source != source:
            row.update(status="filtered", reason="source_mismatch")
        indexed[url], raw[url] = row, candidate
        coverage["candidate_audit"].append(row)
        return row

    async def basic_collection():
        nonlocal llm, intent, model_blocked, tools, account_unavailable
        # A durable active-run checkpoint precedes even public discovery.
        await checkpoint()
        if model_key:
            try:
                llm = q.build_client(req.llm_api_key, req.llm_base_url, timeout=900)
                if req.research_intent and req.research_intent_version == q.ADMISSION_VERSION:
                    intent = q.validate_intent(req.research_intent, req, model)
                else:
                    intent = await q.plan_research_intent(llm, model, req, deadline=min(deadline - 60, loop.time() + 120))
                try:
                    intent = await q.persist_research_intent(req, intent, model)
                except Exception as error:
                    raise CollectorAuthorizationError("research intent checkpoint unavailable") from error
                coverage.update(research_intent=intent, planning_status="enhanced")
            except Exception as error:
                if _fatal_code(error):
                    raise
                coverage["failure"] = _failure(error, "planning")
                model_blocked = coverage["failure"]["code"] == "provider_content_rejected"
                intent = None
        if intent is None:
            coverage.update(planning_status="basic_unenhanced", basic_collection_plan={"basis": "original_inputs_without_model_understanding",
                "original_keywords": list(req.keywords), "search_queries": list(req.keywords)[:8]})
            if not model_key:
                coverage["failure"] = {"phase": "planning", "code": "model_unavailable", "error_type": "MissingConfiguration"}
        coverage["phase_elapsed_seconds"]["planning"] = round(loop.time() - start, 3)
        coverage["phase"] = "searching"
        await checkpoint()
        network_start = loop.time()
        network_end = min(deadline - 90, loop.time() + 180)
        queries = (intent or coverage["basic_collection_plan"])["search_queries"][:8]
        discovery = asyncio.create_task(q.discover_public(keywords=req.keywords, sources=req.sources,
            deadline=network_end, max_pages=60, before_page=checkpoint))
        source_groups = {source: [] for source in req.sources}

        async def supplier_recall():
            nonlocal account_unavailable
            for expression in queries:
                for source in req.sources:
                    entry = {"keyword": expression, "source": source, "status": "unavailable", "provider_candidates": 0}
                    coverage["per_keyword"].append(entry)
                    channel_name = "deepseek_search" if req.search_provider == "deepseek" else "bocha_search"
                    channel = {"source": source, "channel": channel_name, "status": "unavailable", "discovered_count": 0,
                        "body_count": 0, "summary_count": 0, "accepted_count": 0, "failed_count": 0, "reason": "provider_not_configured"}
                    coverage["discovery_channels"].append(channel)
                    if loop.time() >= network_end or account_unavailable:
                        entry["reason"] = "account_or_network_unavailable"
                        continue
                    try:
                        await checkpoint()
                        if req.search_provider == "deepseek" and tools:
                            found = await tools.search(expression, source=source, run_id=req.run_id, date_from=req.date_from,
                                date_to=req.date_to, deadline=network_end, budget=provider_budget)
                        elif req.search_provider == "bocha" and bocha_key:
                            freshness = f"{req.date_from}..{req.date_to}" if req.date_from and req.date_to else "noLimit"
                            found = await scraper._bocha_search(expression, bocha_key, include="|".join(SOURCE_DOMAINS.get(source, ())),
                                count=50, freshness=freshness, deadline=network_end, budget=provider_budget, source=source)
                        else:
                            continue
                        entry.update(status="ok", provider_candidates=len(found))
                        channel.update(status="ok", discovered_count=len(found), reason="")
                        coverage["provider_candidates"] += len(found)
                        for item in found:
                            row = insert(item, source, channel_name)
                            if row:
                                source_groups[source].append(row)
                    except Exception as error:
                        if _fatal_code(error):
                            raise
                        entry.update(status="failed", reason="supplier_request_budget" if isinstance(error, ProviderBudgetExhausted) else "provider_request_failed")
                        channel.update(status="failed", reason=entry["reason"], failed_count=1)
                        if isinstance(error, httpx.HTTPStatusError) and error.response.status_code in {401, 402, 403}:
                            # A permanent provider error is not retried per task.
                            account_unavailable = True

        supplier = asyncio.create_task(supplier_recall())
        try:
            independent, _ = await asyncio.gather(discovery, supplier)
        finally:
            for task in (discovery, supplier):
                if not task.done():
                    task.cancel()
            await asyncio.gather(discovery, supplier, return_exceptions=True)
        coverage["discovery_pages"] = independent["pages_visited"]
        coverage["discovery_channels"].extend(independent.get("channels", []))
        local_groups = {source: [] for source in req.sources}
        for item in independent["candidates"]:
            source = item.get("requested_source") or item.get("source_type") or q._classify_by_domain(item.get("url", ""))
            row = insert(item, source, "local_discovery")
            if row and source in local_groups:
                local_groups[source].append(row)
        retained, seeds = [], []
        for item in req.retained_candidates:
            row = insert(item, str(item.get("source_type") or q._classify_by_domain(item.get("url", ""))), "retained")
            if row:
                retained.append(row)
        for url in req.seed_urls:
            row = insert({"url": url}, q._classify_by_domain(url), "seed")
            if row:
                seeds.append(row)
        for channel_name, rows in [("retained", retained), ("seed", seeds)]:
            for source in req.sources:
                count = sum(row["source_type"] == source for row in rows)
                if count:
                    coverage["discovery_channels"].append({"source": source, "channel": channel_name, "status": "ok", "discovered_count": count,
                        "body_count": 0, "summary_count": 0, "accepted_count": 0, "failed_count": 0, "reason": ""})
        local = list(q._fair_groups([local_groups[source] for source in req.sources]))
        supplier_rows = list(q._fair_groups([source_groups[source] for source in req.sources]))
        selected, seen = [], set()
        # Reserve thirty executable independent candidates, then reclaim unused
        # capacity. Existing saved versions have priority in the remaining pool.
        for row in local[:30] + retained + seeds + supplier_rows + local[30:]:
            if row["url"] in seen:
                continue
            seen.add(row["url"])
            if row["status"] == "filtered":
                continue
            if not q._is_public_http_url(row["url"]):
                row.update(status="filtered", reason="unsafe_url")
            elif row["source_type"] not in req.sources or row["source_type"] != row["requested_source"]:
                row.update(status="filtered", reason="source_mismatch")
            elif len(selected) < 100:
                selected.append(row)
            else:
                row.update(status="unprocessed", reason="candidate_budget")
        coverage.update(provider_calls=provider_budget.calls, independent_reserved_candidates=min(30, len(local)))
        coverage["phase_elapsed_seconds"]["searching"] = round(loop.time() - network_start, 3)
        coverage["keywords_searched"] = list(dict.fromkeys(row["keyword"] for row in coverage["per_keyword"] if row["status"] == "ok"))
        coverage["failed_keywords"] = list(dict.fromkeys(row["keyword"] for row in coverage["per_keyword"] if row["status"] == "failed"))
        coverage["keyword_search"] = "partial" if coverage["failed_keywords"] else "applied" if coverage["keywords_searched"] else "independent_list_only"
        await checkpoint()
        return selected

    def save_version(row, content, kind, channel):
        row.update(content=content[:200_000], content_kind=kind, content_hash=q._hash_content(content[:200_000]),
                   content_sha256=hashlib.sha256(content[:200_000].encode()).hexdigest())
        if not row["fetched_at"]:
            row["fetched_at"] = datetime.now(timezone.utc).isoformat()
        if not row["content_version"]:
            row["content_version"] = row["content_sha256"]
        if not any(item.get("sha256") == row["content_sha256"] for item in row["text_versions"]):
            row["text_versions"].append({"version": row["content_version"], "sha256": row["content_sha256"], "channel": channel,
                "content_kind": kind, "fetched_at": row["fetched_at"]})

    async def fetch_materials(selected):
        nonlocal dynamic_count, tool_calls, account_unavailable
        coverage["phase"] = "fetching_bodies"
        body_start, body_end = loop.time(), min(deadline - 90, loop.time() + 300)

        async def fetch(row):
            nonlocal dynamic_count, tool_calls, account_unavailable
            item = raw[row["url"]]
            if q._date_reason(row, req) in {"date_out_of_range", "date_conflict"}:
                row.update(status="filtered", reason=q._date_reason(row, req))
                return
            if not q._is_public_http_url(row["final_url"]) or q._classify_by_domain(row["final_url"]) != row["source_type"]:
                row.update(status="filtered", reason="source_redirect_mismatch")
                return
            if not row["content"]:
                provider_body = str(item.get("content") or "") if item.get("content_kind") == "provider_body" else ""
                summary = str(item.get("summary") or item.get("snippet") or "")[:200_000]
                if provider_body and usable_excerpt(provider_body):
                    save_version(row, provider_body, "provider_body", "deepseek_search")
                    row.update(fetch_status="fetched", fetch_reason="provider_search_content")
                else:
                    async def local_attempt(mode):
                        before = loop.time()
                        result = scraper.fetch_candidate(row["url"], row["source_type"], mode, deadline=body_end, before_fetch=checkpoint)
                        result = await result if inspect.isawaitable(result) else result
                        row["fetch_attempts"].append({"channel": "local_" + mode, "status": result.status, "reason": result.reason,
                            "elapsed_seconds": round(loop.time() - before, 3), "fetched_at": datetime.now(timezone.utc).isoformat()})
                        if result.published_at:
                            row["date_sources"]["body"] = result.published_at
                            row["publication_candidates"].extend(result.published_candidates or [result.published_at])
                        return result

                    await checkpoint()
                    result = await local_attempt("static")
                    final = q._canonical_url(result.final_url or row["url"])
                    hard_refusal = result.reason.startswith("unsafe") or result.reason in {"source_redirect_mismatch", "source_mismatch"}
                    if hard_refusal or not q._is_public_http_url(final) or q._classify_by_domain(final) != row["source_type"]:
                        row.update(status="filtered", reason="unsafe_url" if result.reason.startswith("unsafe") else "source_redirect_mismatch",
                            fetch_status=result.status, fetch_reason=result.reason, final_url=final)
                        return
                    if q._date_reason(row, req) in {"date_out_of_range", "date_conflict"}:
                        row.update(status="filtered", reason=q._date_reason(row, req), fetch_status=result.status, fetch_reason=result.reason)
                        return
                    if result.status != "fetched" or not usable_excerpt(result.content):
                        if req.fetch_provider == "deepseek" and tools and not account_unavailable and tool_calls < 50:
                            tool_calls += 1
                            try:
                                fetched = await tools.fetch(row["url"], run_id=req.run_id, deadline=body_end)
                                row["fetch_attempts"].append({"channel": "deepseek_fetch", "status": "fetched" if fetched["content"] else "failed", "reason": ""})
                                if usable_excerpt(fetched["content"]):
                                    save_version(row, fetched["content"], "provider_body", "deepseek_fetch")
                                    row.update(fetch_status="fetched", fetch_reason="provider_fetch_content")
                            except Exception as error:
                                if _fatal_code(error):
                                    raise
                                row["fetch_attempts"].append({"channel": "deepseek_fetch", "status": "failed", "reason": "provider_request_failed"})
                                if isinstance(error, httpx.HTTPStatusError) and error.response.status_code in {401, 402, 403}:
                                    account_unavailable = True
                        if not row["content"] and dynamic_count < 20 and loop.time() < body_end and (
                            result.reason in {"empty_body", "verification_or_shell"} or scraper._source_mode(row["source_type"]) != "static"):
                            dynamic_count += 1
                            result = await local_attempt("dynamic")
                    final = q._canonical_url(result.final_url or row["url"])
                    row.update(final_url=final, truncated=result.truncated)
                    if result.reason.startswith("unsafe") or result.reason in {"source_redirect_mismatch", "source_mismatch"} or not q._is_public_http_url(final) or q._classify_by_domain(final) != row["source_type"]:
                        row.update(status="filtered", reason="unsafe_url" if result.reason.startswith("unsafe") else "source_redirect_mismatch")
                        return
                    if result.published_at:
                        row["date_sources"]["body"] = result.published_at
                        row["publication_candidates"].extend(result.published_candidates or [result.published_at])
                    row["published_at"] = row["date_sources"].get("body") or row["date_sources"].get("provider") or row["published_at"]
                    if not row["content"]:
                        if result.status == "fetched" and usable_excerpt(result.content):
                            save_version(row, result.content, "body", "local_scrapling")
                        else:
                            save_version(row, summary, "summary", row["discovery_channel"])
                        row.update(fetch_status=result.status, fetch_reason=result.reason)
            else:
                # The current immutable saved text is reused, not re-fetched.
                save_version(row, row["content"], row["content_kind"], "retained")
            reason = q._date_reason(row, req)
            if reason:
                row.update(status="filtered", reason=reason)
            elif any(word.strip().casefold() in (row["title"] + " " + row["content"]).casefold() for word in req.exclude_words if word.strip()):
                row.update(status="filtered", reason="excluded_word")
            elif not usable_excerpt(row["content"]):
                row.update(status="filtered", reason="unusable_content")
            else:
                row.update(status="semantic_pending")

        # The existing Scrapling service limiters bound actual workers. A small
        # task gate additionally bounds bridge/checkpoint traffic across rows.
        gate = asyncio.Semaphore(4)

        async def bounded(row):
            async with gate:
                await fetch(row)
                await checkpoint()

        tasks = [asyncio.create_task(bounded(row)) for row in selected]
        try:
            await asyncio.wait_for(asyncio.gather(*tasks), max(0.01, body_end - loop.time()))
        except TimeoutError:
            for row in selected:
                if row["status"] == "candidate":
                    row.update(status="unprocessed", reason="body_deadline")
        finally:
            for task in tasks:
                if not task.done():
                    task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
        coverage.update(dynamic_fetches=dynamic_count, provider_fetch_calls=tool_calls)
        coverage["phase_elapsed_seconds"]["fetching_bodies"] = round(loop.time() - body_start, 3)
        eligible, hashes, finals = [], set(), set()
        for row in selected:
            if row["status"] != "semantic_pending":
                continue
            if row["content_hash"] in hashes or row["final_url"] in finals:
                row.update(status="filtered", reason="duplicate_content")
            else:
                hashes.add(row["content_hash"])
                finals.add(row["final_url"])
                eligible.append(row)
        await checkpoint()
        return eligible

    def response(rows):
        documents = [{key: value for key, value in row.items() if key not in {"status", "reason", "publication_candidates", "summary_excerpt", "requested_source"}} for row in rows]
        return q.SearchResponse(documents=documents, total_count=len(documents),
            sources=[{"name": source, "doc_count": sum(row["source_type"] == source for row in rows),
                      "status": "ok" if any(row["source_type"] == source for row in rows) else "partial"} for source in req.sources], coverage=coverage)

    async def raw_response(eligible):
        selected = q._fair_documents(eligible, req.sources)[:limit]
        ids = {row["id"] for row in selected}
        for row in eligible:
            # Incomplete admission cannot become a silent direct/context grant.
            if row.get("semantic_classification"):
                row["partial_semantic_result"] = {"classification": row["semantic_classification"], "reason": row["semantic_reason"]}
            row.update(semantic_classification="uncertain", semantic_reason="语义准入尚未完成；仅保留可追溯资料。",
                evidence_spans=[], observed_spans=[], status="raw_available" if row["id"] in ids else "unprocessed",
                reason="semantic_unverified" if row["id"] in ids else "result_limit")
        coverage.update(delivery_hint="brief" if selected else "empty", semantic_admission_status="incomplete", phase="completed")
        await checkpoint()
        return response(selected)

    try:
        async with asyncio.timeout_at(deadline - 30):
            selected = await basic_collection()
            eligible = await fetch_materials(selected)
            if not eligible or llm is None or intent is None or model_blocked or (account_unavailable and req.search_provider == "deepseek"):
                return await raw_response(eligible)
            coverage["phase"] = "semantic_admission"
            semantic_start = loop.time()

            async def observed(decisions):
                q._apply_decisions(decisions, eligible, partial=True)
                await checkpoint()

            try:
                validation = await q.classify_materials(llm, model, intent, {row["id"]: row["content"] for row in eligible},
                    usage_context={"run_id": req.run_id, "engine": "query", "phase": "relevance"},
                    deadline=min(deadline - 60, loop.time() + 240), on_batch=observed,
                    material_metadata={row["id"]: {"title": row["title"], "content_kind": row["content_kind"]} for row in eligible})
                if not validation.valid:
                    raise SemanticAdmissionIncomplete("semantic_protocol_rejected", protocol_errors=validation.protocol_errors)
                q._apply_decisions(validation.accepted + validation.context + validation.rejected, eligible)
            except Exception as error:
                if _fatal_code(error):
                    raise
                coverage["failure"] = _failure(error, "semantic_admission")
                coverage["phase_elapsed_seconds"]["semantic_admission"] = round(loop.time() - semantic_start, 3)
                return await raw_response(eligible)
            admitted = (q._fair_documents([row for row in eligible if row["semantic_classification"] == "direct"], req.sources) +
                        q._fair_documents([row for row in eligible if row["semantic_classification"] == "context"], req.sources))[:limit]
            ids = {row["id"] for row in admitted}
            for row in eligible:
                if row["semantic_classification"] in {"direct", "context"}:
                    row.update(status="accepted" if row["id"] in ids else "unprocessed", reason="" if row["id"] in ids else "result_limit")
            coverage.update(phase="completed", semantic_admission_status="completed", delivery_hint="" if admitted else "empty")
            coverage["phase_elapsed_seconds"]["semantic_admission"] = round(loop.time() - semantic_start, 3)
            await checkpoint()
            return response(admitted)
    except asyncio.CancelledError:
        coverage.update(phase="canceled", failure={"phase": coverage["phase"], "code": "run_canceled", "error_type": "CancelledError"})
        try:
            await asyncio.shield(asyncio.wait_for(checkpoint(), 15))
        except Exception:
            pass
        raise
    except Exception as error:
        fatal = _fatal_code(error)
        coverage["failure"] = _failure(error, coverage["phase"])
        if fatal:
            coverage["phase"] = "failed"
            raise HTTPException(403 if fatal == "billing_authorization_failed" else 503,
                {"code": fatal, "message": "Authenticated collection accounting could not complete", "coverage": coverage}) from None
        # Preserve already fetched, hard-filtered rows if a dependency deadline
        # fails after useful acquisition. Never rescue filtered/unsafe rows.
        eligible = [row for row in coverage["candidate_audit"] if row["status"] in {"semantic_pending", "semantic_partial", "accepted"} and usable_excerpt(row["content"])]
        return await raw_response(eligible)
