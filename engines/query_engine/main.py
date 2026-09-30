"""Query Engine — Bocha AI search + Scrapling content fetching."""
from datetime import date, datetime
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from engines.common.scraper import PageScraper, ScrapedDocument, BOCHA_API_KEY as DEFAULT_BOCHA_KEY
from engines.common.relevance import ADMISSION_VERSION

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
    coverage: dict


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
    """抓取后按原文和可核验的发布日期筛选，再返回可入库的原文。"""
    bocha_key = req.bocha_api_key or DEFAULT_BOCHA_KEY
    if not bocha_key:
        raise HTTPException(status_code=503, detail="Bocha API key required; preset test URLs are not real sources")

    keywords = list(dict.fromkeys(word.strip() for word in req.keywords if word.strip())) or [""]
    if len(keywords) > 8 or req.max_results < 1:
        raise HTTPException(status_code=422, detail="At most 8 keywords and a positive max_results are required")
    result_limit = min(req.max_results, 100)

    try:
        date_from = date.fromisoformat(req.date_from) if req.date_from else None
        date_to = date.fromisoformat(req.date_to) if req.date_to else None
    except ValueError as exc:
        raise HTTPException(status_code=422, detail="date_from/date_to must be ISO dates") from exc
    if date_from and date_to and date_from > date_to:
        raise HTTPException(status_code=422, detail="date_from must not exceed date_to")

    scraper = get_scraper()
    sources = list(dict.fromkeys(req.sources))
    allowed = {"douyin", "toutiao", "xigua", "weibo", "wechat", "news"}
    if not sources or any(source not in allowed for source in sources):
        raise HTTPException(status_code=422, detail="Requested source is unavailable")
    per_source = (max(result_limit // max(len(sources), 1), 3) if len(keywords) == 1
                  else min(20, max(1, (result_limit + len(keywords) - 1) // len(keywords))))
    results: list[ScrapedDocument] = []
    failed_keywords: list[str] = []
    per_keyword: list[dict] = []
    for keyword in keywords:
        stats: dict = {}
        try:
            found = await scraper.search_and_fetch(
                keyword=keyword,
                sources=sources,
                max_per_source=per_source,
                bocha_key=bocha_key,
                coverage=stats,
            )
            results.extend(found)
            per_keyword.append({"keyword": keyword, "status": "ok", **stats,
                                "returned_count": len(found)})
        except Exception:
            failed_keywords.append(keyword)
            per_keyword.append({"keyword": keyword, "status": "failed", "returned_count": 0})
    if len(failed_keywords) == len(keywords):
        raise HTTPException(status_code=502, detail={
            "message": "All keyword searches failed; no results accepted",
            "failed_keywords": failed_keywords,
        })

    exclude_words = [word.strip().casefold() for word in req.exclude_words if word.strip()]
    docs: list[ScrapedDocument] = []
    unverifiable_date_count = 0
    excluded_word_count = 0
    duplicate_count = 0
    seen_urls: set[tuple[str, str]] = set()
    seen_ids: set[str] = set()
    for d in results:
        if d.source_type not in sources:
            continue
        if any(word in d.content.casefold() for word in exclude_words):
            excluded_word_count += 1
            continue
        if date_from or date_to:
            try:
                published_date = datetime.fromisoformat(d.published_at).date()
            except ValueError:
                unverifiable_date_count += 1
                continue
            if (date_from and published_date < date_from) or (date_to and published_date > date_to):
                continue
        url_key = (d.source_type, d.url)
        if (d.url and url_key in seen_urls) or (d.content_hash and d.content_hash in seen_ids):
            duplicate_count += 1
            continue
        if d.url:
            seen_urls.add(url_key)
        if d.content_hash:
            seen_ids.add(d.content_hash)
        docs.append(d)

    doc_dicts = [_doc_to_dict(d) for d in docs[:result_limit]]
    source_stats: dict[str, int] = {}
    for d in doc_dicts:
        source_stats[d["source_type"]] = source_stats.get(d["source_type"], 0) + 1

    sources_info = [
        {"name": src, "doc_count": source_stats.get(src, 0),
         "status": "ok" if source_stats.get(src, 0) > 0 else "partial"}
        for src in sources
    ]

    return SearchResponse(
        documents=doc_dicts,
        total_count=len(doc_dicts),
        sources=sources_info,
        coverage={
            "admission_version": ADMISSION_VERSION,
            "provider_candidates": sum(row.get("provider_candidates", row.get("returned_count", 0)) for row in per_keyword),
            "unusable_count": sum(row.get("unusable_count", 0) for row in per_keyword),
            "irrelevant_count": sum(row.get("irrelevant_count", 0) for row in per_keyword),
            "source_mismatch_count": sum(row.get("source_mismatch_count", 0) for row in per_keyword),
            "candidate_truncated": any(row.get("candidate_truncated", False) for row in per_keyword),
            "accepted_count": len(doc_dicts),
            "per_keyword": per_keyword,
            "keywords_searched": keywords,
            "failed_keywords": failed_keywords,
            "keyword_search": "partial" if failed_keywords else "applied",
            "requested_max_results": req.max_results,
            "result_limit": result_limit,
            "truncated_count": max(0, len(docs) - result_limit),
            "duplicate_count": duplicate_count,
            "date_filter": ("partial" if unverifiable_date_count else "applied")
            if date_from or date_to else "not_requested",
            "unverifiable_date_count": unverifiable_date_count,
            "scanned_count": len(results),
            "excluded_word_count": excluded_word_count,
            "excluded_word_ratio": excluded_word_count / len(results) if results else 0.0,
            "filter_limitations": (
                "Filtering uses Bocha summary/snippet excerpts, not full original text or comments; "
                "candidate results are capped before filtering for each keyword, so "
                "matching documents may be missed. Platform scope is limited by domain."
            ) if exclude_words or date_from or date_to or len(keywords) > 1 else "",
            "warning": "; ".join(message for message in [
                "Documents with missing/unparseable published_at excluded; date coverage incomplete"
                if unverifiable_date_count else "",
                "Keyword searches failed: " + ", ".join(failed_keywords) if failed_keywords else "",
                "Requested result limit capped at 100" if req.max_results > result_limit else "",
            ] if message),
        },
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
