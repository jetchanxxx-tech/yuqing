"""Current DeepSeek parameters and terminal outcomes remain independently accounted."""
import asyncio
import json

import httpx
import pytest

from engines.common.llm_client import LLMClient
from engines.common.model_budget import chat_json_bounded
from engines.insight_engine import main as insight
from engines.report_engine import main as report
from engines.tests.test_report import DEFAULT_LLM_PAYLOAD


@pytest.mark.asyncio
@pytest.mark.parametrize("reason,code", [("insufficient_system_resource", "llm_insufficient_system_resource"), ("aborted", "llm_aborted")])
async def test_deepseek_terminal_resource_reasons_are_not_json_repair_or_content_refusal(reason, code):
    requests = []
    def provider(request):
        requests.append(request)
        return httpx.Response(200, json={"id": "resource-stop", "model": "deepseek-flash", "usage": None,
            "choices": [{"finish_reason": reason, "message": {"content": ""}}]})
    client = LLMClient("https://offline.invalid/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(RuntimeError) as error:
        await chat_json_bounded(client, "deepseek-flash", [], deadline=asyncio.get_running_loop().time() + 5)
    assert error.value.code == code
    assert len(requests) == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("model", ["deepseek-flash", "deepseek-v4-pro"])
async def test_current_deepseek_analysis_uses_task_effort_and_full_output_recovery(monkeypatch, model):
    requests = []
    def provider(request):
        body = json.loads(request.content)
        requests.append(body)
        return httpx.Response(200, json={"model": model,
            "choices": [{"finish_reason": "length" if len(requests) == 1 else "stop",
                         "message": {"content": '{"ok":true}'}}]})
    client = LLMClient("https://offline.invalid/v1", transport=httpx.MockTransport(provider))
    result = await insight._chat_json_smart(client, model, [], mode="full", temperature=0.6)
    assert result == {"ok": True}
    assert [r["max_tokens"] for r in requests] == [16384, 32768]
    assert all(r.get("thinking") == {"type": "enabled"} and r.get("reasoning_effort") == "high" for r in requests)
    assert all("temperature" not in r for r in requests)


@pytest.mark.asyncio
@pytest.mark.parametrize("model", ["deepseek-flash", "deepseek-v4-pro"])
async def test_current_deepseek_report_uses_explicit_reasoning_policy(monkeypatch, model):
    requests = []
    def provider(request):
        body = json.loads(request.content)
        requests.append(body)
        return httpx.Response(200, json={"model": model,
            "choices": [{"finish_reason": "stop", "message": {"content": json.dumps(DEFAULT_LLM_PAYLOAD)}}]})
    client = LLMClient("https://offline.invalid/v1", transport=httpx.MockTransport(provider))
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: client)
    req = report.GenerateRequest(api_key="offline-key", llm_model=model, documents=[{"id": "d1", "content": "后排腿部空间局促。", "source_name": "汽车之家"}])
    assert await report._llm_insight(req) is not None
    assert requests[0].get("thinking") == {"type": "enabled"}
    assert requests[0].get("reasoning_effort") == "high" and "temperature" not in requests[0]
