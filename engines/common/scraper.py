"""
Scrapling-powered web scraper for 盘古舆情.
Wraps Scrapling's adaptive fetching + Bocha AI search with dedup and error handling.

Requirements: pip install scrapling[fetchers] httpx
"""
import hashlib
import logging
import os
from typing import Optional
from dataclasses import dataclass, field

import httpx

logger = logging.getLogger(__name__)

# ── Bocha API config ───────────────────────────────────────
BOCHA_API_URL = "https://api.bochaai.com/v1/ai/search"
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

        # 1. Search: use Bocha API if key available, else preset URLs
        all_urls: dict[str, list[str]] = {}  # source → [urls]
        key = bocha_key or BOCHA_API_KEY

        if key:
            urls = await self._bocha_search(keyword, key)
            # Assign all Bocha results to first source (Bocha returns universal URLs)
            if sources:
                all_urls[sources[0]] = urls
        else:
            # Fallback: preset test URLs
            for source in sources:
                all_urls[source] = self._preset_search_urls(keyword, source)

        # 2. Fetch + extract + dedup
        for source in sources:
            urls = all_urls.get(source, [])[:max_per_source]
            if not urls:
                continue
            mode = self._source_mode(source)
            for url in urls:
                doc = self.fetch(url, source_type=source, mode=mode)
                if doc.error:
                    continue
                doc.content_hash = _hash_content(doc.content)
                if doc.content_hash in seen_hashes:
                    continue
                seen_hashes.add(doc.content_hash)
                doc.source_type = source
                doc.source_name = _source_display_name(source)
                results.append(doc)

        return results

    async def _bocha_search(self, keyword: str, api_key: str) -> list[str]:
        """Call Bocha AI search API, return URLs."""
        try:
            async with httpx.AsyncClient(timeout=15) as client:
                resp = await client.post(
                    BOCHA_API_URL,
                    headers={
                        "Authorization": f"Bearer {api_key}",
                        "Content-Type": "application/json",
                    },
                    json={"query": keyword, "count": 20},
                )
                resp.raise_for_status()
                data = resp.json()
                webpages = data.get("webpages", []) or data.get("results", [])
                return [r.get("url", "") for r in webpages if r.get("url")]
        except Exception as e:
            logger.warning(f"Bocha search failed for '{keyword}': {e}")
            return []

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