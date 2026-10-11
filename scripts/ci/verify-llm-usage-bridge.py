#!/usr/bin/env python3
"""Hosted actual Python client -> prebuilt Go -> isolated PG ACK-loss acceptance."""
import asyncio
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))


def child(mode):
    import httpx
    from engines.common import llm_client
    from engines.common.llm_client import LLMClient
    from engines.common.usage_bridge import deliver_pending_usage_events
    if mode == "deliver":
        print(asyncio.run(deliver_pending_usage_events()))
        return
    calls = []
    def provider(_request):
        calls.append(1)
        Path(os.environ["K4_SUPPLIER_CALLS"]).write_text(str(len(calls)))
        if mode == "retry-invalid" and len(calls) == 1:
            return httpx.Response(503, json={"error": "sandbox retry"})
        content = "invalid business JSON" if mode == "retry-invalid" else "{}"
        return httpx.Response(200, json={"id": "sandbox-observed", "model": "sandbox-model", "choices": [{"message": {"content": content}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 400, "completion_tokens": 100}})
    llm_client.RETRY_DELAY_SECONDS = 0
    client = LLMClient("https://offline-provider.invalid/v1", transport=httpx.MockTransport(provider))
    try:
        asyncio.run(client.chat_json("sandbox-model", [], usage_context={"run_id": os.environ["K4_RUN_ID"], "engine": "insight", "phase": "analyze"}))
    except ValueError:
        if mode != "retry-invalid":
            raise
    else:
        assert mode != "retry-invalid", "invalid business JSON unexpectedly succeeded"
    assert len(calls) == (2 if mode == "retry-invalid" else 1)


def main():
    import httpx
    import psycopg
    assert os.environ.get("GITHUB_ACTIONS") == "true"
    assert os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted"
    dsn = os.environ["YUQING_TEST_PG_URL"]
    assert "127.0.0.1" in dsn and "yuqing_billing_bridge_test" in dsn
    evidence = ROOT / "test-results" / "billing-bridge"
    evidence.mkdir(parents=True, exist_ok=True)
    secret = "isolated-billing-bridge-secret"
    base = "http://127.0.0.1:8081"
    with tempfile.TemporaryDirectory(prefix="k4-bridge-") as temporary:
        work = Path(temporary)
        config = work / "config.json"
        config.write_text(json.dumps({"server": {"addr": "127.0.0.1:8081"}, "store": {"driver": "postgres"}, "queue": {"driver": "postgres"}, "db": {"primary": dsn}, "auth": {"jwtSecret": "isolated-bridge-jwt-only", "accessTTL": "15m", "refreshTTL": "720h"}, "llm": {"models": [{"id": "sandbox-model", "provider": "sandbox", "inputCostPerM": 4, "outputCostPerM": 16}]}}))
        environment = {**os.environ, "YUQING_CONFIG": str(config), "YUQING_BILLING_SERVICE_TOKEN": secret, "YUQING_PROVIDER_PRICE_VERSION": "sandbox-price-v1", "YUQING_PROVIDER_PRICE_CURRENCY": "CNY"}
        cli = ROOT / "platform/bin/yuqing-cli"
        server_binary = ROOT / "platform/bin/yuqing-server"
        subprocess.run([str(cli), "migrate", "platform"], env=environment, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        log = (evidence / "server.log").open("w")
        server = None
        proxy = None
        transport = httpx.Client(timeout=15, trust_env=False)
        def start_server():
            process = subprocess.Popen([str(server_binary)], env=environment, stdout=log, stderr=subprocess.STDOUT)
            for _ in range(150):
                if process.poll() is not None:
                    raise RuntimeError("prebuilt server exited before health check")
                try:
                    if transport.get(base + "/api/v1/health").status_code == 200:
                        return process
                except httpx.HTTPError:
                    pass
                time.sleep(0.1)
            process.terminate()
            raise RuntimeError("prebuilt server health timed out")
        def request(method, path, body=None, token=""):
            response = transport.request(method, base + "/api/v1" + path, json=body, headers={"Authorization": "Bearer " + token} if token else {})
            assert response.status_code in {200, 201}, f"API failure {response.status_code}: {response.text}"
            return response.json()
        def query(sql, parameters=()):
            with psycopg.connect(dsn) as connection:
                with connection.cursor() as cursor:
                    cursor.execute(sql, parameters)
                    return cursor.fetchall() if cursor.description else []
        try:
            server = start_server()
            normal = request("POST", "/auth/register", {"email": "bridge-normal@example.invalid", "name": "normal bridge", "password": "K4-bridge-sandbox-password"})
            fixed = request("POST", "/auth/register", {"email": "admin@pangu.com", "name": "fixed bridge", "password": "K4-bridge-sandbox-password"})
            fixed_id = fixed["user"]["user_id"]
            for args in [["bootstrap-platform-admin", "--user-id", fixed_id], ["bind-billing-exempt-admin", "--expected-user-id", fixed_id, "--apply"]]:
                subprocess.run([str(cli), *args], env=environment, check=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            fixed = request("POST", "/auth/login", {"email": "admin@pangu.com", "password": "K4-bridge-sandbox-password"})
            normal_analysis = request("POST", "/analyses", {"name": "accepted paid bridge run"}, normal["access_token"])
            query("UPDATE report_credits SET balance=0 WHERE tenant_id=%s", (fixed["user"]["tenant_id"],))
            fixed_analysis = request("POST", "/analyses", {"name": "accepted exempt bridge run"}, fixed["access_token"])
            state = {"drop_ack": True, "late_transition": False, "forwarded": 0}
            mutex = threading.Lock()
            class Proxy(BaseHTTPRequestHandler):
                def log_message(self, *_args):
                    pass
                def do_POST(self):
                    body = self.rfile.read(int(self.headers["Content-Length"]))
                    with mutex:
                        if self.path.endswith("usage-events") and not state["late_transition"]:
                            request("POST", "/analyses/" + normal_analysis["id"] + "/cancel", token=normal["access_token"])
                            query("UPDATE users SET status='disabled' WHERE id=%s", (normal["user"]["user_id"],))
                            state["late_transition"] = True
                        response = transport.post(base + self.path, content=body, headers={"Content-Type": "application/json", "X-Billing-Service-Token": self.headers.get("X-Billing-Service-Token", ""), "X-LLM-Call-Permit": self.headers.get("X-LLM-Call-Permit", "")})
                        if self.path.endswith("usage-events"):
                            assert response.status_code in {200, 201}, response.text
                            assert response.json()["committed"] is True
                            state["forwarded"] += 1
                            if state["drop_ack"]:
                                self.close_connection = True
                                return  # Real PG committed; caller receives no ACK.
                    self.send_response(response.status_code)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(response.content)))
                    self.end_headers()
                    self.wfile.write(response.content)
            proxy = ThreadingHTTPServer(("127.0.0.1", 0), Proxy)
            thread = threading.Thread(target=proxy.serve_forever, daemon=True)
            thread.start()
            outbox = work / "outbox"
            calls = work / "supplier-calls"
            child_env = {**environment, "YUQING_BILLING_URL": f"http://127.0.0.1:{proxy.server_port}", "YUQING_USAGE_OUTBOX_DIR": str(outbox), "K4_RUN_ID": normal_analysis["current_run_id"], "K4_SUPPLIER_CALLS": str(calls)}
            def run_child(mode, env=child_env):
                result = subprocess.run([sys.executable, str(Path(__file__)), "--child", mode], env=env, capture_output=True, text=True, timeout=60)
                assert result.returncode == 0, result.stderr
                return result.stdout.strip()
            run_child("retry-invalid")
            files = list(outbox.glob("*.json"))
            assert len(files) == 2 and calls.read_text() == "2"
            records = [json.loads(path.read_text()) for path in files]
            observed = query("SELECT count(*),sum(prompt_tokens+completion_tokens),sum(quota_tokens),sum(cost_micro_cny),sum(billed_micro_cny) FROM usage_events WHERE run_id=%s", (normal_analysis["current_run_id"],))[0]
            assert observed == (2, 500, 500, 3200, 0), observed
            identities = query("SELECT DISTINCT user_id,tenant_id FROM usage_events WHERE run_id=%s", (normal_analysis["current_run_id"],))
            assert identities == [(normal["user"]["user_id"], normal["user"]["tenant_id"])], identities
            assert query("SELECT state FROM analyses WHERE id=%s", (normal_analysis["id"],)) == [("canceled",)]
            assert query("SELECT balance FROM report_credits WHERE tenant_id=%s", (normal["user"]["tenant_id"],)) == [(1,)]
            server.terminate(); server.wait(timeout=15); server = start_server()
            state["drop_ack"] = False
            assert run_child("deliver") == "2"
            assert list(outbox.glob("*.json")) == [] and calls.read_text() == "2"
            assert query("SELECT count(*) FROM usage_events WHERE run_id=%s", (normal_analysis["current_run_id"],)) == [(2,)]
            changed = dict(records[0]["event"]); changed["outcome"] = "conflicting replay"
            conflict = transport.post(base + "/internal/v1/billing/usage-events", json=changed, headers={"X-Billing-Service-Token": secret, "X-LLM-Call-Permit": records[0]["permit"]})
            assert conflict.status_code == 409
            fixed_env = {**child_env, "K4_RUN_ID": fixed_analysis["current_run_id"], "K4_SUPPLIER_CALLS": str(work / "fixed-calls")}
            run_child("single", fixed_env)
            assert query("SELECT prompt_tokens,completion_tokens,quota_tokens,cost_micro_cny,billed_micro_cny,billing_exempt,user_id FROM usage_events WHERE run_id=%s", (fixed_analysis["current_run_id"],)) == [(400, 100, 0, 3200, 0, True, fixed_id)]
            assert query("SELECT count(*) FROM credit_transactions WHERE tenant_id=%s AND reason='consume'", (fixed["user"]["tenant_id"],)) == [(0,)]
            summary = {"result": "PASS", "normal_events": 2, "actual_tokens": 500, "normal_quota_tokens": 500, "normal_known_cost_micro_cny": 3200, "fixed_quota_tokens": 0, "fixed_known_cost_micro_cny": 3200, "provider_attempts_before_and_after_replay": 2, "go_restarted": True, "python_restarted": True, "late_cancel_and_disabled_actor_settled": True, "real_supplier_calls": 0}
            (evidence / "result.json").write_text(json.dumps(summary, indent=2) + "\n")
            print(json.dumps(summary))
        finally:
            if proxy:
                proxy.shutdown(); proxy.server_close()
            if server:
                server.terminate(); server.wait(timeout=15)
            transport.close(); log.close()


if __name__ == "__main__":
    if len(sys.argv) > 2 and sys.argv[1] == "--child":
        child(sys.argv[2])
    else:
        main()
