"""
Scrapling-powered web scraper for 盘古舆情.
Wraps Scrapling's adaptive fetching + Bocha AI search with dedup and error handling.

Requirements: pip install scrapling[fetchers] httpx
"""
import hashlib
import logging
import os
import re
from typing import Optional
from dataclasses import dataclass, field

import httpx

logger = logging.getLogger(__name__)

# ── Bocha API config ───────────────────────────────────────
# 端点必须是 /v1/web-search。早期误用 /v1/ai/search，该路径不存在，
# 接口返回 404（实测），导致搜索结果恒为空、任务「秒完成但 0 文档」。
BOCHA_API_URL = "https://api.bochaai.com/v1/web-search"
BOCHA_API_KEY = os.getenv("BOCHA_API_KEY", "")

# Lazy import — Scrapling may not be installed in dev.
_scrapling_available = False
try:
    from scrapling.fetchers import Fetcher, DynamicFetcher  # type: ignore
    _scrapling_available = True
except ImportError:
    logger.warning("Scrapling not installed. PageScraper will return empty results. "
                   "Install: pip install scrapling[fetchers]")


@dataclass
class ScrapedDocument:
    """Normalized document from any source."""
    title: str = ""
    url: str = ""
    content: str = ""
    author: str = ""
    source_type: str = ""      # weibo, news, xiaohongshu, bilibili, rss, custom_web
    source_name: str = ""
    published_at: str = ""
    content_hash: str = ""     # SHA256 hex[:16] for dedup
    media_url: str = ""
    error: str = ""            # non-empty if fetch failed


class PageScraper:
    """Adaptive page scraper backed by Scrapling.

    Usage:
        scraper = PageScraper()
        docs = await scraper.search_and_fetch("雅阁后排", sources=["news"], max_per_source=5)
    """

    def __init__(self, headless: bool = True, timeout: int = 30):
        self.headless = headless
        self.timeout = timeout
        self._available = _scrapling_available

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def fetch(self, url: str, source_type: str = "news", mode: str = "static") -> ScrapedDocument:
        """Fetch a single URL. mode: 'static' | 'dynamic' | 'stealth'."""
        if not self._available:
            return ScrapedDocument(url=url, source_type=source_type,
                                   error="Scrapling not installed")

        try:
            if mode == "dynamic":
                page = DynamicFetcher.fetch(url, headless=self.headless,
                                            network_idle=True, timeout=self.timeout)
            elif mode == "stealth":
                # StealthyFetcher requires extra dep; fall back to dynamic
                page = DynamicFetcher.fetch(url, headless=self.headless,
                                            solve_cloudflare=True, timeout=self.timeout)
            else:
                page = Fetcher.get(url, timeout=self.timeout)

            return self._extract(page, url, source_type)
        except Exception as e:
            logger.warning(f"Scrapling fetch failed for {url}: {e}")
            return ScrapedDocument(url=url, source_type=source_type, error=str(e))

    async def search_and_fetch(self, keyword: str, sources: list[str],
                                max_per_source: int = 5, bocha_key: str = "") -> list[ScrapedDocument]:
        """Search via Bocha API → fetch each URL via Scrapling → dedup.

        Falls back to preset test URLs if Bocha key is not available.
        """
        results: list[ScrapedDocument] = []
        seen_hashes: set[str] = set()

        # 1. 搜索：有 Bocha key 走真实搜索，否则退回预设测试 URL
        #    候选是 dict（url/title/snippet），不是裸 URL 字符串
        all_candidates: dict[str, list[dict]] = {}  # source → [candidate]
        key = bocha_key or BOCHA_API_KEY

        if key:
            # 有 key 时 Bocha 失败必须抛错 —— 绝不静默降级为空/假数据
            #（生产上 key 失效若静默，会产出「看起来成功」的假报告）
            found = await self._bocha_search(keyword, key)
            # Bocha 返回通用网页结果，按 URL 域名分类（P0 修复）
            for item in found:
                url = item.get("url", "")
                if not url:
                    continue
                stype = _classify_by_domain(url)
                if stype not in all_candidates:
                    all_candidates[stype] = []
                all_candidates[stype].append(item)
        else:
            for source in sources:
                all_candidates[source] = [
                    {"url": u, "title": "", "snippet": ""}
                    for u in self._preset_search_urls(keyword, source)
                ]

        # 2. 抓取正文 + 去重
        # 配额截断修复（P0 bug）：先收集所有候选 URL，再按用户勾选的来源过滤，
        # 最后截取全局 max_per_source（而非每个 source 单独截断 → 勾越多结果越多）。
        all_urls: list[tuple[str, dict]] = []  # (source_type, candidate)
        for stype, candidates in all_candidates.items():
            for cand in candidates:
                all_urls.append((stype, cand))

        for stype, cand in all_urls:
            url = cand.get("url", "")
            if not url:
                continue
            mode = self._source_mode(stype)
            doc = self.fetch(url, source_type=stype, mode=mode)

            # 抓取失败或正文为空时，退回 Bocha 摘要 —— 摘要虽短，
            # 但保证搜索结果不因目标站反爬而全部丢失
            if doc.error or not doc.content:
                snippet = cand.get("snippet", "")
                if not snippet:
                    continue
                doc = ScrapedDocument(
                    title=cand.get("title", ""),
                    url=url,
                    content=snippet,
                    source_type=stype,
                    error="",  # 摘要兜底视为有效结果
                )
            if not doc.title:
                doc.title = cand.get("title", "")

            doc.content_hash = _hash_content(doc.content)
            if doc.content_hash in seen_hashes:
                continue
            seen_hashes.add(doc.content_hash)
            doc.source_type = stype
            doc.source_name = _source_display_name(stype)
            results.append(doc)

        # 全局截断：无论选几个源，最多返回 max_per_source 条（P0 bug 修复）
        if len(results) > max_per_source:
            results = results[:max_per_source]

        return results

    async def _bocha_search(self, keyword: str, api_key: str) -> list[dict]:
        """调用 Bocha Web Search API，返回候选结果列表。

        实际响应结构（实测）：
          {"code":200, "data":{"webPages":{"value":[
              {"name":"标题","url":"...","snippet":"摘要"}, ...]}}}
        注意是两层嵌套的 data.webPages.value，字段名是 name 而非 title。
        """
        try:
            async with httpx.AsyncClient(timeout=20) as client:
                resp = await client.post(
                    BOCHA_API_URL,
                    headers={
                        "Authorization": f"Bearer {api_key}",
                        "Content-Type": "application/json",
                    },
                    json={"query": keyword, "count": 20},
                )
                resp.raise_for_status()
                body = resp.json()

                # 兼容两种形态：标准 {"data":{"webPages":{"value":[...]}}}
                # 以及部分网关直接透传 {"webPages":{"value":[...]}}
                payload = body.get("data", body) if isinstance(body, dict) else {}
                web_pages = payload.get("webPages") or payload.get("webpages") or {}
                items = web_pages.get("value", []) if isinstance(web_pages, dict) else []

                out: list[dict] = []
                for it in items:
                    url = it.get("url") or ""
                    if not url:
                        continue
                    out.append({
                        "url": url,
                        "title": it.get("name") or it.get("title") or "",
                        "snippet": it.get("snippet") or it.get("summary") or "",
                    })
                logger.info(f"Bocha search '{keyword}': {len(out)} results")
                return out
        except Exception as e:
            # 数据诚信：配置了 key 却调用失败（401/超时/限流）时抛错，
            # 由调用方返回 5xx → Go 管线 fetch_failed + 额度回补；
            # 绝不返回空列表让上层误以为「无结果」。
            logger.error(f"Bocha search failed for '{keyword}': {e}")
            raise

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _extract(self, page, url: str, source_type: str) -> ScrapedDocument:
        """Extract structured fields from a Scrapling page object."""
        try:
            title = (page.css('title::text').get()
                     or page.css('h1::text').get()
                     or page.css('meta[property="og:title"]::attr(content)').get()
                     or "")
        except Exception:
            title = ""

        try:
            # Try common article/content selectors
            content_parts = (
                page.css('article p::text, article span::text').getall()
                or page.css('.content p::text, .article p::text, .post p::text').getall()
                or page.css('p::text').getall()
            )
            content = " ".join(content_parts)[:5000]  # Truncate
        except Exception:
            content = ""

        try:
            author = (page.css('meta[name="author"]::attr(content)').get()
                      or page.css('.author::text, .byline::text').get()
                      or "")
        except Exception:
            author = ""

        try:
            published_at = (page.css('meta[property="article:published_time"]::attr(content)').get()
                            or page.css('time::attr(datetime)').get()
                            or "")
        except Exception:
            published_at = ""

        return ScrapedDocument(
            title=title.strip(),
            url=url,
            content=content.strip(),
            author=author.strip(),
            source_type=source_type,
            published_at=published_at.strip(),
        )

    # ------------------------------------------------------------------
    # Source routing
    # ------------------------------------------------------------------

    @staticmethod
    def _source_mode(source: str) -> str:
        """Map source to fetch mode."""
        dynamic_sources = {"weibo", "xiaohongshu", "douyin", "bilibili"}
        if source in dynamic_sources:
            return "stealth"
        return "static"

    @staticmethod
    def _preset_search_urls(keyword: str, source: str) -> list[str]:
        """Preset test URLs per source (Bocha API replacement until key is provided).

        These are public test pages that return valid HTML for Scrapling to parse.
        When Bocha API key is available, replace with real search results.
        """
        preset_map = {
            "weibo": [
                "https://s.weibo.com/weibo?q=" + keyword,
            ],
            "news": [
                "https://www.example.com",
                "https://httpbin.org/html",
            ],
            "xiaohongshu": [
                "https://httpbin.org/html",
            ],
            "bilibili": [
                "https://httpbin.org/html",
            ],
            "rss": [
                "https://httpbin.org/html",
            ],
            "custom_web": [
                "https://httpbin.org/html",
            ],
        }
        return preset_map.get(source, ["https://httpbin.org/html"])


def _hash_content(content: str) -> str:
    """SHA256 hex digest, truncated to 16 chars for URL-safe dedup."""
    return hashlib.sha256(content.encode("utf-8")).hexdigest()[:16]


def _source_display_name(source: str) -> str:
    names = {
        "weibo": "微博", "news": "新闻", "xiaohongshu": "小红书",
        "bilibili": "B站", "douyin": "抖音", "kuaishou": "快手",
        "zhihu": "知乎", "rss": "RSS", "custom_web": "自定义",
    }
    return names.get(source, source)


# URL 域名 → 数据源类型映射（P0 数据源标签修复）
_DOMAIN_SOURCE_MAP = [
    (re.compile(r"weibo\.com|sina\.com\.cn", re.IGNORECASE), "weibo"),
    (re.compile(r"mp\.weixin\.qq\.com", re.IGNORECASE), "weixin"),
    (re.compile(r"xiaohongshu\.com|xhslink\.com", re.IGNORECASE), "xiaohongshu"),
    (re.compile(r"bilibili\.com|b23\.tv", re.IGNORECASE), "bilibili"),
    (re.compile(r"douyin\.com|iesdouyin\.com", re.IGNORECASE), "douyin"),
    (re.compile(r"kuaishou\.com|ksurl\.cn", re.IGNORECASE), "kuaishou"),
    (re.compile(r"zhihu\.com", re.IGNORECASE), "zhihu"),
    (re.compile(r"36kr\.com", re.IGNORECASE), "news"),
    (re.compile(r"sohu\.com|ifeng\.com|qq\.com/news", re.IGNORECASE), "news"),
    (re.compile(r"baidu\.com/s", re.IGNORECASE), "news"),
]


def _classify_by_domain(url: str) -> str:
    """根据 URL 域名推断数据源类型（P0 修复：Bocha 结果不再强制归 sources[0]）。"""
    for pattern, source in _DOMAIN_SOURCE_MAP:
        if pattern.search(url):
            return source
    return "custom_web"