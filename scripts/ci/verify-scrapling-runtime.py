#!/usr/bin/env python3
"""Hosted-only real Scrapling fixture probe with no production private-URL flag."""
import asyncio
from contextlib import contextmanager
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import sys
import tempfile
import threading
import time
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
FIXTURES = ROOT / "engines/tests/fixtures/scrapling"
HOST = "yuqing-offline.weibo.com"
PUBLIC_IP = "8.8.8.8"


@contextmanager
def fixture_network(port):
    """Replace only DNS/dial boundaries; real proxy and Scrapling still run."""
    import engines.common.safe_fetch as safe
    original_resolve = safe.resolve_public_addresses
    original_connect = socket.create_connection
    def resolve(host, target_port):
        return [PUBLIC_IP] if host == HOST else original_resolve(host, target_port)
    def connect(address, *args, **kwargs):
        if address[0] == PUBLIC_IP:
            return original_connect(("127.0.0.1", port), *args, **kwargs)
        return original_connect(address, *args, **kwargs)
    with patch.object(safe, "resolve_public_addresses", resolve), patch.object(socket, "create_connection", connect):
        yield


class Handler(BaseHTTPRequestHandler):
    forbidden_hits = 0
    def log_message(self, *_args):
        pass
    def do_GET(self):
        if self.path == "/private-target":
            Handler.forbidden_hits += 1
        if self.path == "/hang":
            time.sleep(35)
        if self.path == "/redirect-private":
            self.send_response(302)
            self.send_header("Location", "http://169.254.169.254/latest/meta-data")
            self.end_headers()
            return
        dynamic = self.path in {"/dynamic", "/hang"}
        body = (FIXTURES / ("dynamic.html" if dynamic else "wechat.html")).read_text()
        if dynamic:
            body = body.replace("</body>", "<script>fetch('http://127.0.0.1:" + str(self.server.server_port) + "/private-target').catch(()=>{});</script></body>")
        payload = body.encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        try:
            self.wfile.write(payload)
        except BrokenPipeError:
            pass


def living_owned(identities):
    import engines.common.safe_fetch as safe
    alive = []
    for pid, (started, _) in identities.items():
        if safe._same_process(pid, started):
            try:
                raw = Path(f"/proc/{pid}/stat").read_text()
                if raw[raw.rfind(")") + 2:].split()[0] != "Z":
                    alive.append(pid)
            except OSError:
                pass
    return alive


async def cancellation_probe(port):
    from engines.common.safe_fetch import run_fetch_subprocess, process_tree
    with tempfile.TemporaryDirectory(prefix="yuqing-browser-cleanup-") as directory:
        pidfile = Path(directory) / "pid"
        task = asyncio.create_task(run_fetch_subprocess(f"http://{HOST}/hang", "weibo", "dynamic",
            deadline=asyncio.get_running_loop().time() + 30,
            command=[sys.executable, __file__, "--worker", str(port), str(pidfile)]))
        for _ in range(100):
            if pidfile.exists():
                break
            await asyncio.sleep(0.05)
        assert pidfile.exists(), "fetch process did not start"
        group = int(pidfile.read_text())
        for _ in range(100):
            if len(process_tree(group)) >= 4:
                break
            await asyncio.sleep(0.05)
        identities = process_tree(group)
        before = living_owned(identities)
        assert len(before) >= 4, "actual browser children did not start"
        task.cancel()
        try:
            await task
        except asyncio.CancelledError:
            pass
        for _ in range(50):
            if not living_owned(identities):
                break
            await asyncio.sleep(0.05)
        assert living_owned(identities) == [], "detached browser descendants survived cancellation"
        return {"actual_processes_before_cancel": len(before), "running_processes_after_cancel": 0}


def main():
    from engines.common.safe_fetch import fetch_in_worker
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        with fixture_network(server.server_port):
            static = fetch_in_worker(f"http://{HOST}/static", "weibo", "static")
            assert static["status"] == "fetched", static
            assert "iPhone 18 Pro续航体验优秀" in static["content"]
            assert "脚本" not in static["content"] and "导航" not in static["content"]
            dynamic = fetch_in_worker(f"http://{HOST}/dynamic", "weibo", "dynamic")
            assert dynamic["status"] == "fetched", dynamic
            assert "动态正文已加载" in dynamic["content"]
            assert Handler.forbidden_hits == 0, "browser reached a loopback subresource"
            redirect = fetch_in_worker(f"http://{HOST}/redirect-private", "weibo", "static")
            assert redirect["status"] == "failed" and redirect["reason"] in {"unsafe_destination", "unsafe_redirect"}, redirect
        cleanup = asyncio.run(cancellation_probe(server.server_port))
        result = {"scrapling": "0.4.15", "static": "passed", "browser_javascript": "passed",
                  "loopback_subresource_hits": Handler.forbidden_hits, "private_redirect": "blocked",
                  "browser_cancellation": cleanup,
                  "static_content_sha256": hashlib.sha256(static["content"].encode()).hexdigest(),
                  "dynamic_content_sha256": hashlib.sha256(dynamic["content"].encode()).hexdigest()}
        output = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("scrapling-runtime-evidence.json")
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(result, indent=2) + "\n")
        print(json.dumps(result))
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--worker":
        from engines.common.safe_fetch import fetch_in_worker, enable_subreaper, reap_worker_descendants
        enable_subreaper()
        Path(sys.argv[3]).write_text(str(os.getpid()))
        try:
            with fixture_network(int(sys.argv[2])):
                print(json.dumps(fetch_in_worker(f"http://{HOST}/hang", "weibo", "dynamic")))
        finally:
            reap_worker_descendants()
    else:
        main()
