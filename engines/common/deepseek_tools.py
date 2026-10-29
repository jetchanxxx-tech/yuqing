"""Bounded raw HTTP DeepSeek Search/Fetch with accepted-run tool accounting."""
import asyncio
from datetime import date, timedelta
import hashlib
import json
import uuid

import httpx

from engines.common.safe_fetch import guarded_proxy, is_public_http_url
from engines.common.scraper import SOURCE_DOMAINS, _provider_limiter
from engines.common.tool_usage import authorize_tool_call, persist_tool_event

MAX_RESPONSE_BYTES = 4 * 1024 * 1024


def search_queries(expression, source, date_from="", date_to=""):
    operators = []
    domains = SOURCE_DOMAINS.get(source, ())
    if domains:
        operators.append("site:" + domains[0])
    # Operators are hints only. Admission independently applies Shanghai dates.
    if date_from:
        operators.append("after:" + (date.fromisoformat(date_from) - timedelta(days=1)).isoformat())
    if date_to:
        operators.append("before:" + (date.fromisoformat(date_to) + timedelta(days=1)).isoformat())
    suffix = " " + " ".join(operators) if operators else ""
    capacity = 128 - len(suffix)
    if capacity < 1:
        raise ValueError("search operator budget exhausted")
    expression = str(expression).strip()
    if not expression:
        raise ValueError("search expression required")
    # Keep every Unicode character; long inputs become explicit bounded calls.
    return [expression[index:index + capacity] + suffix for index in range(0, len(expression), capacity)]


class DeepSeekTools:
    def __init__(self, api_key, base_url="https://api.deepseek.com", *, transport=None, billing_transport=None):
        self.api_key = api_key
        self.base_url = (base_url or "https://api.deepseek.com").rstrip("/").removesuffix("/v1")
        if not is_public_http_url(self.base_url):
            raise ValueError("unsafe tool provider endpoint")
        self.transport = transport
        self.billing_transport = billing_transport

    async def _request(self, operation, payload, run_id, deadline=None):
        if not self.api_key:
            raise ValueError("tool provider key required")
        loop = asyncio.get_running_loop()
        deadline = deadline or loop.time() + 45
        call_id = uuid.uuid4().hex
        request_hash = hashlib.sha256(json.dumps(payload, sort_keys=True, ensure_ascii=False, separators=(",", ":")).encode()).hexdigest()
        async with _provider_limiter().slot(deadline):
            permission = await authorize_tool_call(run_id, call_id, operation, 1, request_hash, transport=self.billing_transport)
            event = {"event_id": uuid.uuid4().hex, "call_id": call_id, "event_version": 1, "attempt": 1,
                "provider": "deepseek", "operation": operation, "outcome": "provider_pending", "http_status": 0,
                "result_count": 0, "usage_status": "unknown", "provider_request_id": ""}
            persist_tool_event(event, permission["permit"], pending=True)
            try:
                async def send(proxy=None):
                    remaining = max(0.01, deadline - loop.time())
                    async with httpx.AsyncClient(timeout=min(45, remaining), transport=self.transport,
                                                proxy=proxy, trust_env=False, follow_redirects=False) as client:
                        async with client.stream("POST", self.base_url + "/v1/tools/" + operation,
                            headers={"Authorization": "Bearer " + self.api_key}, json=payload) as response:
                            event.update(http_status=response.status_code, provider_request_id=response.headers.get("x-request-id", ""))
                            received = bytearray()
                            async for chunk in response.aiter_bytes():
                                received.extend(chunk)
                                if len(received) > MAX_RESPONSE_BYTES:
                                    raise ValueError("tool response size exceeded")
                            response.raise_for_status()
                            result = json.loads(received)
                            if not isinstance(result, dict):
                                raise ValueError("tool response must be an object")
                            return result

                async with asyncio.timeout_at(deadline):
                    if self.transport is not None:
                        result = await send()
                    else:
                        with guarded_proxy() as (proxy, _guard):
                            result = await send(proxy)
                if operation == "search":
                    if not isinstance(result.get("results"), list):
                        raise ValueError("tool search missing results")
                    count = len(result["results"])
                else:
                    if not isinstance(result.get("content"), str):
                        raise ValueError("tool fetch missing content")
                    count = int(bool(result["content"].strip()))
                event.update(outcome="provider_response", usage_status="reported", result_count=count,
                             provider_request_id=str(result.get("id") or event["provider_request_id"])[:200])
            except BaseException:
                # A received error status is observed; an interrupted successful
                # response remains unknown until reconciliation with supplier.
                event.update(outcome="provider_error", usage_status="reported" if event["http_status"] >= 400 else "unknown")
                persist_tool_event(event, permission["permit"])
                raise
            persist_tool_event(event, permission["permit"])
            return result

    async def search(self, expression, *, source, run_id, date_from="", date_to="", count=50, deadline=None, budget=None):
        rows = []
        for query in search_queries(expression, source, date_from, date_to):
            if budget is not None:
                budget.consume(source)
            result = await self._request("search", {"query": query, "count": min(50, max(1, count)), "need_content": True}, run_id, deadline)
            if not isinstance(result.get("results"), list):
                raise ValueError("tool search missing results")
            for item in result["results"][:50]:
                if not isinstance(item, dict) or not item.get("url"):
                    continue
                content = str(item.get("content") or "")[:200_000]
                rows.append({"url": str(item["url"]), "title": str(item.get("title") or "")[:1000],
                    "published_at": str(item.get("date") or "")[:100], "content": content,
                    "content_kind": "provider_body" if content else "summary",
                    "summary": str(item.get("snippet") or item.get("summary") or "")[:200_000],
                    "provider_query": query, "discovery_channel": "deepseek_search"})
        return rows

    async def fetch(self, url, *, run_id, deadline=None):
        if not is_public_http_url(url):
            raise ValueError("unsafe fetch target")
        result = await self._request("fetch", {"url": url}, run_id, deadline)
        return {"title": str(result.get("title") or "")[:1000], "content": str(result.get("content") or "")[:200_000],
                "content_kind": "provider_body"}
