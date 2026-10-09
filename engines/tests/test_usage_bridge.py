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
