"""Public feed/list discovery independent of every search/model supplier.

Entry pages are bounded and fetched through the existing DNS-pinned public
proxy. Discovering a link never claims that its body or relevance is verified.
"""
import asyncio
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from html.parser import HTMLParser
import json
import os
import re
from urllib.parse import quote, urljoin, urlsplit
import xml.etree.ElementTree as ET

import httpx

from engines.common.platform_extractors import clean_fragment
from engines.common.safe_fetch import guarded_proxy, is_public_http_url
from engines.common.scraper import SOURCE_DOMAINS, _classify_by_domain

PUBLIC_ENTRIES = {
    "news": [("rss", "https://www.chinanews.com.cn/rss/scroll-news.xml"),
             ("html", "https://www.thepaper.cn/")],
    "toutiao": [("toutiao_hot", "https://www.toutiao.com/hot-event/hot-board/?origin=toutiao_pc")],
}
MAX_PAGE_BYTES = 2 * 1024 * 1024


def _rsshub_entries(source, keywords):
    base = os.getenv("YUQING_RSSHUB_URL", "").rstrip("/")
    if not base:
        return []
    parsed = urlsplit(base)
    # This is a trusted, explicitly configured service, never a user URL.
    if parsed.scheme not in {"http", "https"} or parsed.hostname not in {"127.0.0.1", "::1", "localhost"} or parsed.username or parsed.password:
        return []
    try:
        routes = json.loads(os.getenv("YUQING_RSSHUB_ROUTES_JSON", "{}")).get(source, [])
    except (ValueError, AttributeError):
        return []
    entries = []
    if not isinstance(routes, list):
        return entries
    for route in routes[:8]:
        if not isinstance(route, str) or not route.startswith("/") or route.startswith("//") or ".." in route:
            continue
        for word in (keywords[:8] if "{keyword}" in route else [""]):
            path = route.replace("{keyword}", quote(word, safe=""))
            entries.append(("rsshub", base + path))
    return entries


def collection_capabilities(scrapling_available=False):
    rows = []
    for source in ["news", *SOURCE_DOMAINS]:
        public = bool(PUBLIC_ENTRIES.get(source))
        rsshub = bool(_rsshub_entries(source, ["probe"]))
        limitations = ["公开入口覆盖其列表范围，不代表全站关键词搜索；每条材料另行核验日期、来源和语义。"]
        if source == "toutiao":
            limitations.append("公开榜单可发现链接；详情页可能需要验证，正文能力以本次任务结果为准。")
        if not public and not rsshub:
            limitations.append("尚无已配置独立发现入口，可使用供应商搜索或用户提供的公开链接。")
        if rsshub:
            limitations.append("RSSHub 路由已配置；路由和源站可用性以实际请求为准。")
        if not scrapling_available:
            limitations.append("本机 Scrapling 当前不可用。")
        rows.append({"source": source, "discovery": public or rsshub, "static_fetch": bool(scrapling_available),
            "dynamic_fetch": bool(scrapling_available), "status": "configured_unverified" if public or rsshub else "unavailable",
            "limitations": limitations})
    return {"sources": rows, "limits": {"discovery_pages": 60, "candidates": 100, "results": 50, "dynamic_renders": 20},
            "supplier_independent": True}


class _Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.links = []
        self.current = None

    def handle_starttag(self, tag, attrs):
        values = dict(attrs)
        if tag == "a" and values.get("href"):
            self.current = {"url": values["href"], "title": ""}
        if tag == "link" and values.get("rel") == "next" and values.get("href"):
            self.links.append({"url": values["href"], "next_page": True})

    def handle_data(self, value):
        if self.current:
            self.current["title"] += value

    def handle_endtag(self, tag):
        if tag == "a" and self.current:
            self.links.append(self.current)
            self.current = None


def _date(value):
    if not value:
        return ""
    if isinstance(value, (int, float)):
        try:
            return datetime.fromtimestamp(value, timezone.utc).isoformat()
        except (ValueError, OverflowError, OSError):
            return ""
    value = str(value).strip()
    try:
        return parsedate_to_datetime(value).isoformat()
    except (ValueError, TypeError):
        return value[:100]


def _parse(payload, kind):
    if kind == "toutiao_hot":
        value = json.loads(payload)
        items = value.get("data") or []
        if not isinstance(items, list):
            raise ValueError("invalid public board response")
        return [{"url": str(item.get("Url") or ""), "title": str(item.get("Title") or ""),
                 "published_at": _date(item.get("PublishTime"))} for item in items if isinstance(item, dict)]
    if kind in {"rss", "rsshub"}:
        if re.search(r"<!\s*(?:DOCTYPE|ENTITY)", payload, re.I):
            raise ValueError("unsafe feed declarations")
        root = ET.fromstring(payload)
        rows = []
        for item in root.iter():
            if item.tag.rsplit("}", 1)[-1] not in {"item", "entry"}:
                continue
            values = {}
            for child in item:
                tag = child.tag.rsplit("}", 1)[-1]
                values[tag] = "".join(child.itertext()).strip()
                if tag == "link" and child.get("href") and child.get("rel", "alternate") == "alternate":
                    values["link"] = child.get("href")
            rows.append({"url": values.get("link", ""), "title": clean_fragment(values.get("title", ""))[:1000],
                "summary": clean_fragment(values.get("description") or values.get("summary") or "")[:200_000],
                "published_at": _date(values.get("pubDate") or values.get("published") or values.get("date"))})
        return rows
    parser = _Links()
    parser.feed(payload)
    # Next.js pages also carry source URLs in serialized public page data.
    for match in re.findall(r'(?:https?:)?//[^\s"<>\\]+|/(?:newsDetail_forward_|article/)[^\s"<>\\]+', payload):
        parser.links.append({"url": match, "title": ""})
    return parser.links


async def _read_page(url, kind, *, deadline, transport=None):
    async def send(proxy=None):
        async with httpx.AsyncClient(timeout=min(15, max(0.01, deadline - asyncio.get_running_loop().time())),
            proxy=proxy, transport=transport, trust_env=False, follow_redirects=False) as client:
            current = url
            for _ in range(4):
                async with client.stream("GET", current, headers={"User-Agent": "PanguYuqingPublicCollector/0.5"}) as response:
                    if response.status_code in {301, 302, 303, 307, 308}:
                        target = urljoin(current, response.headers.get("location", ""))
                        if kind == "rsshub" or not is_public_http_url(target) or urlsplit(target).hostname != urlsplit(url).hostname:
                            raise ValueError("unsafe discovery redirect")
                        current = target
                        continue
                    response.raise_for_status()
                    received = bytearray()
                    async for chunk in response.aiter_bytes():
                        received.extend(chunk)
                        if len(received) > MAX_PAGE_BYTES:
                            raise ValueError("discovery page size limit")
                    return bytes(received).decode(response.encoding or "utf-8", errors="replace")
            raise ValueError("discovery redirect limit")

    async with asyncio.timeout_at(deadline):
        if transport is not None or kind == "rsshub":
            return await send()
        if not is_public_http_url(url):
            raise ValueError("unsafe discovery entry")
        with guarded_proxy() as (proxy, _guard):
            return await send(proxy)


async def discover_public(*, keywords, sources, deadline=None, max_pages=60, before_page=None, transport=None):
    deadline = deadline or asyncio.get_running_loop().time() + 90
    channels, candidates, seen, pages = [], [], set(), 0
    queues = {source: list(PUBLIC_ENTRIES.get(source, [])) + _rsshub_entries(source, keywords) for source in sources}
    while any(queues.values()) and pages < min(60, max_pages) and asyncio.get_running_loop().time() < deadline:
        for source in sources:
            if not queues[source] or pages >= min(60, max_pages):
                continue
            kind, entry = queues[source].pop(0)
            channel = {"source": source, "channel": "local_discovery", "entry_url": entry, "status": "failed",
                "discovered_count": 0, "body_count": 0, "summary_count": 0, "accepted_count": 0, "failed_count": 0, "reason": ""}
            channels.append(channel)
            # Authorization/checkpoint failures are never swallowed as entry failures.
            if before_page:
                await before_page()
            pages += 1
            try:
                payload = await _read_page(entry, kind, deadline=deadline, transport=transport)
                found = _parse(payload, kind)
                found.sort(key=lambda row: -sum(word.casefold() in (row.get("title", "") + row.get("summary", "")).casefold() for word in keywords))
                for item in found:
                    url = urljoin(entry, item.get("url", ""))
                    if not is_public_http_url(url) or _classify_by_domain(url) != source:
                        continue
                    if item.get("next_page"):
                        if kind != "rsshub" and urlsplit(url).hostname == urlsplit(entry).hostname:
                            queues[source].append((kind, url))
                        continue
                    # Article-shaped links only; navigation never becomes material.
                    if kind == "html" and not re.search(r"(?:newsDetail_forward_|/article/|\.s?html(?:\?|$))", url):
                        continue
                    if url in seen:
                        continue
                    seen.add(url)
                    channel["discovered_count"] += 1
                    candidates.append({**item, "url": url, "source_type": source, "requested_source": source,
                        "discovery_channel": "local_discovery", "discovery_chain": [{"channel": "local_discovery", "entry_url": entry,
                            "adapter": kind, "observed_at": datetime.now(timezone.utc).isoformat()}]})
                    if len(candidates) >= 300:
                        break
                channel["status"] = "ok"
            except asyncio.CancelledError:
                raise
            except Exception as error:
                channel.update(reason="entry_unavailable", failed_count=1, error_type=type(error).__name__)
    for source in sources:
        if not any(row["source"] == source for row in channels):
            channels.append({"source": source, "channel": "local_discovery", "status": "unavailable", "discovered_count": 0,
                "body_count": 0, "summary_count": 0, "accepted_count": 0, "failed_count": 0, "reason": "no_verified_entry_configured"})
    return {"candidates": candidates, "channels": channels, "pages_visited": pages}
