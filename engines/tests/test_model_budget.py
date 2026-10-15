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
