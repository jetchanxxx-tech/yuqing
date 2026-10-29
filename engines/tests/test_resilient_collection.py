"""Supplier-independent collection and raw-material delivery regressions.

HTTP doubles replace public/provider networks only; planning, hard filters,
candidate scheduling, tool authorization and the durable outbox remain real.
"""
import json

import httpx
import pytest

from engines.common.scraper import FetchOutcome
from engines.query_engine import main as query


@pytest.mark.asyncio
async def test_public_discovery_returns_news_and_existing_social_links_without_supplier_keys(monkeypatch):
    from engines.common.discovery import discover_public

    for name in ["BOCHA_API_KEY", "DEEPSEEK_API_KEY", "LLM_API_KEY", "RSSHUB_URL"]:
        monkeypatch.delenv(name, raising=False)

    def public_network(request):
        if request.url.host == "www.chinanews.com.cn":
            return httpx.Response(200, text='''<?xml version="1.0"?><rss><channel>
                <item><title>手机新品用户体验</title><link>https://www.chinanews.com.cn/cj/2026/10-30/100001.shtml</link>
                <pubDate>Fri, 30 Oct 2026 03:00:00 GMT</pubDate><description>公开新闻入口材料</description></item>
                <item><title>非法目标</title><link>http://127.0.0.1/private</link></item>
                </channel></rss>''', headers={"content-type": "application/xml"})
        if request.url.host == "www.toutiao.com":
            return httpx.Response(200, json={"data": [{"Title": "手机新品公开讨论", "Url": "https://www.toutiao.com/article/760001/", "PublishTime": 1793329200}]})
        raise AssertionError(f"unexpected independent entry {request.url}")

    result = await discover_public(keywords=["手机"], sources=["news", "toutiao"], transport=httpx.MockTransport(public_network))
    assert {row["source_type"] for row in result["candidates"]} == {"news", "toutiao"}
    assert all(row["discovery_chain"][0]["channel"] == "local_discovery" for row in result["candidates"])
    assert all("127.0.0.1" not in row["url"] for row in result["candidates"])
    assert 0 < result["pages_visited"] <= 60


@pytest.mark.asyncio
async def test_resilient_missing_model_keeps_hard_filtered_raw_materials_as_uncertain(monkeypatch):
    for name in ["BOCHA_API_KEY", "DEEPSEEK_API_KEY", "LLM_API_KEY"]:
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setattr(query, "DEFAULT_BOCHA_KEY", "")
    snapshots = []

    async def discover(**_kwargs):
        return {"pages_visited": 1, "channels": [{"source": "news", "channel": "local_discovery", "status": "ok", "discovered_count": 4}], "candidates": [
            {"url": "https://www.chinanews.com.cn/cj/2026/10-30/100001.shtml", "title": "手机公开体验", "published_at": "2026-10-30T03:00:00Z", "source_type": "news", "discovery_chain": [{"channel": "local_discovery", "url": "https://www.chinanews.com.cn/rss/scroll-news.xml"}]},
            {"url": "https://www.chinanews.com.cn/cj/2026/10-01/100002.shtml", "title": "手机旧文", "published_at": "2026-10-01T03:00:00Z", "source_type": "news"},
            {"url": "https://www.toutiao.com/article/760001/", "title": "未选来源", "published_at": "2026-10-30T03:00:00Z", "source_type": "toutiao"},
            {"url": "https://www.chinanews.com.cn/cj/2026/10-30/100003.shtml", "title": "推广内容", "published_at": "2026-10-30T03:00:00Z", "source_type": "news"},
        ]}

    class LocalScraper:
        async def fetch_candidate(self, url, source, mode="static", **_kwargs):
            body = "推广广告 " if url.endswith("100003.shtml") else ""
            return FetchOutcome(content=body + "手机用户公开体验记录，来源网页包含续航、拍照和日常使用体验，资料仍需进行主题相关性核验。", final_url=url, status="fetched", content_kind="body")

        def _source_mode(self, _source):
            return "static"

        async def _bocha_search(self, *_args, **_kwargs):
            raise AssertionError("independent collection cannot require supplier search")

    async def checkpoint(_req, coverage):
        snapshots.append(json.loads(json.dumps(coverage)))

    monkeypatch.setattr(query, "discover_public", discover)
    monkeypatch.setattr(query, "get_scraper", lambda: LocalScraper())
    monkeypatch.setattr(query, "persist_candidate_audit", checkpoint)
    monkeypatch.setattr(query, "build_client", lambda *_args, **_kwargs: (_ for _ in ()).throw(AssertionError("missing model must not be called")))
    req = query.SearchRequest(analysis_id="analysis", run_id="run", resilient=True, keywords=["手机"], sources=["news"], date_from="2026-10-30", date_to="2026-10-30", exclude_words=["推广"])
    query._validate(req)
    response = await query._execute(req)
    assert response.total_count == 1
    assert response.documents[0]["semantic_classification"] == "uncertain"
    assert response.documents[0]["content_kind"] == "body"
    assert response.coverage["delivery_hint"] == "brief"
    assert response.coverage["direct_count"] == response.coverage["context_count"] == 0
    assert response.coverage["planning_status"] == "basic_unenhanced"
    assert {row["reason"] for row in response.coverage["candidate_audit"] if row["status"] == "filtered"} >= {"date_out_of_range", "source_mismatch", "excluded_word"}
    assert snapshots[-1]["phase"] == "completed"


@pytest.mark.asyncio
async def test_deepseek_search_counts_operators_maps_results_and_separates_tool_outbox(monkeypatch, tmp_path):
    from engines.common.deepseek_tools import DeepSeekTools
    from engines.common.tool_usage import deliver_pending_tool_events

    tool_dir = tmp_path / "tools"
    token_dir = tmp_path / "tokens"
    monkeypatch.setenv("YUQING_TOOL_USAGE_OUTBOX_DIR", str(tool_dir))
    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(token_dir))
    monkeypatch.setenv("YUQING_BILLING_URL", "http://127.0.0.1:18080")
    monkeypatch.setenv("YUQING_BILLING_SERVICE_TOKEN", "isolated-tool-secret")
    provider_payloads, events = [], []

    def network(request):
        body = json.loads(request.content)
        if request.url.path.endswith("tool-authorizations"):
            assert body["run_id"] == "accepted-run"
            assert body["operation"] in {"search", "fetch"}
            assert len(body["request_hash"]) == 64
            return httpx.Response(200, json={"call_id": body["call_id"], "permit": "tool-permit", "expires_at": "2099-01-01T00:00:00Z", "duplicate": False})
        if request.url.path.endswith("tool-usage-events"):
            assert request.headers["X-Tool-Call-Permit"] == "tool-permit"
            events.append(body)
            return httpx.Response(201, json={"committed": True})
        provider_payloads.append(body)
        assert list(tool_dir.glob("*.json")), "intent must be durable before supplier spend"
        if request.url.path == "/v1/tools/search":
            return httpx.Response(200, json={"results": [{"title": "公开报道", "url": "https://www.chinanews.com.cn/cj/100001.shtml", "date": "2026-10-29T17:00:00Z", "content": "供应商返回的正文版本必须标记来源。", "snippet": "这是摘要。"}], "id": "provider-search-id"})
        if request.url.path == "/v1/tools/fetch":
            return httpx.Response(200, json={"title": "正文标题", "content": "# Markdown\n这是供应商正文。"})
        raise AssertionError(str(request.url))

    transport = httpx.MockTransport(network)
    client = DeepSeekTools("sandbox-key", transport=transport, billing_transport=transport)
    rows = await client.search("手机" * 150, source="news", date_from="2026-10-30", date_to="2026-10-30", run_id="accepted-run", count=99)
    assert rows[0]["published_at"] == "2026-10-29T17:00:00Z"
    assert rows[0]["content_kind"] == "provider_body"
    for body in provider_payloads:
        assert len(body["query"]) <= 128
        assert body["count"] == 50 and body["need_content"] is True
        assert "include" not in body and "freshness" not in body
        assert "after:" in body["query"] and "before:" in body["query"]
    fetched = await client.fetch(rows[0]["url"], run_id="accepted-run")
    assert fetched["content"].startswith("# Markdown")
    assert fetched.get("published_at", "") == fetched.get("final_url", "") == ""
    assert not token_dir.exists(), "tool requests must never create token-ledger records"
    assert await deliver_pending_tool_events(transport=transport) == len(provider_payloads)
    assert {event["operation"] for event in events} == {"search", "fetch"}
    assert all(event["usage_status"] == "reported" and "prompt_tokens" not in event for event in events)
    assert list(tool_dir.glob("*.json")) == []


@pytest.mark.asyncio
async def test_tool_authorization_failure_never_spends_or_becomes_local_semantic_success(monkeypatch, tmp_path):
    from engines.common.deepseek_tools import DeepSeekTools

    monkeypatch.setenv("YUQING_TOOL_USAGE_OUTBOX_DIR", str(tmp_path / "tools"))
    monkeypatch.setenv("YUQING_BILLING_URL", "http://127.0.0.1:18080")
    monkeypatch.setenv("YUQING_BILLING_SERVICE_TOKEN", "isolated-tool-secret")

    def network(request):
        assert request.url.host == "127.0.0.1", "denied authorization must prevent supplier access"
        return httpx.Response(403, json={"code": "stale_run"})

    transport = httpx.MockTransport(network)
    with pytest.raises(httpx.HTTPStatusError):
        await DeepSeekTools("sandbox-key", transport=transport, billing_transport=transport).search("手机", source="news", run_id="stale-run")
    assert not list((tmp_path / "tools").glob("*.json"))


@pytest.mark.asyncio
async def test_independent_candidates_keep_reserved_slots_and_resume_reuses_versions(monkeypatch):
    for name in ["BOCHA_API_KEY", "DEEPSEEK_API_KEY", "LLM_API_KEY"]:
        monkeypatch.delenv(name, raising=False)
    local_urls = {f"https://www.chinanews.com.cn/cj/local-{index}.shtml" for index in range(35)}

    async def discover(**_kwargs):
        return {"pages_visited": 1, "channels": [], "candidates": [
            {"url": url, "title": "本机发现", "source_type": "news", "published_at": "2026-10-30"}
            for url in sorted(local_urls)]}

    class Scraper:
        def __init__(self):
            self.fetched = []

        async def _bocha_search(self, *_args, **_kwargs):
            return [{"url": f"https://www.chinanews.com.cn/cj/supplier-{index}.shtml", "title": "供应商发现", "published_at": "2026-10-30"} for index in range(100)]

        async def fetch_candidate(self, url, source, mode="static", **_kwargs):
            self.fetched.append(url)
            return FetchOutcome(content=f"手机公开体验资料，关于{url}有具体记录，内容尚未经过主题相关性验证。", final_url=url, status="fetched")

        def _source_mode(self, _source):
            return "static"

    async def checkpoint(*_args):
        return None

    scraper = Scraper()
    monkeypatch.setattr(query, "discover_public", discover)
    monkeypatch.setattr(query, "get_scraper", lambda: scraper)
    monkeypatch.setattr(query, "persist_candidate_audit", checkpoint)
    response = await query._execute(query.SearchRequest(analysis_id="analysis", run_id="new-run", resilient=True,
        keywords=["手机"], sources=["news"], bocha_api_key="sandbox-key", search_provider="bocha",
        retained_candidates=[{"url": "https://www.chinanews.com.cn/cj/retained.shtml", "source_type": "news", "title": "已保存资料",
            "content": "上一轮已经取得的公开网页资料，内容足够完整，保留原始文本版本且不重新向供应商取文。", "published_at": "2026-10-30",
            "content_kind": "body", "fetch_status": "fetched", "content_version": "saved-v1", "fetched_at": "2026-10-30T01:00:00Z"}], resume_from_run_id="old-run"))
    assert len(scraper.fetched) <= 99
    assert len(set(scraper.fetched) & local_urls) >= 30
    assert "https://www.chinanews.com.cn/cj/retained.shtml" not in scraper.fetched
    retained = next(row for row in response.coverage["candidate_audit"] if row["url"].endswith("retained.shtml"))
    assert retained["content_version"] == "saved-v1"
    assert retained["fetched_at"] == "2026-10-30T01:00:00Z"
    assert retained["semantic_classification"] == "uncertain"


@pytest.mark.asyncio
async def test_social_trending_page_is_discovery_step_and_never_article_material():
    from engines.common.discovery import discover_public

    def public_network(request):
        if request.url.path.startswith("/hot-event/"):
            return httpx.Response(200, json={"data": [{"Title": "公开热点", "Url": "https://www.toutiao.com/trending/760002/"}]})
        if request.url.path == "/trending/760002/":
            return httpx.Response(200, text='<html><body><a href="https://www.toutiao.com/article/760003/">真实文章</a></body></html>')
        raise AssertionError(str(request.url))

    result = await discover_public(keywords=["手机"], sources=["toutiao"], transport=httpx.MockTransport(public_network))
    assert [row["url"] for row in result["candidates"]] == ["https://www.toutiao.com/article/760003/"]
    assert result["pages_visited"] == 2
    assert len(result["candidates"][0]["discovery_chain"]) == 2
