"""Source union and semantic admission through the authenticated HTTP route."""
import json
import httpx
import pytest

from engines.common import scraper as module
from engines.query_engine import main as query
from engines.tests.test_query_semantic_http import setup_pipeline, accepted_request, post_search, TopicModel


@pytest.mark.asyncio
@pytest.mark.parametrize("sources,expected_urls", [
    (["news", "douyin", "weibo", "wechat"], {"https://www.sohu.com/a/news-one", "https://feng.ifeng.com/c/news-two", "https://weibo.com/social-one"}),
    (["news"], {"https://www.sohu.com/a/news-one", "https://feng.ifeng.com/c/news-two"}),
    (["douyin", "weibo", "wechat"], {"https://weibo.com/social-one"}),
])
async def test_selected_sources_form_union_without_bypassing_admission(monkeypatch, sources, expected_urls):
    def decisions(items):
        return [{"document_id": item["document_id"], "classification": "irrelevant" if "特斯拉" in item["content"] else "direct",
                 "reason": "研究对象不同" if "特斯拉" in item["content"] else "越7产品反馈",
                 "evidence_spans": [] if "特斯拉" in item["content"] else [item["content"]]} for item in items]
    class CarTopicModel(TopicModel):
        async def chat_json(self, model, messages, **kwargs):
            if kwargs.get("usage_context", {}).get("phase") == "plan":
                self.calls.append((messages, kwargs))
                return {"topic": "越7车型产品反馈", "entities": ["越7"], "focus": ["体验与反馈"],
                        "search_queries": ["越7"]}
            return await super().chat_json(model, messages, **kwargs)
    setup_pipeline(monkeypatch, {}, CarTopicModel(decisions))
    values = [
        ("https://www.sohu.com/a/news-one", "2026-09-16", "上市报道"),
        ("https://feng.ifeng.com/c/news-two", "2026-10-15T23:59:59+08:00", "独立试驾报道"),
        ("https://weibo.com/social-one", "2026-10-01", "公开讨论摘要"),
        ("https://www.toutiao.com/article/unselected", "2026-10-01", "未选择的平台"),
        ("https://unknown.example/unselected", "2026-10-01", "未知站点"),
        ("https://www.sohu.com/a/old", "2026-09-15", "范围外旧闻"),
        ("https://www.sohu.com/a/undated", "", "无法核验日期"),
    ]
    pages = [{"url": url, "name": f"越7 {label}", "summary": f"越7 {label}的公开网页摘要，提供用户体验观察和反馈。",
              "datePublished": published, "siteName": "fixture"} for url, published, label in values]
    pages.append({"url": "https://weibo.com/unrelated", "name": "特斯拉涂装", "summary": "特斯拉涂装讨论，与本次越7车型研究不相关。", "datePublished": "2026-10-12"})
    calls = []
    def supplier(request):
        body = json.loads(request.content)
        calls.append(body)
        assert body["query"] == "越7"
        assert body["freshness"] == "2026-09-16..2026-10-15"
        assert body["summary"] is True
        include = body.get("include", "")
        selected = [page for page in pages if "weibo.com/" in page["url"]] if include == "weibo.com|weibo.cn" else [] if include else pages
        return httpx.Response(200, json={"data": {"webPages": {"value": selected}}})
    client = httpx.AsyncClient
    monkeypatch.setattr(module.httpx, "AsyncClient", lambda **kwargs: client(**{**kwargs, "transport": httpx.MockTransport(supplier)}))
    scraper = module.PageScraper()
    monkeypatch.setattr(scraper, "fetch_candidate", lambda *args, **kwargs: module.FetchOutcome(reason="fixture_body_unavailable"))
    monkeypatch.setattr(query, "get_scraper", lambda: scraper)
    response = await post_search(accepted_request(keywords=["越7"], sources=sources,
        date_from="2026-09-16", date_to="2026-10-15", bocha_api_key="fixture-key"))
    assert response.status_code == 200, response.text
    result = response.json()
    assert {item["url"] for item in result["documents"]} == expected_urls
    assert result["coverage"]["accepted_count"] == len(expected_urls)
    assert all(item["source_type"] in sources for item in result["documents"])
    assert len(calls) == len(sources)
    if "news" in sources:
        assert result["coverage"]["out_of_range_date_count"] == 1
        assert result["coverage"]["unverifiable_date_count"] == 1
