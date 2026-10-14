"""Scene selection must reach actual insight and report provider requests."""
import json

import httpx
import pytest
from fastapi.testclient import TestClient

from engines.insight_engine import main as insight
from engines.report_engine import main as report
from engines.common.llm_client import LLMClient
from engines.tests.test_insight_quick_mode import KwargsFakeLLM, DOCS


@pytest.mark.parametrize("scene,focus", [
    ("brand_daily", "产品／服务／价格／信任"),
    ("product_launch", "购买阻力"),
    ("quality_complaint", "缺陷率"),
    ("competitor_update", "同一时间窗"),
    ("crisis", "已确认事实、传言与未知点"),
    ("campaign_review", "ROI"),
])
def test_scene_and_perspective_requirements_reach_every_dimension_and_summary(monkeypatch, scene, focus):
    provider = KwargsFakeLLM()
    monkeypatch.setattr(insight, "build_client", lambda *_args, **_kwargs: provider)
    response = TestClient(insight.app).post("/analyze", json={
        "documents": DOCS, "api_key": "offline-key", "analysis_type": "brand",
        "scenario_template_id": scene, "mode": "quick",
    })
    assert response.status_code == 200
    relevant = [prompt for prompt, _kwargs in provider.calls if "【分析维度】" in prompt or "revised_summary" in prompt]
    assert len(relevant) == 4
    assert all(focus in prompt and "【业务视角】" in prompt for prompt in relevant)


@pytest.mark.parametrize("path", ["/analyze", "/generate"])
def test_unknown_scene_fails_before_any_model_call(monkeypatch, path):
    module = insight if path == "/analyze" else report
    provider = KwargsFakeLLM()
    monkeypatch.setattr(module, "build_client", lambda *_args, **_kwargs: provider)
    response = TestClient(module.app).post(path, json={
        "documents": DOCS, "api_key": "offline-key", "scenario_template_id": "invented",
    })
    assert response.status_code == 422
    assert provider.calls == []


@pytest.mark.asyncio
async def test_report_uses_scene_in_its_actual_writing_request(monkeypatch):
    provider = KwargsFakeLLM()
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: provider)
    await report._llm_insight(report.GenerateRequest(
        documents=DOCS, api_key="offline-key", analysis_type="brand",
        scenario_template_id="quality_complaint",
    ))
    assert "缺陷率" in provider.calls[0][0]
    assert "【业务视角】" in provider.calls[0][0]


@pytest.mark.asyncio
async def test_glm_quick_request_uses_valid_official_reasoning_parameters():
    def provider(request):
        payload = json.loads(request.content)
        if payload.get("thinking") != {"type": "enabled"} or payload.get("reasoning_effort") != "low":
            return httpx.Response(400, json={"error": "invalid reasoning parameters"})
        return httpx.Response(200, json={"choices": [{"message": {"content": '{"accepted":true}'}}]})

    client = LLMClient("https://offline.invalid/v4", transport=httpx.MockTransport(provider))
    assert await insight._chat_json_smart(client, "glm-5.3-flash", [], mode="quick") == {"accepted": True}


@pytest.mark.asyncio
async def test_other_models_do_not_receive_glm_specific_reasoning_fields():
    def provider(request):
        payload = json.loads(request.content)
        if "thinking" in payload or "reasoning_effort" in payload:
            return httpx.Response(400, json={"error": "unsupported vendor fields"})
        return httpx.Response(200, json={"choices": [{"message": {"content": '{"accepted":true}'}}]})

    client = LLMClient("https://offline.invalid/v1", transport=httpx.MockTransport(provider))
    assert await insight._chat_json_smart(client, "deepseek-chat", [], mode="quick") == {"accepted": True}


@pytest.mark.asyncio
async def test_unrelated_400_is_not_retried_by_dropping_reasoning_fields():
    attempts = []

    def provider(request):
        attempts.append(request)
        return httpx.Response(400, json={"error": {"message": "max_tokens exceeds model limit"}})

    client = LLMClient("https://offline.invalid/v4", transport=httpx.MockTransport(provider))
    with pytest.raises(httpx.HTTPStatusError):
        await insight._chat_json_smart(client, "glm-5.3-flash", [], mode="quick")
    assert len(attempts) == 1
