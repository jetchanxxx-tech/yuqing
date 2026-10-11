"""Offline provider-attempt accounting against a loopback billing sandbox."""
import json
import asyncio
import os
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import httpx
import pytest

from engines.common import llm_client
from engines.common.llm_client import LLMClient, LLMOutputTruncated


@pytest.fixture
def billing_sandbox(monkeypatch, tmp_path):
    received = {"authorizations": [], "events": {}, "ack_lost": True}

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if self.headers.get("X-Billing-Service-Token") != "isolated-billing-secret":
                self.send_error(403)
                return
            if self.path.endswith("llm-authorizations"):
                received["authorizations"].append(body)
                self.send_response(201)
                self.end_headers()
                self.wfile.write(json.dumps({"call_id": body["call_id"], "permit": "isolated-permit", "expires_at": "2099-01-01T00:00:00Z"}).encode())
                return
            assert self.headers.get("X-LLM-Call-Permit") == "isolated-permit"
            key = body["event_id"]
            previous = received["events"].setdefault(key, body)
            assert previous == body
            if received["ack_lost"]:
                # Simulates a committed ledger transaction with a lost HTTP ACK.
                self.connection.close()
                return
            self.send_response(200 if previous else 201)
            self.end_headers()
            self.wfile.write(b'{"committed":true}')

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    monkeypatch.setenv("YUQING_BILLING_URL", f"http://127.0.0.1:{server.server_port}")
    monkeypatch.setenv("YUQING_BILLING_SERVICE_TOKEN", "isolated-billing-secret")
    monkeypatch.setenv("YUQING_USAGE_OUTBOX_DIR", str(tmp_path))
    monkeypatch.setattr(llm_client, "RETRY_DELAY_SECONDS", 0)
    yield received, tmp_path
    server.shutdown()
    server.server_close()
    thread.join(timeout=3)


@pytest.mark.asyncio
@pytest.mark.parametrize("finish,content,error", [("stop", "broken JSON", ValueError), ("length", "{", LLMOutputTruncated)])
async def test_usage_survives_invalid_json_and_retry(billing_sandbox, finish, content, error):
    received, outbox = billing_sandbox
    attempts = []

    def provider(request):
        payload = json.loads(request.content)
        attempts.append(payload)
        if len(attempts) == 1:
            return httpx.Response(503, json={"error": "retry sandbox"})
        return httpx.Response(200, json={"id": "sandbox-request", "model": "sandbox-model", "choices": [{"message": {"content": content}, "finish_reason": finish}], "usage": {"prompt_tokens": 400, "completion_tokens": 100, "prompt_tokens_details": {"cached_tokens": 20}}})

    client = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(error):
        await client.chat_json("sandbox-model", [], usage_context={"run_id": "accepted-run", "engine": "insight", "phase": "analyze"})
    files = list(outbox.glob("*.json"))
    assert len(files) == 2, "every provider attempt must be durable before business JSON parsing"
    records = [json.loads(path.read_text()) for path in files]
    assert sorted(item["event"]["attempt"] for item in records) == [1, 2]
    assert len({item["event"]["call_id"] for item in records}) == 2
    reported = next(item["event"] for item in records if item["event"]["usage_status"] == "reported")
    assert reported["prompt_tokens"] == 400
    assert reported["completion_tokens"] == 100
    assert reported["cache_tokens"] == 20
    assert reported["actual_model"] == "sandbox-model"
    assert reported["provider_request_id"] == "sandbox-request"
    assert len(received["authorizations"]) == 2
    assert all("usage_context" not in attempt for attempt in attempts)
    assert all(attempt["stream_options"]["include_usage"] is True for attempt in attempts)


@pytest.mark.asyncio
async def test_usage_outbox_recovers_after_ack_loss(billing_sandbox):
    received, directory = billing_sandbox

    def provider(_request):
        return httpx.Response(200, json={"id": "committed-late", "model": "sandbox-model", "choices": [{"message": {"content": '{"ok":true}'}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 400, "completion_tokens": 100}})

    client = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(provider))
    assert await client.chat_json("sandbox-model", [], usage_context={"run_id": "accepted-run", "engine": "report", "phase": "generate"}) == {"ok": True}
    pending = list(directory.glob("*.json"))
    assert len(pending) == 1, "ACK loss must retain the committed event on disk"
    before = json.loads(pending[0].read_text())
    assert before["permit"] == "isolated-permit"
    received["ack_lost"] = False
    # Import after the behavioral assertion allows the existing client to fail
    # meaningfully before the implementation module exists in the RED baseline.
    restarted = await asyncio.to_thread(subprocess.run, [sys.executable, "-c", "import asyncio; from engines.common.usage_bridge import deliver_pending_usage_events; print(asyncio.run(deliver_pending_usage_events()))"], env=os.environ.copy(), capture_output=True, text=True, timeout=20)
    assert restarted.returncode == 0, restarted.stderr
    assert restarted.stdout.strip() == "1"
    assert list(directory.glob("*.json")) == []
    assert len(received["events"]) == 1
    assert next(iter(received["events"].values())) == before["event"]


@pytest.mark.asyncio
async def test_partial_parallel_dimensions_keep_all_observed_usage(billing_sandbox):
    received, directory = billing_sandbox

    def provider(request):
        prompt = json.loads(request.content)["messages"][0]["content"]
        content = '{"finding":"retained"}' if prompt == "good dimension" else "invalid JSON dimension"
        return httpx.Response(200, json={"id": prompt, "model": "sandbox-model", "choices": [{"message": {"content": content}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 400, "completion_tokens": 100}})

    client = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(provider))
    context = {"run_id": "partial-run", "engine": "insight", "phase": "analyze"}
    results = await asyncio.gather(*(client.chat_json("sandbox-model", [{"role": "user", "content": prompt}], usage_context=context) for prompt in ["good dimension", "bad dimension"]), return_exceptions=True)
    assert results[0] == {"finding": "retained"}
    assert isinstance(results[1], ValueError)
    records = [json.loads(path.read_text())["event"] for path in directory.glob("*.json")]
    assert len(records) == 2
    assert sum(event["prompt_tokens"] + event["completion_tokens"] for event in records) == 1000
    assert len(received["authorizations"]) == 2

@pytest.mark.asyncio
async def test_engine_lifespan_drains_durable_events_on_startup(billing_sandbox):
    received, directory = billing_sandbox

    def provider(_request):
        return httpx.Response(200, json={"id": "startup-retry", "model": "sandbox-model", "choices": [{"message": {"content": "{}"}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 2, "completion_tokens": 1}})

    client = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(provider))
    await client.chat_json("sandbox-model", [], usage_context={"run_id": "accepted-run", "engine": "report", "phase": "generate"})
    assert len(list(directory.glob("*.json"))) == 1
    received["ack_lost"] = False
    from engines.report_engine.main import app
    async with app.router.lifespan_context(app):
        for _ in range(100):
            if not list(directory.glob("*.json")):
                break
            await asyncio.sleep(0.02)
        assert list(directory.glob("*.json")) == []
    assert app.state.usage_delivery_task.cancelled()
    assert len(received["events"]) == 1

@pytest.mark.asyncio
async def test_provider_process_crash_reconciles_unknown_intent(billing_sandbox):
    received, directory = billing_sandbox
    script = """
import asyncio, os, httpx
from engines.common.llm_client import LLMClient
def crash(_request):
    os._exit(23)
client=LLMClient('https://sandbox.invalid/v1',transport=httpx.MockTransport(crash))
asyncio.run(client.chat_json('sandbox-model',[],usage_context={'run_id':'crashed-run','engine':'insight','phase':'analyze'}))
"""
    result = await asyncio.to_thread(subprocess.run, [sys.executable, "-c", script], env=os.environ.copy(), capture_output=True, text=True, timeout=20)
    assert result.returncode == 23, result.stderr
    files = list(directory.glob("*.json"))
    assert len(files) == 1
    assert json.loads(files[0].read_text())["pending"] is True
    received["ack_lost"] = False
    from engines.common.usage_bridge import deliver_pending_usage_events
    assert await deliver_pending_usage_events() == 1
    event = next(iter(received["events"].values()))
    assert event["usage_status"] == "unknown"
    assert event["outcome"] == "process_interrupted"
    assert event["prompt_tokens"] == 0
    assert list(directory.glob("*.json")) == []

@pytest.mark.asyncio
@pytest.mark.parametrize("fail_after", [1, 2])
async def test_report_product_never_succeeds_when_usage_fsync_fails(billing_sandbox, monkeypatch, fail_after):
    from engines.report_engine import main as report_engine
    from engines.common import usage_outbox
    provider_calls = []

    def provider(_request):
        provider_calls.append(1)
        return httpx.Response(200, json={"id": "observed-before-disk-failure", "model": "sandbox-model", "choices": [{"message": {"content": '{"executive_summary":"must not publish"}'}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 400, "completion_tokens": 100}})

    actual = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(provider))
    monkeypatch.setattr(report_engine, "build_client", lambda *args, **kwargs: actual)
    sync = usage_outbox._sync_directory
    sync_calls = []

    def fail_sync(path):
        sync_calls.append(1)
        if len(sync_calls) >= fail_after:
            raise OSError("sandbox durable usage storage unavailable")
        return sync(path)

    monkeypatch.setattr(usage_outbox, "_sync_directory", fail_sync)
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=report_engine.app, raise_app_exceptions=False), base_url="http://engine") as client:
        response = await client.post("/generate", json={"run_id": "accepted-run", "title": "accounting must be durable", "api_key": "sandbox-test-key", "llm_model": "sandbox-model", "documents": [{"content": "sandbox evidence"}]}, headers={"X-Internal-Token": "isolated-billing-secret"})
    assert response.status_code >= 500, "report fallback must not publish success when accounting persistence failed"
    assert len(provider_calls) == fail_after - 1, "accounting failure must never retry a supplier call"


@pytest.mark.asyncio
@pytest.mark.parametrize("tail", ['data: {broken-json\n\n', 'data: {"error":{"message":"late provider stream failure"}}\n\n'])
async def test_observed_stream_usage_survives_later_parser_or_provider_error(billing_sandbox, tail):
    _received, directory = billing_sandbox
    observed = {"id": "observed-stream-request", "model": "sandbox-model", "choices": [], "usage": {"prompt_tokens": 400, "completion_tokens": 100}}

    def provider(_request):
        return httpx.Response(200, headers={"content-type": "text/event-stream"}, content=("data: " + json.dumps(observed) + "\n\n" + tail).encode())

    client = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(Exception):
        await client.chat_json("sandbox-model", [], usage_context={"run_id": "accepted-run", "engine": "insight", "phase": "analyze"})
    records = [json.loads(path.read_text())["event"] for path in directory.glob("*.json")]
    assert len(records) == 1
    event = records[0]
    assert event["usage_status"] == "reported", "later malformed chunk must not discard already observed usage"
    assert (event["prompt_tokens"], event["completion_tokens"]) == (400, 100)
    assert event["provider_request_id"] == "observed-stream-request"
    assert event["actual_model"] == "sandbox-model"

@pytest.mark.asyncio
async def test_insight_dimension_does_not_retry_fatal_usage_durability_failure(billing_sandbox, monkeypatch):
    from engines.insight_engine import main as insight_engine
    from engines.common import usage_outbox
    received, _directory = billing_sandbox
    provider_calls = []

    def provider(_request):
        provider_calls.append(1)
        return httpx.Response(200, json={"id": "dimension-disk-failure", "model": "sandbox-model", "choices": [{"message": {"content": "{}"}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 400, "completion_tokens": 100}})

    client = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(provider))
    client.usage_context = {"run_id": "accepted-run", "engine": "insight", "phase": "analyze"}
    sync = usage_outbox._sync_directory
    calls = []
    def fail_after_response(path):
        calls.append(1)
        if len(calls) >= 2:
            raise OSError("sandbox final usage fsync failure")
        return sync(path)
    monkeypatch.setattr(usage_outbox, "_sync_directory", fail_after_response)
    monkeypatch.setattr(insight_engine, "_RETRY_DELAY_SECONDS", 0)
    with pytest.raises(Exception):
        await insight_engine._run_dimensions(client, [{"id": "doc", "title": "evidence", "content": "sandbox evidence"}], "brand", "accounting failure", "sandbox-model", dimension_ids={"heat"})
    assert len(provider_calls) == 1
    assert len(received["authorizations"]) == 1, "fatal accounting error must not start a new dimension attempt"


@pytest.mark.asyncio
async def test_usage_on_provider_error_chunk_is_recorded(billing_sandbox):
    _received, directory = billing_sandbox
    chunk = {"id": "error-chunk-request", "model": "sandbox-model", "error": {"message": "provider failure"}, "usage": {"prompt_tokens": 400, "completion_tokens": 100}}
    client = LLMClient("https://sandbox.invalid/v1", transport=httpx.MockTransport(lambda _request: httpx.Response(200, headers={"content-type": "text/event-stream"}, content=("data: " + json.dumps(chunk) + "\n\n").encode())))
    with pytest.raises(Exception):
        await client.chat_json("sandbox-model", [], usage_context={"run_id": "accepted-run", "engine": "insight", "phase": "analyze"})
    event = json.loads(next(directory.glob("*.json")).read_text())["event"]
    assert event["usage_status"] == "reported"
    assert (event["prompt_tokens"], event["completion_tokens"], event["provider_request_id"]) == (400, 100, "error-chunk-request")


def test_configured_engine_health_remains_readable_and_workloads_require_token():
    script = """
import json
from fastapi.testclient import TestClient
from engines.insight_engine.main import app as insight
from engines.report_engine.main import app as report
result=[]
for app,path in [(insight,'/analyze'),(report,'/generate')]:
 client=TestClient(app,raise_server_exceptions=False)
 result.append([client.get('/health').status_code,client.post(path,json={}).status_code,client.post(path,json={},headers={'X-Internal-Token':'isolated-billing-secret'}).status_code])
print(json.dumps(result))
"""
    environment = os.environ.copy()
    environment["YUQING_BILLING_SERVICE_TOKEN"] = "isolated-billing-secret"
    result = subprocess.run([sys.executable, "-c", script], env=environment, capture_output=True, text=True, timeout=20)
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == [[200, 403, 200], [200, 403, 200]]
