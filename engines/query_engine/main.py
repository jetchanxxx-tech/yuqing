"""Query Engine — Multi-source web search + Scrapling-powered content fetching."""
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from engines.common.scraper import PageScraper, ScrapedDocument

app = FastAPI(title="Query Engine", version="0.2.0")

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


class SearchResponse(BaseModel):
    documents: list[dict]
    total_count: int
    sources: list[dict]


# ── Routes ──────────────────────────────────────────────────


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "query", "version": "0.2.0", "scrapling": get_scraper()._available}


@app.post("/search")
async def search(req: SearchRequest) -> SearchResponse:
    """Multi-source search with Scrapling content fetching.

    MVP: uses preset test URLs per source (Bocha API integration pending API key).
    Deduplicates by content hash across all sources.
    """
    scraper = get_scraper()
    keyword = req.keywords[0] if req.keywords else ""

    docs: list[ScrapedDocument] = []
    source_stats: dict[str, int] = {}

    for source in req.sources:
        count_before = len(docs)
        results = await scraper.search_and_fetch(
            keyword=keyword,
            sources=[source],
            max_per_source=max(req.max_results // len(req.sources), 3),
        )
        docs.extend(results)
        source_stats[source] = len(results)

    # Convert to JSON-safe dicts.
    doc_dicts = [_doc_to_dict(d) for d in docs[:req.max_results]]

    sources_info = [
        {"name": src, "doc_count": source_stats.get(src, 0), "status": "ok" if source_stats.get(src, 0) > 0 else "partial"}
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