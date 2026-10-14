"""Date-window contracts using actual query/report paths and offline responses."""
import json

import httpx
import pytest

from engines.common import scraper as scraper_module
from engines.query_engine import main as query
from engines.report_engine import main as report


def candidate(index, published):
    return {
        "url": f"https://weibo.com/date-fixture/{index}",
        "name": f"窗口品牌公开资料 {index}",
        "summary": f"窗口品牌公开资料 {index}：这是一条独立的公开网页摘要，说明本轮业务观察的范围与来源，供日期筛选契约使用。",
        "siteName": "微博",
        "datePublished": published,
    }


def provider(monkeypatch, values):
    calls = []

    def handle(request):
        assert request.url == scraper_module.BOCHA_API_URL
        calls.append(json.loads(request.content))
        return httpx.Response(200, json={"data": {"webPages": {"value": values}}})

    actual_client = httpx.AsyncClient
    monkeypatch.setattr(scraper_module.httpx, "AsyncClient", lambda **kwargs: actual_client(
        **{**kwargs, "transport": httpx.MockTransport(handle)}))
    monkeypatch.setattr(query, "get_scraper", lambda: scraper_module.PageScraper())
    return calls


@pytest.mark.asyncio
async def test_selected_calendar_window_reaches_bocha_http_request(monkeypatch):
    calls = provider(monkeypatch, [candidate(1, "2026-09-10"), candidate(2, "2026-09-20T23:59:59+08:00")])
    response = await query.search(query.SearchRequest(
        keywords=["窗口品牌"], sources=["weibo"], bocha_api_key="fixture-date-key",
        date_from="2026-09-10", date_to="2026-09-20",
    ))
    assert calls[0].get("freshness") == "2026-09-10..2026-09-20"
    assert calls[0]["include"] == "weibo.com"
    assert [item["url"] for item in response.documents] == [value["url"] for value in [candidate(1, ""), candidate(2, "")]]


@pytest.mark.asyncio
async def test_date_rejection_precedes_result_limit_and_preserves_boundary_samples(monkeypatch):
    values = [candidate(index, "2022-01-01") for index in range(6)] + [
        candidate(6, ""), candidate(7, "not-a-date"),
        candidate(8, "2026-09-10T00:00:00+08:00"), candidate(9, "2026-09-20T23:59:59+08:00"),
        candidate(10, "2026-09-21"),
    ]
    provider(monkeypatch, values)
    response = await query.search(query.SearchRequest(
        keywords=["窗口品牌"], sources=["weibo"], max_results=2, bocha_api_key="fixture-date-key",
        date_from="2026-09-10", date_to="2026-09-20",
    ))
    assert [item["url"] for item in response.documents] == [values[8]["url"], values[9]["url"]]
    assert response.total_count == 2
    assert response.coverage["unverifiable_date_count"] == 2
    assert response.coverage["out_of_range_date_count"] == 7
    assert response.coverage["date_filter"] == "partial"


@pytest.mark.asyncio
async def test_one_calendar_day_has_same_day_provider_and_local_bounds(monkeypatch):
    calls = provider(monkeypatch, [
        candidate(1, "2026-09-09"), candidate(2, "2026-09-10T00:00:00Z"),
        candidate(3, "2026-09-10T23:59:59+08:00"), candidate(4, "2026-09-11"),
    ])
    response = await query.search(query.SearchRequest(
        keywords=["窗口品牌"], sources=["weibo"], bocha_api_key="fixture-date-key",
        date_from="2026-09-10", date_to="2026-09-10",
    ))
    assert calls[0].get("freshness") == "2026-09-10"
    assert response.total_count == 2
    assert all(item["published_at"].startswith("2026-09-10") for item in response.documents)


@pytest.mark.asyncio
async def test_report_generation_preserves_selected_period_without_live_provider(monkeypatch):
    async def no_model(req):
        return None

    monkeypatch.setattr(report, "_llm_insight", no_model)
    generated = await report.generate(report.GenerateRequest(
        title="窗口品牌日期报告", date_from="2026-09-10", date_to="2026-09-20",
        documents=[{"id": "dated", "title": "窗口品牌资料", "content": "本轮资料", "published_at": "2026-09-10"}],
        insight_available=False,
    ))
    assert "分析时间范围" in generated.content
    assert "2026-09-10 至 2026-09-20" in generated.content
    assert "包含起止日期" in generated.content


@pytest.mark.asyncio
async def test_report_writer_prompt_is_scoped_to_selected_period(monkeypatch):
    prompts = []

    class Writer:
        async def chat_json(self, model, messages, **kwargs):
            prompts.append(messages[-1]["content"])
            return {}

    monkeypatch.setattr(report, "build_client", lambda *args, **kwargs: Writer())
    await report._llm_insight(report.GenerateRequest(
        title="窗口品牌日期报告", api_key="fixture-date-key", date_from="2026-09-10", date_to="2026-09-20",
    ))
    assert prompts and "2026-09-10 至 2026-09-20" in prompts[0]
    assert "历史背景" in prompts[0]
