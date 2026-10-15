"""Actual provider client contracts for complete output within a deadline."""
import asyncio
import json

import httpx
import pytest

from engines.common.llm_client import LLMClient, LLMOutputTruncated


@pytest.mark.asyncio
async def test_live_model_without_verified_capabilities_spends_nothing(monkeypatch):
    from engines.common.model_budget import ModelContextExceeded, chat_json_bounded

    monkeypatch.delenv("YUQING_MODEL_CAPABILITIES_JSON", raising=False)
    calls = []
    client = LLMClient("https://supplier.test/v1")

    async def observed_call(*args, **kwargs):
        calls.append(kwargs)
        return {"ready": True}

    monkeypatch.setattr(client, "chat_json", observed_call)
    with pytest.raises(ModelContextExceeded, match="capabilities have not been verified"):
        await chat_json_bounded(client, "unverified-model", [])
    assert calls == []


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
async def test_semantic_recovery_cannot_restart_exhausted_supplier_attempts():
    from engines.common.model_budget import AttemptBudget, chat_json_bounded
    calls = []

    def provider(request):
        calls.append(json.loads(request.content))
        return httpx.Response(200, json={"choices": [{"message": {"content": '{"ready":true}'},
            "finish_reason": "length" if len(calls) == 1 else "stop"}]})

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    budget = AttemptBudget()
    assert await chat_json_bounded(client, "fixture-model", [], attempt_budget=budget) == {"ready": True}
    with pytest.raises(ValueError, match="attempt budget exhausted"):
        await chat_json_bounded(client, "fixture-model", [], attempt_budget=budget)
    assert len(calls) == 2
    assert budget.remaining == 0


@pytest.mark.asyncio
async def test_output_validation_uses_same_supplier_allowance():
    from engines.common.model_budget import AttemptBudget, chat_json_bounded
    calls = []

    def provider(request):
        calls.append(json.loads(request.content))
        content = '{}' if len(calls) == 1 else '{"executive_summary":"Evidence-backed findings"}'
        return httpx.Response(200, json={"choices": [{"message": {"content": content}, "finish_reason": "stop"}]})

    def substantive(data):
        if not data.get("executive_summary"):
            raise ValueError("empty report")

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    budget = AttemptBudget()
    result = await chat_json_bounded(client, "fixture-model", [], attempt_budget=budget, validate_result=substantive)
    assert result["executive_summary"] == "Evidence-backed findings"
    assert [r["max_tokens"] for r in calls] == [16384, 32768]
    assert all("attempt_budget" not in r and "validate_result" not in r for r in calls)
    assert budget.used == 2


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
@pytest.mark.parametrize("bounded", [False, True])
async def test_cancelled_real_stream_preserves_already_received_supplier_usage(monkeypatch, tmp_path, bounded):
    from engines.common import llm_client as module
    from engines.common.model_budget import chat_json_bounded
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
        if bounded:
            await chat_json_bounded(client, "fixture-model", [], attempt_timeout=0.5,
                deadline=asyncio.get_running_loop().time() + 0.1,
                usage_context={"run_id": "accepted-stream", "engine": "query", "phase": "relevance"})
        else:
            await asyncio.wait_for(client.chat_json("fixture-model", [], _max_attempts=1,
                usage_context={"run_id": "accepted-stream", "engine": "query", "phase": "relevance"}), timeout=0.1)
    records = list(pending_records())
    assert len(records) == 1
    event = records[0][1]["event"]
    assert event["usage_status"] == "reported"
    assert (event["prompt_tokens"], event["completion_tokens"]) == (7, 2)
    assert event["provider_request_id"] == "supplier-observed"


@pytest.mark.asyncio
async def test_stalled_attempt_recovers_inside_original_phase_without_reducing_output():
    from engines.common.model_budget import AttemptBudget, chat_json_bounded
    requests = []
    closed = []

    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b'data: {"id":"stalled-public-request","choices":[]}\n\n'
            await asyncio.Event().wait()

        async def aclose(self):
            closed.append(True)

    def provider(request):
        requests.append(json.loads(request.content))
        if len(requests) == 1:
            return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())
        return httpx.Response(200, json={"choices": [{"message": {"content": '{"ready":true}'}, "finish_reason": "stop"}]})

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    budget = AttemptBudget()
    result = await chat_json_bounded(client, "fixture-model", [], attempt_budget=budget,
        deadline=asyncio.get_running_loop().time() + 0.8, attempt_timeout=0.05)
    assert result == {"ready": True}
    assert len(requests) == budget.used == 2
    assert closed == [True]
    assert [request["max_tokens"] for request in requests] == [16384, 16384]
    assert all("attempt_timeout" not in request for request in requests)


@pytest.mark.asyncio
async def test_two_stalled_attempts_exhaust_shared_allowance_without_third_call():
    from engines.common.model_budget import AttemptBudget, chat_json_bounded
    requests = []

    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b'data: {"id":"stalled-public-request","choices":[]}\n\n'
            await asyncio.Event().wait()

    def provider(request):
        requests.append(json.loads(request.content))
        return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    budget = AttemptBudget()
    deadline = asyncio.get_running_loop().time() + 0.8
    with pytest.raises(TimeoutError):
        await chat_json_bounded(client, "fixture-model", [], attempt_budget=budget,
            deadline=deadline, attempt_timeout=0.05)
    assert len(requests) == budget.used == 2
    assert budget.remaining == 0
    with pytest.raises(ValueError, match="attempt budget exhausted"):
        await chat_json_bounded(client, "fixture-model", [], attempt_budget=budget,
            deadline=deadline, attempt_timeout=0.05)
    assert len(requests) == 2


@pytest.mark.asyncio
async def test_attempt_wait_never_extends_original_phase_deadline():
    from engines.common.model_budget import AttemptBudget, chat_json_bounded
    requests = []

    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b'data: {"id":"stalled-public-request","choices":[]}\n\n'
            await asyncio.Event().wait()

    def provider(request):
        requests.append(json.loads(request.content))
        return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    budget = AttemptBudget()
    with pytest.raises(TimeoutError):
        await chat_json_bounded(client, "fixture-model", [], attempt_budget=budget,
            deadline=asyncio.get_running_loop().time() + 0.05, attempt_timeout=0.5)
    assert len(requests) == budget.used == 1
    assert "attempt_timeout" not in requests[0]


@pytest.mark.asyncio
async def test_recovered_stall_retains_received_usage_for_both_distinct_attempts(monkeypatch, tmp_path):
    from engines.common import llm_client as module
    from engines.common.model_budget import chat_json_bounded
    from engines.common.usage_outbox import pending_records
    requests = []

    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield ("data: " + json.dumps({"id": "received-before-stall", "model": "actual-fixture",
                "usage": {"prompt_tokens": 7, "completion_tokens": 2}, "choices": []}) + "\n\n").encode()
            await asyncio.Event().wait()

    def provider(request):
        requests.append(json.loads(request.content))
        if len(requests) == 1:
            return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())
        return httpx.Response(200, json={"id": "recovered-request", "model": "actual-fixture",
            "usage": {"prompt_tokens": 8, "completion_tokens": 3},
            "choices": [{"message": {"content": '{"ready":true}'}, "finish_reason": "stop"}]})

    async def authorize(*args, **kwargs):
        return {"permit": "offline-permit"}

    async def retain_pending():
        pass

    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(module, "authorize_call", authorize)
    monkeypatch.setattr(module, "deliver_pending_usage_events", retain_pending)
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    result = await chat_json_bounded(client, "fixture-model", [], attempt_timeout=0.05,
        deadline=asyncio.get_running_loop().time() + 0.8,
        usage_context={"run_id": "accepted-recovery", "engine": "query", "phase": "relevance"})
    assert result == {"ready": True}
    events = sorted((record[1]["event"] for record in pending_records()), key=lambda event: event["attempt"])
    assert len(events) == 2
    assert len({event["call_id"] for event in events}) == 2
    assert [event["attempt"] for event in events] == [1, 2]
    assert [event["outcome"] for event in events] == ["provider_error", "provider_response"]
    assert all(event["usage_status"] == "reported" for event in events)
    assert [(event["prompt_tokens"], event["completion_tokens"]) for event in events] == [(7, 2), (8, 3)]
    assert [event["provider_request_id"] for event in events] == ["received-before-stall", "recovered-request"]


@pytest.mark.asyncio
async def test_slow_usage_delivery_after_completed_supplier_never_purchases_again(monkeypatch, tmp_path):
    from engines.common import llm_client as module
    from engines.common.model_budget import chat_json_bounded
    from engines.common.usage_outbox import pending_records
    requests = []

    def provider(request):
        requests.append(json.loads(request.content))
        return httpx.Response(200, json={"id": "already-completed", "model": "actual-fixture",
            "usage": {"prompt_tokens": 7, "completion_tokens": 2},
            "choices": [{"message": {"content": '{"ready":true}'}, "finish_reason": "stop"}]})

    async def authorize(*args, **kwargs):
        return {"permit": "offline-permit"}

    async def stalled_delivery():
        await asyncio.Event().wait()

    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(module, "authorize_call", authorize)
    monkeypatch.setattr(module, "deliver_pending_usage_events", stalled_delivery)
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(TimeoutError):
        await chat_json_bounded(client, "fixture-model", [], attempt_timeout=0.05,
            deadline=asyncio.get_running_loop().time() + 0.2,
            usage_context={"run_id": "accepted-completed", "engine": "query", "phase": "relevance"})
    assert len(requests) == 1
    events = [record[1]["event"] for record in pending_records()]
    assert len(events) == 1
    assert events[0]["outcome"] == "provider_response"
    assert events[0]["usage_status"] == "reported"
    assert (events[0]["prompt_tokens"], events[0]["completion_tokens"]) == (7, 2)


@pytest.mark.asyncio
async def test_external_cancellation_during_attempt_never_retries():
    from engines.common.model_budget import chat_json_bounded
    requests = []
    entered = asyncio.Event()

    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b'data: {"id":"cancelled-request","choices":[]}\n\n'
            entered.set()
            await asyncio.Event().wait()

    def provider(request):
        requests.append(json.loads(request.content))
        return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())

    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    task = asyncio.create_task(chat_json_bounded(client, "fixture-model", [], attempt_timeout=0.5,
        deadline=asyncio.get_running_loop().time() + 2))
    await asyncio.wait_for(entered.wait(), timeout=1)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert len(requests) == 1


@pytest.mark.asyncio
async def test_stalled_request_without_reported_usage_stays_unknown_after_recovery(monkeypatch, tmp_path):
    from engines.common import llm_client as module
    from engines.common.model_budget import chat_json_bounded
    from engines.common.usage_outbox import pending_records
    requests = []

    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b'data: {"id":"usage-not-reported","model":"actual-fixture","choices":[]}\n\n'
            await asyncio.Event().wait()

    def provider(request):
        requests.append(json.loads(request.content))
        if len(requests) == 1:
            return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())
        return httpx.Response(200, json={"id": "recovered-request", "model": "actual-fixture",
            "usage": {"prompt_tokens": 8, "completion_tokens": 3},
            "choices": [{"message": {"content": '{"ready":true}'}, "finish_reason": "stop"}]})

    async def authorize(*args, **kwargs):
        return {"permit": "offline-permit"}

    async def retain_pending():
        pass

    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(module, "authorize_call", authorize)
    monkeypatch.setattr(module, "deliver_pending_usage_events", retain_pending)
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    assert await chat_json_bounded(client, "fixture-model", [], attempt_timeout=0.05,
        deadline=asyncio.get_running_loop().time() + 0.8,
        usage_context={"run_id": "accepted-unknown", "engine": "query", "phase": "relevance"}) == {"ready": True}
    events = sorted((record[1]["event"] for record in pending_records()), key=lambda event: event["attempt"])
    assert len(events) == 2
    assert events[0]["outcome"] == "provider_error"
    assert events[0]["provider_request_id"] == "usage-not-reported"
    assert events[0]["usage_status"] == "unknown"
    assert events[1]["usage_status"] == "reported"


@pytest.mark.asyncio
@pytest.mark.parametrize("invalid", [0, -1, float("inf"), float("nan"), True])
async def test_invalid_attempt_wait_is_rejected_before_supplier_or_budget_spend(invalid):
    from engines.common.model_budget import AttemptBudget, chat_json_bounded
    requests = []

    def provider(request):
        requests.append(request)
        return httpx.Response(200, json={"choices": [{"message": {"content": '{"ready":true}'}, "finish_reason": "stop"}]})

    budget = AttemptBudget()
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(ValueError):
        await chat_json_bounded(client, "fixture-model", [], attempt_timeout=invalid, attempt_budget=budget)
    assert requests == []
    assert budget.used == 0
