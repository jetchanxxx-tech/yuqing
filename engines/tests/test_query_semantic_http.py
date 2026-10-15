"""Accepted-run /search contracts: one topic, real candidate boundaries, no lexical bypass."""
import json

import httpx
import pytest

from engines.common import scraper as scraper_module
from engines.query_engine import main as query


INTERNAL_TOKEN = "offline-internal-token"


def provider_candidate(source, index, text, published="2026-10-12"):
    hosts = {"toutiao": "www.toutiao.com", "wechat": "mp.weixin.qq.com", "weibo": "weibo.com"}
    return {"url": f"https://{hosts[source]}/article/{index}", "title": f"材料 {index}",
            "summary": text, "snippet": text, "published_at": published, "site_name": source}


class TopicModel:
    def __init__(self, decisions=None):
        self.calls = []
        self.decisions = decisions

    async def chat_json(self, model, messages, **kwargs):
        self.calls.append((messages, kwargs))
        prompt = json.loads(messages[-1]["content"])
        if kwargs.get("usage_context", {}).get("phase") == "plan":
            return {"topic": "iPhone18系列新品反馈", "entities": ["iphone18", "iphone18pro", "iphone18max"],
                    "relations": ["用户列出的同系列研究对象，未核实官方型号"],
                    "focus": ["体验与反馈"], "aliases": [], "non_assumptions": ["不推断已经发布"],
                    "search_queries": ["iPhone18 系列 体验"]}
        if self.decisions is not None:
            return {"decisions": self.decisions(prompt["materials"])}
        return {"decisions": [
            {"document_id": item["document_id"], "classification": "direct", "reason": "讨论研究对象的具体体验",
             "matched_entities": ["iPhone18 Pro"], "evidence_spans": [item["content"][:24]]}
            for item in prompt["materials"]]}


def setup_pipeline(monkeypatch, candidates, model=None, recovered=None):
    model = model or TopicModel()
    provider_calls = []
    scraper = scraper_module.PageScraper()
    monkeypatch.setenv("YUQING_BILLING_SERVICE_TOKEN", INTERNAL_TOKEN)
    monkeypatch.setenv("LLM_API_KEY", "offline-model-key")
    monkeypatch.setenv("BOCHA_API_KEY", "offline-search-key")
    monkeypatch.setattr(query, "DEFAULT_BOCHA_KEY", "offline-search-key")
    monkeypatch.setattr(query, "get_scraper", lambda: scraper)
    monkeypatch.setattr(query, "build_client", lambda *args, **kwargs: model)

    async def bocha(keyword, api_key, include="", count=20, freshness="noLimit", **kwargs):
        provider_calls.append({"keyword": keyword, "include": include, "count": count, "freshness": freshness})
        source = {"toutiao.com": "toutiao", "mp.weixin.qq.com": "wechat", "weibo.com": "weibo"}.get(include)
        return [dict(item) for item in candidates.get(source, [])]

    async def save_intent(req, intent, model=None):
        return intent

    monkeypatch.setattr(scraper, "_bocha_search", bocha)
    monkeypatch.setattr(scraper, "fetch_candidate", lambda url, source_type, mode="static", **kwargs:
                        recovered.get(url, scraper_module.FetchOutcome(reason="fixture_body_unavailable"))
                        if recovered is not None else scraper_module.FetchOutcome(reason="fixture_body_unavailable"))
    # Persistence is a boundary to the actual Go/PG contract, independently tested there.
    monkeypatch.setattr(query, "persist_research_intent", save_intent, raising=False)
    monkeypatch.setattr(query, "persist_candidate_audit", save_intent, raising=False)
    return provider_calls, model


async def post_search(payload, token=INTERNAL_TOKEN):
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=query.app), base_url="http://query.test") as client:
        return await client.post("/search", json=payload, headers={"X-Internal-Token": token} if token else {})


def accepted_request(**changes):
    return {"keywords": ["iphone18", "iphone18pro", "iphone18max"], "sources": ["toutiao"],
            "analysis_id": "accepted-analysis", "run_id": "accepted-run", "max_results": 50,
            "research_question": "同一系列的新品体验", "analysis_type": "product", **changes}


@pytest.mark.asyncio
async def test_real_search_plans_all_related_keywords_and_admits_one_variant(monkeypatch):
    calls, llm = setup_pipeline(monkeypatch, {"toutiao": [
        provider_candidate("toutiao", 1, "iPhone 18 Pro续航体验不错，长时间拍摄仍有余量。"),
        provider_candidate("toutiao", 2, "iPhone18 Max机身手感偏重，但大屏阅读体验不错。"),
    ]})
    response = await post_search(accepted_request())
    assert response.status_code == 200, response.text
    data = response.json()
    assert len(data["documents"]) == 2
    assert data["coverage"]["admission_version"] == "semantic-v2"
    assert data["coverage"]["research_intent"]["original_keywords"] == ["iphone18", "iphone18pro", "iphone18max"]
    assert [kwargs["usage_context"]["phase"] for _, kwargs in llm.calls] == ["plan", "relevance"]
    assert all(kwargs["usage_context"]["run_id"] == "accepted-run" for _, kwargs in llm.calls)
    assert all(kwargs["max_tokens"] == 16384 for _, kwargs in llm.calls)
    assert calls[0]["keyword"] == "iPhone18 系列 体验"


@pytest.mark.asyncio
@pytest.mark.parametrize("payload,token", [(accepted_request(run_id=""), INTERNAL_TOKEN), (accepted_request(), ""),
                                          (accepted_request(), "wrong-token")])
async def test_live_lexical_or_unauthenticated_path_cannot_search_or_fetch(monkeypatch, payload, token):
    calls, model = setup_pipeline(monkeypatch, {})
    response = await post_search(payload, token)
    assert response.status_code in {401, 403, 422}, response.text
    assert not calls
    assert not model.calls


@pytest.mark.asyncio
async def test_search_queries_platforms_independently_and_keeps_later_direct_after_context(monkeypatch):
    values = [provider_candidate("toutiao", i, f"旧款背景材料{i}：iPhone17是上一代产品，仅用于比较背景。") for i in range(49)]
    values += [provider_candidate("toutiao", 50, "iPhone 18 Pro直接体验材料：续航有余量。")]
    def decisions(items):
        return [{"document_id": row["document_id"], "classification": "context" if "旧款" in row["content"] else "direct",
                 "reason": "明确区分旧款背景与目标型号", "evidence_spans": [row["content"][:20]]} for row in items]
    calls, _ = setup_pipeline(monkeypatch, {"toutiao": values, "wechat": [
        provider_candidate("wechat", 51, "iPhone18 Max独立体验材料：大屏清晰、手感偏重。")
    ]}, TopicModel(decisions))
    response = await post_search(accepted_request(sources=["toutiao", "wechat"]))
    assert response.status_code == 200, response.text
    result = response.json()
    assert {item["include"] for item in calls} == {"toutiao.com", "mp.weixin.qq.com"}
    assert len(result["documents"]) == 50
    assert sum(row["semantic_classification"] == "direct" for row in result["documents"]) == 2
    assert {row["source_type"] for row in result["documents"]} == {"toutiao", "wechat"}
    assert len(result["coverage"]["candidate_audit"]) == 51
    assert {row["source"] for row in result["coverage"]["per_platform"]} == {"toutiao", "wechat"}


@pytest.mark.asyncio
async def test_shell_summary_is_recovered_before_semantic_not_discarded(monkeypatch):
    item = provider_candidate("toutiao", 1, "We're sorry but react app doesn't work properly without JavaScript enabled.")
    body = "iPhone 18 Pro续航实测：录像一小时后仍有充足电量，本文为用户测试感受。"
    setup_pipeline(monkeypatch, {"toutiao": [item]}, recovered={item["url"]: scraper_module.FetchOutcome(
        content=body, final_url=item["url"], status="fetched", content_kind="body")})
    response = await post_search(accepted_request())
    assert response.status_code == 200, response.text
    document = response.json()["documents"][0]
    assert document["content"] == body
    assert document["content_kind"] == "body"
    assert document["fetch_status"] == "fetched"


@pytest.mark.asyncio
async def test_wrong_source_date_conflict_and_empty_evidence_cannot_enter_report_pool(monkeypatch):
    valid = provider_candidate("toutiao", 1, "iPhone18 Pro续航实际测试反馈，供本轮研究讨论。")
    wrong_source = provider_candidate("wechat", 2, "iPhone18 Pro来自未选择公众号的材料，不得进入报告。")
    old = provider_candidate("toutiao", 3, "iPhone18 Pro日期越界的材料，不得进入背景材料池。", "2025-01-01")
    def no_evidence(items):
        return [{"document_id": row["document_id"], "classification": "direct", "reason": "没有证据", "evidence_spans": []} for row in items]
    setup_pipeline(monkeypatch, {"toutiao": [valid, wrong_source, old]}, TopicModel(no_evidence))
    response = await post_search(accepted_request(date_from="2026-10-01", date_to="2026-10-15"))
    assert response.status_code == 502, response.text
    assert "documents" not in response.json().get("detail", {})
    assert "offline-model-key" not in response.text and "offline-search-key" not in response.text

@pytest.mark.asyncio
async def test_generated_intent_is_persisted_before_any_provider_request(monkeypatch):
    calls, _ = setup_pipeline(monkeypatch, {"toutiao": []})
    snapshots = []
    async def snapshot(req, intent, model):
        assert calls == []
        assert req.run_id == "accepted-run"
        snapshots.append(intent)
        return intent
    monkeypatch.setattr(query, "persist_research_intent", snapshot, raising=False)
    response = await post_search(accepted_request())
    assert response.status_code == 200, response.text
    assert snapshots and snapshots[0]["original_keywords"] == accepted_request()["keywords"]


@pytest.mark.asyncio
async def test_failed_snapshot_prevents_provider_search(monkeypatch):
    calls, _ = setup_pipeline(monkeypatch, {})
    async def snapshot(*args, **kwargs):
        raise RuntimeError("fixture persistence unavailable")
    monkeypatch.setattr(query, "persist_research_intent", snapshot, raising=False)
    response = await post_search(accepted_request())
    assert response.status_code == 503, response.text
    assert calls == []
    assert "fixture persistence unavailable" not in response.text
