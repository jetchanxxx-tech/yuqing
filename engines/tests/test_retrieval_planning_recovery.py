"""Research-plan protocol recovery through real clients and the /search route."""
import asyncio
import json

import httpx
import pytest

from engines.common.llm_client import LLMClient
from engines.common.retrieval_intent import plan_research_intent
from engines.common.usage_outbox import pending_records
from engines.query_engine.main import SearchRequest
from engines.tests.test_query_semantic_http import (
    TopicModel, accepted_request, post_search, provider_candidate, setup_pipeline,
)


VALID_PLAN = {
    "topic": "姆巴佩与饮品品牌的关联舆情",
    "entities": ["姆巴佩", "蒙牛", "东鹏", "补水啦"],
    "aliases": [], "relations": ["用户共同提供的研究线索，不预设代言关系"],
    "focus": ["事件回应与公众反馈"], "non_assumptions": ["不预设争议已发生"],
    "search_queries": ["姆巴佩 蒙牛", "姆巴佩 东鹏 补水啦"],
}


def planning_request():
    return SearchRequest(**accepted_request(
        keywords=["姆巴佩", "蒙牛", "东鹏", "补水啦"],
        title="姆巴佩", scenario_template_id="crisis"))


def metered_client(monkeypatch, tmp_path, responses):
    from engines.common import llm_client

    requests = []

    async def authorize(*args, **kwargs):
        return {"permit": "offline-planning-permit"}

    async def retain_pending():
        return 0

    def provider(request):
        requests.append(json.loads(request.content))
        value = responses[min(len(requests) - 1, len(responses) - 1)]
        return httpx.Response(200, json={
            "id": f"planning-response-{len(requests)}", "model": "glm-5.3-flash",
            "usage": {"prompt_tokens": 271, "completion_tokens": 391},
            "choices": [{"message": {"content": json.dumps(value, ensure_ascii=False)},
                         "finish_reason": "stop"}],
        })

    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(llm_client, "authorize_call", authorize)
    monkeypatch.setattr(llm_client, "deliver_pending_usage_events", retain_pending)
    return LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider)), requests


@pytest.mark.asyncio
@pytest.mark.parametrize("invalid", [
    {**VALID_PLAN, "entities": [{"name": "姆巴佩"}]},
    {**VALID_PLAN, "relations": [{"subject": "姆巴佩", "object": "蒙牛"}]},
    {**VALID_PLAN, "search_queries": []},
    {**VALID_PLAN, "topic": ""},
])
async def test_invalid_planning_shape_recovers_within_two_metered_attempts(monkeypatch, tmp_path, invalid):
    client, requests = metered_client(monkeypatch, tmp_path, [invalid, VALID_PLAN])
    result = await plan_research_intent(client, "GLM-5.3-Flash", planning_request(),
        deadline=asyncio.get_running_loop().time() + 20)
    assert result["topic"] == "姆巴佩与饮品品牌的关联舆情"
    assert result["original_keywords"] == ["姆巴佩", "蒙牛", "东鹏", "补水啦"]
    assert result["search_queries"] == ["姆巴佩 蒙牛", "姆巴佩 东鹏 补水啦"]
    assert len(requests) == 2
    assert all(request["max_tokens"] >= 16384 for request in requests)
    assert len(requests[1]["messages"]) > len(requests[0]["messages"])
    events = sorted((record[1]["event"] for record in pending_records()), key=lambda item: item["attempt"])
    assert [event["attempt"] for event in events] == [1, 2]
    assert len({event["call_id"] for event in events}) == 2
    assert all(event["outcome"] == "provider_response" and event["usage_status"] == "reported" for event in events)
    assert [(event["prompt_tokens"], event["completion_tokens"]) for event in events] == [(271, 391), (271, 391)]


@pytest.mark.asyncio
async def test_recovered_plan_allows_search_and_evidence_admission(monkeypatch):
    class RecoveringPlanner(TopicModel):
        async def chat_json(self, model, messages, **kwargs):
            attempts = sum(call[1].get("usage_context", {}).get("phase") == "plan" for call in self.calls)
            if kwargs.get("usage_context", {}).get("phase") == "plan" and attempts == 0:
                self.calls.append((messages, kwargs))
                return {"topic": "iPhone18系列反馈", "entities": [{"name": "iphone18pro"}],
                        "search_queries": ["iPhone18 系列 体验"]}
            return await super().chat_json(model, messages, **kwargs)

    calls, _ = setup_pipeline(monkeypatch, {"toutiao": [provider_candidate("toutiao", 1,
        "iPhone18 Pro真实用户体验，持续拍摄后电池仍有余量，机身握持比较舒服。") ]}, RecoveringPlanner())
    response = await post_search(accepted_request())
    assert response.status_code == 200, response.text
    data = response.json()
    assert len(data["documents"]) == 1
    assert data["coverage"]["research_intent"]["original_keywords"] == ["iphone18", "iphone18pro", "iphone18max"]
    assert data["coverage"]["direct_count"] == 1
    assert calls


@pytest.mark.asyncio
async def test_exhausted_planning_protocol_has_safe_diagnostic_and_no_search(monkeypatch, caplog):
    class BrokenPlanner(TopicModel):
        async def chat_json(self, model, messages, **kwargs):
            self.calls.append((messages, kwargs))
            return {"topic": "iPhone18系列反馈", "entities": [{"name": "fixture-private-output"}],
                    "search_queries": ["iPhone18 系列 体验"]}

    calls, model = setup_pipeline(monkeypatch, {}, BrokenPlanner())
    response = await post_search(accepted_request())
    assert response.status_code == 502, response.text
    detail = response.json()["detail"]
    assert detail["code"] == "research_intent_protocol_rejected"
    failure = detail["coverage"]["failure"]
    assert failure["phase"] == "planning"
    assert failure["code"] == "research_intent_invalid_list"
    assert failure["field"] == "entities"
    assert calls == [] and len(model.calls) == 2
    assert "accepted-analysis" in caplog.text and "accepted-run" in caplog.text
    assert "research_intent_invalid_list" in caplog.text
    assert "fixture-private-output" not in response.text + caplog.text
    assert "offline-model-key" not in response.text + caplog.text


@pytest.mark.asyncio
async def test_snapshot_dependency_failure_retains_phase_without_leaking_exception(monkeypatch, caplog):
    from engines.query_engine import main

    calls, _ = setup_pipeline(monkeypatch, {})

    async def unavailable(*args, **kwargs):
        raise RuntimeError("private-upstream-body-and-credential")

    monkeypatch.setattr(main, "persist_research_intent", unavailable)
    response = await post_search(accepted_request())
    assert response.status_code == 503, response.text
    failure = response.json()["detail"]["coverage"]["failure"]
    assert failure["phase"] == "persisting_research_intent"
    assert failure["error_type"] == "RuntimeError"
    assert calls == []
    assert "persisting_research_intent" in caplog.text
    assert "private-upstream-body-and-credential" not in response.text + caplog.text
