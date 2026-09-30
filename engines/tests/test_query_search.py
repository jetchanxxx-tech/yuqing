"""Synthetic /search contract fixtures; not a live-source verification."""
import httpx
import pytest

from engines.common.scraper import ScrapedDocument
from engines.query_engine import main


class FixtureScraper:
    def __init__(self, documents):
        self.documents = documents

    async def search_and_fetch(self, **kwargs):
        return self.documents


class KeywordScraper:
    def __init__(self, results, failures=()):
        self.results = results
        self.failures = failures
        self.calls = []

    async def search_and_fetch(self, **kwargs):
        self.calls.append(kwargs)
        if kwargs["keyword"] in self.failures:
            raise RuntimeError("synthetic upstream failure")
        return self.results.get(kwargs["keyword"], [])[:kwargs["max_per_source"]]


def doc(content, published_at="2026-09-15T10:00:00Z", source_type="news"):
    return ScrapedDocument(content=content, published_at=published_at,
                           source_type=source_type, content_hash=content)


@pytest.mark.asyncio
async def test_exclude_words_match_original_content_case_insensitive(monkeypatch):
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([
        doc("含 噪声 的正文"), doc("Clean BODY"), doc("中文噪声原文"),
    ]))
    response = await main.search(main.SearchRequest(
        sources=["news"], exclude_words=[" 噪声 ", "body"], bocha_api_key="fixture-key",
    ))
    assert response.documents == []
    assert response.total_count == response.sources[0]["doc_count"] == 0
    assert response.coverage["scanned_count"] == 3
    assert response.coverage["excluded_word_count"] == 3
    assert response.coverage["excluded_word_ratio"] == 1.0
    assert "snippet" in response.coverage["filter_limitations"]


@pytest.mark.asyncio
async def test_date_window_includes_both_boundaries_and_excludes_unknown(monkeypatch):
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([
        doc("before", "2026-09-09T23:59:59Z"), doc("start", "2026-09-10"),
        doc("end", "2026-09-20T23:59:59+08:00"), doc("after", "2026-09-21"),
        doc("missing", ""), doc("malformed", "not-a-date"),
    ]))
    response = await main.search(main.SearchRequest(
        date_from="2026-09-10", date_to="2026-09-20", bocha_api_key="fixture-key",
    ))
    assert [d["content"] for d in response.documents] == ["start", "end"]
    assert response.total_count == response.sources[0]["doc_count"] == 2
    assert response.coverage["unverifiable_date_count"] == 2
    assert response.coverage["date_filter"] == "partial"
    assert "before filtering" in response.coverage["filter_limitations"]


@pytest.mark.asyncio
async def test_source_counts_reflect_only_returned_documents_after_limit(monkeypatch):
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([
        doc("keep-news"), doc("drop-noise"), doc("keep-weibo", source_type="weibo"),
        doc("over-limit", source_type="weibo"), doc("wrong-source", source_type="rss"),
    ]))
    response = await main.search(main.SearchRequest(
        sources=["news", "weibo"], max_results=2, exclude_words=["noise"],
        bocha_api_key="fixture-key",
    ))
    assert [d["content"] for d in response.documents] == ["keep-news", "keep-weibo"]
    assert response.total_count == 2
    assert [(s["name"], s["doc_count"]) for s in response.sources] == [
        ("news", 1), ("weibo", 1),
    ]


@pytest.mark.asyncio
async def test_old_request_defaults_keep_undated_documents(monkeypatch):
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([doc("old", "")]))
    response = await main.search(main.SearchRequest(bocha_api_key="fixture-key"))
    assert [d["content"] for d in response.documents] == ["old"]
    assert response.coverage["date_filter"] == "not_requested"


@pytest.mark.asyncio
async def test_duplicate_requested_source_does_not_double_count(monkeypatch):
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([doc("only")]))
    response = await main.search(main.SearchRequest(
        sources=["news", "news"], bocha_api_key="fixture-key",
    ))
    assert response.total_count == 1
    assert [(s["name"], s["doc_count"]) for s in response.sources] == [("news", 1)]


@pytest.mark.asyncio
async def test_source_not_requested_cannot_enter_returned_documents(monkeypatch):
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([
        doc("unrequested", source_type="rss"), doc("requested"),
    ]))
    response = await main.search(main.SearchRequest(bocha_api_key="fixture-key"))
    assert [d["content"] for d in response.documents] == ["requested"]
    assert response.total_count == response.sources[0]["doc_count"] == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("bounds", [
    {"date_from": "bad"}, {"date_to": "2026-09-31"},
    {"date_from": "2026-09-21", "date_to": "2026-09-20"},
])
async def test_invalid_window_rejected_before_fetch(monkeypatch, bounds):
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([]))
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app), base_url="http://test") as client:
        response = await client.post("/search", json={**bounds, "bocha_api_key": "fixture-key"})
    assert response.status_code == 422


@pytest.mark.asyncio
async def test_missing_real_search_key_never_falls_back_to_preset_urls(monkeypatch):
    monkeypatch.setattr(main, "DEFAULT_BOCHA_KEY", "")
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([]))
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app), base_url="http://test") as client:
        response = await client.post("/search", json={"keywords": ["example"]})
    assert response.status_code == 503


@pytest.mark.asyncio
async def test_all_distinct_nonempty_keywords_search_and_deduplicate_eligible_results(monkeypatch):
    shared = doc("same URL original")
    shared.url = "https://example.test/post/1"
    url_duplicate = doc("different excerpt")
    url_duplicate.url = shared.url
    id_duplicate = doc("same URL original")
    id_duplicate.url = "https://example.test/post/2"
    second = doc("second original")
    second.url = "https://example.test/post/3"
    scraper = KeywordScraper({
        "first": [shared, doc("exclude-noise"), doc("old", "2026-09-01")],
        "second": [url_duplicate, id_duplicate, second],
    })
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    response = await main.search(main.SearchRequest(
        keywords=[" ", "first", "second", "first", ""], max_results=6,
        exclude_words=["noise"], date_from="2026-09-10", bocha_api_key="fixture-key",
    ))
    assert [call["keyword"] for call in scraper.calls] == ["first", "second"]
    assert all(call["bocha_key"] == "fixture-key" for call in scraper.calls)
    assert [item["content"] for item in response.documents] == ["same URL original", "second original"]
    assert response.total_count == response.sources[0]["doc_count"] == 2
    assert response.coverage["scanned_count"] == 6
    assert response.coverage["duplicate_count"] == 2
    assert response.coverage["keywords_searched"] == ["first", "second"]


@pytest.mark.asyncio
@pytest.mark.parametrize(("first_date", "first_content", "exclude_words", "missing_dates", "excluded"), [
    ("2026-09-01", "same original", [], 0, 0),
    ("", "same original", [], 1, 0),
    ("2026-09-15", "noise excerpt", ["noise"], 0, 1),
])
async def test_filtered_copy_cannot_hide_later_eligible_url(
    monkeypatch, first_date, first_content, exclude_words, missing_dates, excluded,
):
    first = doc(first_content, first_date)
    valid = doc("clean original", "2026-09-15")
    repeat = doc("clean original", "2026-09-15")
    for item in (first, valid, repeat):
        item.url = "https://example.test/original"
        item.content_hash = "same-source-id"
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([first, valid, repeat]))

    response = await main.search(main.SearchRequest(
        date_from="2026-09-10", date_to="2026-09-20", exclude_words=exclude_words,
        bocha_api_key="fixture-key",
    ))

    assert [item["content"] for item in response.documents] == ["clean original"]
    assert response.coverage["scanned_count"] == 3
    assert response.coverage["duplicate_count"] == 1
    assert response.coverage["unverifiable_date_count"] == missing_dates
    assert response.coverage["excluded_word_count"] == excluded


@pytest.mark.asyncio
async def test_excluded_copy_cannot_hide_later_eligible_content_hash(monkeypatch):
    first = doc("noise excerpt")
    valid = doc("clean excerpt")
    repeat = doc("clean excerpt")
    for item, suffix in zip((first, valid, repeat), ("filtered", "valid", "repeat")):
        item.url = f"https://example.test/{suffix}"
        item.content_hash = "same-source-id"
    monkeypatch.setattr(main, "get_scraper", lambda: FixtureScraper([first, valid, repeat]))

    response = await main.search(main.SearchRequest(
        exclude_words=["noise"], bocha_api_key="fixture-key",
    ))

    assert [item["url"] for item in response.documents] == ["https://example.test/valid"]
    assert response.coverage["excluded_word_count"] == 1
    assert response.coverage["duplicate_count"] == 1


@pytest.mark.asyncio
async def test_bounded_per_keyword_results_and_response_truncation_are_visible(monkeypatch):
    scraper = KeywordScraper({
        "first": [doc(f"first-{i}") for i in range(20)],
        "second": [doc(f"second-{i}") for i in range(20)],
    })
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    response = await main.search(main.SearchRequest(
        keywords=["first", "second"], max_results=3, bocha_api_key="fixture-key",
    ))
    assert len(scraper.calls) == 2
    assert all(1 <= call["max_per_source"] <= 3 for call in scraper.calls)
    assert response.total_count == 3
    assert response.coverage["scanned_count"] == 4
    assert response.coverage["truncated_count"] == 1
    assert "before filtering" in response.coverage["filter_limitations"]


@pytest.mark.asyncio
async def test_partial_keyword_failure_returns_data_and_explicit_warning(monkeypatch):
    scraper = KeywordScraper({"good": [doc("real fixture")]}, failures={"bad"})
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    response = await main.search(main.SearchRequest(
        keywords=["bad", "good"], bocha_api_key="fixture-key",
    ))
    assert [call["keyword"] for call in scraper.calls] == ["bad", "good"]
    assert response.total_count == 1
    assert response.coverage["failed_keywords"] == ["bad"]
    assert response.coverage["keyword_search"] == "partial"
    assert "bad" in response.coverage["warning"]
    assert "synthetic upstream failure" not in response.coverage["warning"]


@pytest.mark.asyncio
async def test_all_keyword_failures_never_report_successful_zero_results(monkeypatch):
    scraper = KeywordScraper({}, failures={"bad", "also-bad"})
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app), base_url="http://test") as client:
        response = await client.post("/search", json={
            "keywords": ["bad", "also-bad"], "bocha_api_key": "fixture-key",
        })
    assert [call["keyword"] for call in scraper.calls] == ["bad", "also-bad"]
    assert response.status_code == 502
    assert "synthetic upstream failure" not in response.text
    assert response.json()["detail"]["failed_keywords"] == ["bad", "also-bad"]


@pytest.mark.asyncio
async def test_single_keyword_failure_is_not_a_successful_empty_search(monkeypatch):
    monkeypatch.setattr(main, "get_scraper", lambda: KeywordScraper({}, failures={"bad"}))
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app), base_url="http://test") as client:
        response = await client.post("/search", json={"keywords": ["bad"], "bocha_api_key": "fixture-key"})
    assert response.status_code == 502


@pytest.mark.asyncio
async def test_too_many_nonempty_keywords_rejected_not_silently_skipped(monkeypatch):
    scraper = KeywordScraper({})
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app), base_url="http://test") as client:
        response = await client.post("/search", json={
            "keywords": [f"word-{i}" for i in range(9)], "bocha_api_key": "fixture-key",
        })
    assert response.status_code == 422
    assert scraper.calls == []


@pytest.mark.asyncio
async def test_single_keyword_old_request_still_uses_original_per_source_limit(monkeypatch):
    scraper = KeywordScraper({"only": [doc("retained")]})
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    response = await main.search(main.SearchRequest(
        keywords=["only"], sources=["news", "weibo"], max_results=20,
        bocha_api_key="fixture-key",
    ))
    assert len(scraper.calls) == 1
    assert scraper.calls[0]["max_per_source"] == 10
    assert response.total_count == 1


@pytest.mark.asyncio
async def test_large_result_request_is_capped_and_reports_the_cap(monkeypatch):
    scraper = KeywordScraper({"only": [doc(f"item-{i}") for i in range(120)]})
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    response = await main.search(main.SearchRequest(
        keywords=["only"], max_results=500, bocha_api_key="fixture-key",
    ))
    assert scraper.calls[0]["max_per_source"] <= 100
    assert response.total_count <= 100
    assert response.coverage["result_limit"] == 100
    assert response.coverage["requested_max_results"] == 500
    assert "limit" in response.coverage["warning"].lower()


@pytest.mark.asyncio
async def test_unrequested_source_does_not_hide_requested_same_original_id(monkeypatch):
    scraper = KeywordScraper({
        "first": [doc("same id", source_type="rss")],
        "second": [doc("same id", source_type="news")],
    })
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    response = await main.search(main.SearchRequest(
        keywords=["first", "second"], sources=["news"], bocha_api_key="fixture-key",
    ))
    assert response.total_count == 1
    assert response.documents[0]["source_type"] == "news"


@pytest.mark.asyncio
@pytest.mark.parametrize("source", ["xiaohongshu", "bilibili", "unknown"])
async def test_unavailable_source_rejected_before_provider_call(monkeypatch, source):
    scraper = KeywordScraper({})
    monkeypatch.setattr(main, "get_scraper", lambda: scraper)
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=main.app), base_url="http://test") as client:
        response = await client.post("/search", json={"sources": [source], "bocha_api_key": "fixture-key"})
    assert response.status_code == 422
    assert scraper.calls == []
