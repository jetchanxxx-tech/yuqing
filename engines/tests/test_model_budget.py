"""Actual provider client contracts for complete output within a deadline."""
import asyncio
import json

import httpx
import pytest

from engines.common.llm_client import LLMClient, LLMOutputTruncated


@pytest.mark.asyncio
async def test_complete_output_has_explicit_budget_and_one_larger_truncation_retry():
    from engines.common.model_budget import chat_json_bounded
    requests = []

    def provider(request):
        requests.append(json.loads(request.content))
        return httpx.Response(200, json={
            "choices": [{"message": {"content": '{"ready":true}'},
                         "finish_reason": "length" if len(requests) == 1 else "stop"}],
        })

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    result = await chat_json_bounded(client, "fixture-model", [], deadline=asyncio.get_running_loop().time() + 20)
    assert result == {"ready": True}
    assert [r["max_tokens"] for r in requests] == [16384, 32768]
    assert all(not any(k.startswith("_") for k in r) for r in requests)


@pytest.mark.asyncio
async def test_persistent_truncation_does_not_create_nested_retries():
    from engines.common.model_budget import chat_json_bounded
    calls = []

    def provider(request):
        calls.append(request)
        return httpx.Response(200, json={"choices": [{"message": {"content": "{}"}, "finish_reason": "length"}]})

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(LLMOutputTruncated):
        await chat_json_bounded(client, "fixture-model", [], deadline=asyncio.get_running_loop().time() + 10)
    assert len(calls) == 2


@pytest.mark.asyncio
async def test_expired_deadline_spends_nothing():
    from engines.common.model_budget import chat_json_bounded
    calls = []

    def provider(request):
        calls.append(request)
        return httpx.Response(200, json={"choices": []})

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(TimeoutError):
        await chat_json_bounded(client, "fixture-model", [], deadline=asyncio.get_running_loop().time() - 1)
    assert calls == []


@pytest.mark.asyncio
async def test_cancelled_real_stream_preserves_already_received_supplier_usage(monkeypatch, tmp_path):
    from engines.common import llm_client as module
    from engines.common.usage_outbox import pending_records

    class SlowStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield ("data: " + json.dumps({"id": "supplier-observed", "model": "actual-fixture",
                "usage": {"prompt_tokens": 7, "completion_tokens": 2}, "choices": []}) + "\n\n").encode()
            await asyncio.sleep(10)

    async def authorize(*args, **kwargs):
        return {"permit": "offline-permit"}

    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(module, "authorize_call", authorize)
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(
        lambda request: httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=SlowStream())))
    with pytest.raises(TimeoutError):
        await asyncio.wait_for(client.chat_json("fixture-model", [], _max_attempts=1,
            usage_context={"run_id": "accepted-stream", "engine": "query", "phase": "relevance"}), timeout=0.1)
    records = list(pending_records())
    assert len(records) == 1
    event = records[0][1]["event"]
    assert event["usage_status"] == "reported"
    assert (event["prompt_tokens"], event["completion_tokens"]) == (7, 2)
    assert event["provider_request_id"] == "supplier-observed"
