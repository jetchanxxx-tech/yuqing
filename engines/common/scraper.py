"""Bocha candidate transport and bounded Scrapling recovery for 盘古舆情.

This module enforces network/source boundaries. Relevance is decided only by
semantic-v2 in the authenticated accepted-run query entrypoint.
"""
import asyncio
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
import hashlib
import importlib.util
import os
from urllib.parse import urlsplit
import weakref

import httpx

from engines.common.platform_extractors import extract_page, MAX_BODY_BYTES
from engines.common.safe_fetch import is_public_http_url, run_fetch_subprocess

BOCHA_API_URL = "https://api.bochaai.com/v1/web-search"
BOCHA_API_KEY = os.getenv("BOCHA_API_KEY", "")
SOURCE_DOMAINS = {
    "douyin": ("douyin.com", "iesdouyin.com"), "toutiao": ("toutiao.com",),
    "xigua": ("ixigua.com",), "weibo": ("weibo.com", "weibo.cn"), "wechat": ("mp.weixin.qq.com",),
}
NEWS_DOMAINS = ("36kr.com", "sohu.com", "ifeng.com", "sina.com.cn", "news.qq.com", "thepaper.cn",
                "xinhuanet.com", "people.com.cn", "cnr.cn", "cctv.com", "chinanews.com.cn", "yicai.com",
                "jiemian.com", "stcn.com", "cnstock.com", "nbd.com.cn", "cs.com.cn")
_EXTENDED_DOMAINS = {
    "xiaohongshu": ("xiaohongshu.com", "xhslink.com"), "bilibili": ("bilibili.com", "b23.tv"),
    "kuaishou": ("kuaishou.com", "ksurl.cn"), "zhihu": ("zhihu.com",),
}
_scrapling_available = importlib.util.find_spec("scrapling") is not None


@dataclass
class ScrapedDocument:
    title: str = ""
    url: str = ""
    content: str = ""
    author: str = ""
    source_type: str = ""
    source_name: str = ""
    published_at: str = ""
    content_hash: str = ""
    media_url: str = ""
    error: str = ""
    content_kind: str = "summary"
    fetch_status: str = "not_attempted"
    fetch_reason: str = ""
    final_url: str = ""
    truncated: bool = False


@dataclass
class FetchOutcome:
    content: str = ""
    final_url: str = ""
    status: str = "failed"
    reason: str = ""
    content_kind: str = "summary"
    published_at: str = ""
    truncated: bool = False
    published_candidates: list[str] = field(default_factory=list)
    title: str = ""
    author: str = ""


class ProviderBudgetExhausted(RuntimeError):
    pass


@dataclass
class ProviderBudget:
    calls: int = 0
    per_source: dict = field(default_factory=dict)

    def consume(self, source):
        if self.calls >= 40 or self.per_source.get(source, 0) >= 8:
            raise ProviderBudgetExhausted("supplier_request_budget")
        self.calls += 1
        self.per_source[source] = self.per_source.get(source, 0) + 1


class ProviderLimiter:
    """One shared limiter per service event loop, across concurrent analyses."""
    def __init__(self, interval=1.0, concurrency=2):
        self.interval = interval
        self._semaphore = asyncio.Semaphore(concurrency)
        self._lock = asyncio.Lock()
        self._next = 0.0

    @asynccontextmanager
    async def slot(self, deadline=None):
        loop = asyncio.get_running_loop()
        async with asyncio.timeout_at(deadline):
            async with self._semaphore:
                async with self._lock:
                    delay = max(0, self._next - loop.time())
                    if delay:
                        await asyncio.sleep(delay)
                    self._next = loop.time() + self.interval
                yield


_LIMITERS = weakref.WeakKeyDictionary()
_FETCH_LIMITERS = weakref.WeakKeyDictionary()


def _provider_limiter():
    loop = asyncio.get_running_loop()
    if loop not in _LIMITERS:
        _LIMITERS[loop] = ProviderLimiter()
    return _LIMITERS[loop]


def _fetch_limiter(mode):
    loop = asyncio.get_running_loop()
    if loop not in _FETCH_LIMITERS:
        _FETCH_LIMITERS[loop] = {"static": asyncio.Semaphore(4), "browser": asyncio.Semaphore(1)}
    return _FETCH_LIMITERS[loop]["static" if mode == "static" else "browser"]


class PageScraper:
    def __init__(self, headless=True, timeout=30):
        self.headless = headless
        self.timeout = timeout
        self._available = _scrapling_available

    async def fetch_candidate(self, url, source_type, mode="static", *, deadline=None, before_fetch=None):
        if not is_public_http_url(url) or _classify_by_domain(url) != source_type:
            return FetchOutcome(reason="unsafe_or_unselected_url")
        if not self._available:
            return FetchOutcome(reason="scrapling_unavailable")
        try:
            async with asyncio.timeout_at(deadline):
                async with _fetch_limiter(mode):
                    if before_fetch is not None:
                        await before_fetch()
                    values = await run_fetch_subprocess(url, source_type, mode, deadline=deadline)
                    fields = FetchOutcome.__dataclass_fields__
                    return FetchOutcome(**{key: value for key, value in values.items() if key in fields})
        except TimeoutError:
            return FetchOutcome(status="not_attempted", reason="fetch_deadline")

    def fetch(self, url, source_type="news", mode="static"):
        """Independent bounded fetch; report retrieval uses the async variant."""
        try:
            asyncio.get_running_loop()
        except RuntimeError:
            result = asyncio.run(self.fetch_candidate(url, source_type, mode))
            return ScrapedDocument(url=url, source_type=source_type, content=result.content,
                published_at=result.published_at, final_url=result.final_url, content_kind=result.content_kind,
                fetch_status=result.status, fetch_reason=result.reason, error=result.reason)
        return ScrapedDocument(url=url, source_type=source_type, error="use_async_fetch_candidate")

    async def search_and_fetch(self, *args, **kwargs):
        # A caller cannot opt into the old free/unmetered lexical route. Old
        # lexical-v1 is a persisted read format, never a live search protocol.
        raise RuntimeError("Live retrieval requires the authenticated semantic-v2 /search accepted-run path")

    async def _bocha_search(self, keyword, api_key, include="", count=20, freshness="noLimit", *,
                            deadline=None, budget=None, source=""):
        deadline = deadline or asyncio.get_running_loop().time() + 45
        for attempt in range(2):
            retry_after = 1.0
            try:
                async with _provider_limiter().slot(deadline):
                    if budget is not None:
                        budget.consume(source)
                    remaining = deadline - asyncio.get_running_loop().time()
                    if remaining <= 0:
                        raise TimeoutError("search_deadline")
                    async with httpx.AsyncClient(timeout=min(20, remaining), trust_env=False, follow_redirects=False) as client:
                        async with client.stream("POST", BOCHA_API_URL,
                            headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
                            json={"query": keyword, "count": min(50, max(1, count)), "summary": True,
                                  "freshness": freshness, **({"include": include} if include else {})}) as response:
                            retry_after = _retry_after(response.headers.get("Retry-After", ""))
                            response.raise_for_status()
                            payload = bytearray()
                            async for chunk in response.aiter_bytes():
                                payload.extend(chunk)
                                if len(payload) > MAX_BODY_BYTES:
                                    raise ValueError("invalid search response: size limit")
                            import json
                            body = json.loads(payload)
                payload = body.get("data", body) if isinstance(body, dict) else {}
                web_pages = payload.get("webPages") or payload.get("webpages")
                if not isinstance(web_pages, dict) or not isinstance(web_pages.get("value"), list):
                    raise ValueError("invalid search response: missing webPages.value")
                out = []
                for item in web_pages["value"][:50]:
                    if isinstance(item, dict) and item.get("url"):
                        out.append({"url": str(item["url"]), "title": str(item.get("name") or item.get("title") or "")[:1000],
                            "snippet": str(item.get("snippet") or "")[:200_000], "summary": str(item.get("summary") or "")[:200_000],
                            "site_name": str(item.get("siteName") or "")[:200], "published_at": str(item.get("datePublished") or "")[:100]})
                return out
            except httpx.HTTPStatusError as exc:
                if attempt or (exc.response.status_code != 429 and exc.response.status_code < 500):
                    raise
            except (httpx.TimeoutException, httpx.TransportError):
                if attempt:
                    raise
            if asyncio.get_running_loop().time() + retry_after >= deadline:
                raise TimeoutError("search_retry_deadline")
            await asyncio.sleep(retry_after)
        raise RuntimeError("unreachable_supplier_retry")

    @staticmethod
    def _source_mode(source):
        return "dynamic" if source in {"weibo", "douyin", "xigua"} else "static"

    def _extract(self, page, url, source_type):
        item = extract_page(page, source_type)
        return ScrapedDocument(url=url, source_type=source_type, title=item.get("title", ""),
                               content=item["content"], published_at=item["published_at"], author=item.get("author", ""))


def _retry_after(value):
    try:
        return max(0.0, float(value))
    except ValueError:
        try:
            return max(0.0, (parsedate_to_datetime(value) - datetime.now(timezone.utc)).total_seconds())
        except (ValueError, TypeError, OverflowError):
            return 1.0


def _hash_content(content):
    return hashlib.sha256(content.encode("utf-8")).hexdigest()[:16]


def _is_public_http_url(url):
    return is_public_http_url(url)


def _extract_clean_body(page, source_type):
    return extract_page(page, source_type)["content"]


def _source_display_name(source):
    return {"weibo": "微博", "news": "新闻", "wechat": "公众号", "toutiao": "今日头条", "xigua": "西瓜视频",
            "douyin": "抖音", "xiaohongshu": "小红书", "bilibili": "B站", "kuaishou": "快手", "zhihu": "知乎"}.get(source, source)


def _classify_by_domain(url):
    try:
        host = (urlsplit(url).hostname or "").lower().rstrip(".")
    except ValueError:
        return "custom_web"
    for source, domains in {**SOURCE_DOMAINS, **_EXTENDED_DOMAINS, "news": NEWS_DOMAINS}.items():
        if any(host == domain or host.endswith("." + domain) for domain in domains):
            return source
    return "custom_web"
