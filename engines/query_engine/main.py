"""Query Engine — Bocha AI search + Scrapling content fetching."""
import os
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from engines.common.scraper import PageScraper, ScrapedDocument, BOCHA_API_KEY as DEFAULT_BOCHA_KEY

app = FastAPI(title="Query Engine", version="0.3.0")

# Singleton scraper (lazy init).
_scraper: PageScraper | None = None


def get_scraper() -> PageScraper:
    global _scraper
    if _scraper is None:
        _scraper = PageScraper(headless=True, timeout=30)
    return _scraper


# ── Request / Response models ──────────────────────────────


class SearchRequest(BaseModel):
    keywords: list[str] = [""]
    sources: list[str] = ["news"]
    max_results: int = 20
    date_from: str = ""
    date_to: str = ""
    analysis_id: str = ""
    exclude_words: list[str] = []
    bocha_api_key: str = ""  # 空则从环境变量 BOCHA_API_KEY 读取


class SearchResponse(BaseModel):
    documents: list[dict]
    total_count: int
    sources: list[dict]


# ── Routes ──────────────────────────────────────────────────


@app.get("/health")
async def health():
    return {
        "status": "ok",
        "engine": "query",
        "version": "0.3.0",
        "scrapling": get_scraper()._available,
        "bocha": bool(DEFAULT_BOCHA_KEY),
    }


@app.post("/search")
async def search(req: SearchRequest) -> SearchResponse:
    """Bocha API 搜索 → Scrapling 抓取正文 → 去重 → 返回 Document 列表。

    当 Bocha key 不可用时自动降级到预设 test URL。
    """
    scraper = get_scraper()
    keyword = req.keywords[0] if req.keywords else ""
    bocha_key = req.bocha_api_key or DEFAULT_BOCHA_KEY

    docs: list[ScrapedDocument] = []
    source_stats: dict[str, int] = {}

    per_source = max(req.max_results // max(len(req.sources), 1), 3)
    results = await scraper.search_and_fetch(
        keyword=keyword,
        sources=req.sources,
        max_per_source=per_source,
        bocha_key=bocha_key,
    )

    # Count by source
    for d in results:
        source_stats[d.source_type] = source_stats.get(d.source_type, 0) + 1
    docs = results

    doc_dicts = [_doc_to_dict(d) for d in docs[:req.max_results]]

    sources_info = [
        {"name": src, "doc_count": source_stats.get(src, 0),
         "status": "ok" if source_stats.get(src, 0) > 0 else "partial"}
        for src in req.sources
    ]

    return SearchResponse(
        documents=doc_dicts,
        total_count=len(doc_dicts),
        sources=sources_info,
    )


def _doc_to_dict(doc: ScrapedDocument) -> dict:
    return {
        "id": doc.content_hash or "",
        "title": doc.title,
        "url": doc.url,
        "content": doc.content,
        "author": doc.author,
        "source_type": doc.source_type,
        "source_name": doc.source_name,
        "published_at": doc.published_at,
        "content_hash": doc.content_hash,
        "media_url": doc.media_url,
    }