"""Actual sensitive/empty response must be terminal, accounted and diagnosable."""
import asyncio
import json

import httpx
import pytest

from engines.common.llm_client import LLMClient
from engines.common.model_budget import chat_json_bounded
from engines.common.usage_outbox import pending_records


@pytest.mark.asyncio
@pytest.mark.parametrize("finish_reason", ["sensitive", "content_filter"])
@pytest.mark.parametrize("stream", [False, True])
async def test_provider_rejection_is_not_retried_or_parsed_as_json(monkeypatch, tmp_path, finish_reason, stream):
    from engines.common import llm_client
    requests = []

    async def authorize(*args, **kwargs):
        return {"permit": "offline-rejection-permit"}

    async def retain():
        return 0

    def supplier(request):
        requests.append(json.loads(request.content))
        # Even a syntactically valid partial payload cannot override rejection.
        content = '{"private":"supplier-private-fragment"}'
        if stream:
            chunks = [
                {"id": "rejected-response", "model": "glm-5.3-flash", "choices": [{"delta": {"content": content}}]},
                {"choices": [{"delta": {}, "finish_reason": finish_reason}]},
            ]
            return httpx.Response(200, headers={"content-type": "text/event-stream"},
                text="\n\n".join("data: " + json.dumps(item) for item in chunks) + "\n\ndata: [DONE]\n\n")
        return httpx.Response(200, json={"id": "rejected-response", "model": "glm-5.3-flash", "usage": None,
            "choices": [{"message": {"content": content}, "finish_reason": finish_reason}]})

    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(llm_client, "authorize_call", authorize)
    monkeypatch.setattr(llm_client, "deliver_pending_usage_events", retain)
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(supplier))
    with pytest.raises(RuntimeError, match="llm_content_rejected") as error:
        await chat_json_bounded(client, "GLM-5.3-Flash", [],
            deadline=asyncio.get_running_loop().time() + 10,
            usage_context={"run_id": "accepted-rejection", "engine": "insight", "phase": "analyze"})
    assert error.value.code == "llm_content_rejected"
    assert "supplier-private-fragment" not in str(error.value)
    assert len(requests) == 1
    events = [record[1]["event"] for record in pending_records()]
    assert len(events) == 1
    assert events[0]["outcome"] == "provider_rejected"
    assert events[0]["usage_status"] == "unknown"
    assert events[0]["provider_request_id"] == "rejected-response"


@pytest.mark.asyncio
async def test_rejected_insight_material_retains_typed_code_without_summarizing(monkeypatch, tmp_path):
    from engines.common import llm_client
    from engines.insight_engine import main
    requests = []

    async def authorize(*args, **kwargs):
        return {"permit": "offline-rejection-permit"}

    async def retain():
        return 0

    def supplier(request):
        requests.append(json.loads(request.content))
        return httpx.Response(200, json={"id": "rejected-material", "model": "glm-5.3-flash",
            "choices": [{"message": {"content": ""}, "finish_reason": "sensitive"}]})

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(supplier))
    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(llm_client, "authorize_call", authorize)
    monkeypatch.setattr(llm_client, "deliver_pending_usage_events", retain)
    monkeypatch.setattr(main, "build_client", lambda *args, **kwargs: client)
    result = await main.analyze(main.AnalyzeRequest(
        run_id="accepted-rejection", api_key="offline-key", mode="quick", title="供应商审核边界",
        documents=[{"id": "doc-a", "content": "这是一条用于验证供应商拒绝处理路径的完整测试材料。", "source_type": "toutiao"}],
    ))
    assert result["summary"] == "" and result["warning"]
    phases = result["material_coverage"]["phases"]
    assert all(span["code"] == "llm_content_rejected" for phase in phases for span in phase["unprocessed"])
    assert next(phase for phase in phases if phase["phase"] == "summary")["failure_code"] == "llm_content_rejected"
    assert len(requests) == 4  # classification plus three quick dimensions; no second calls or summary
