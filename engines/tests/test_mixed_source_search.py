"""Source-union regression through the real query HTTP entrypoint."""
import json

import httpx
import pytest

from engines.common import scraper as scraper_module
from engines.query_engine import main as query


@pytest.mark.asyncio
@pytest.mark.parametrize("sources,expected_urls", [
    (["news", "douyin", "weibo", "wechat"], [
        "https://www.sohu.com/a/news-one", "https://feng.ifeng.com/c/news-two",
        "https://weibo.com/social-one",
    ]),
    (["news"], ["https://www.sohu.com/a/news-one", "https://feng.ifeng.com/c/news-two"]),
    (["douyin", "weibo", "wechat"], ["https://weibo.com/social-one"]),
])
async def test_selected_sources_form_union_without_bypassing_admission(monkeypatch, sources, expected_urls):
    # The supplier receives a restrictive domain list only for platform-only
    # selection. Mixing news with platforms must retain the news-only results.
    values = [
        ("https://www.sohu.com/a/news-one", "2026-09-16", "上市报道"),
        ("https://feng.ifeng.com/c/news-two", "2026-10-15T23:59:59+08:00", "独立试驾报道"),
        ("https://weibo.com/social-one", "2026-10-01", "公开讨论摘要"),
        ("https://www.toutiao.com/article/unselected", "2026-10-01", "未选择的平台"),
        ("https://unknown.example/unselected", "2026-10-01", "未知站点"),
        ("https://www.sohu.com/a/old", "2026-09-15", "范围外旧闻"),
        ("https://www.sohu.com/a/undated", "", "无法核验日期"),
    ]
    pages = [{"url": url, "name": f"越7 {label}", "summary": f"越7 {label}的公开网页摘要。",
              "datePublished": published, "siteName": "fixture"}
             for url, published, label in values]
    # Unrelated material remains rejected even when it has a selected domain.
    pages.append({"url": "https://weibo.com/unrelated", "name": "特斯拉涂装",
                  "summary": "一篇与所搜车型无关的公开文章。", "datePublished": "2026-10-12"})

    def supplier(request):
        body = json.loads(request.content)
        assert body["query"] == "越7"
        assert body["freshness"] == "2026-09-16..2026-10-15"
        assert body["summary"] is True
        included = body.get("include", "")
        # Reproduce Bocha's domain restriction at the external HTTP boundary.
        if included == "weibo.com":
            selected = [page for page in pages if "weibo.com/" in page["url"]]
        elif included:
            selected = []
        else:
            selected = pages
        return httpx.Response(200, json={"code": 200, "data": {"webPages": {"value": selected}}})

    actual_client = httpx.AsyncClient
    monkeypatch.setattr(scraper_module.httpx, "AsyncClient", lambda **kwargs: actual_client(
        **{**kwargs, "transport": httpx.MockTransport(supplier)}))
    monkeypatch.setattr(query, "get_scraper", scraper_module.PageScraper)
    async with actual_client(transport=httpx.ASGITransport(app=query.app), base_url="http://query") as client:
        response = await client.post("/search", json={
            "keywords": ["越7"], "sources": sources, "date_from": "2026-09-16", "date_to": "2026-10-15",
            "max_results": 50, "bocha_api_key": "fixture-key",
        })
    assert response.status_code == 200
    result = response.json()
    assert [doc["url"] for doc in result["documents"]] == expected_urls
    assert result["coverage"]["accepted_count"] == len(expected_urls)
    assert all(doc["source_type"] in sources for doc in result["documents"])
    if "news" in sources:
        assert result["coverage"]["out_of_range_date_count"] == 1
        assert result["coverage"]["unverifiable_date_count"] == 1
