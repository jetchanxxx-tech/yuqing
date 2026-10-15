"""Isolated Scrapling fetching through a DNS-pinned public-only egress proxy.

Every connection (including redirects, scripts, XHR and browser subresources)
uses this proxy. The worker owns its complete browser process group; deadline
or cancellation reaps it before returning. No provider credentials are inherited.
"""
import asyncio
import ctypes
from pathlib import Path
from contextlib import contextmanager
import ipaddress
import json
import os
import resource
import select
import signal
import socket
import socketserver
import sys
import threading
import time
from urllib.parse import urlsplit, urlunsplit, urljoin

from engines.common.platform_extractors import MAX_BODY_BYTES, extract_page


class UnsafeDestination(ValueError):
    pass


def is_public_http_url(url):
    try:
        parsed = urlsplit(str(url or ""))
        host = (parsed.hostname or "").lower().rstrip(".")
        if parsed.scheme not in {"http", "https"} or not host or parsed.username or parsed.password:
            return False
        if parsed.port not in {None, 80, 443} or "%" in host or "\\" in str(url):
            return False
        if host in {"localhost", "metadata.google.internal"} or host.endswith((".localhost", ".local", ".internal")):
            return False
        try:
            address = ipaddress.ip_address(host)
        except ValueError:
            # Numeric alternative IPv4 forms must not bypass ordinary IP checks.
            if host.replace(".", "").isdigit() or host.startswith("0x"):
                return False
            return "." in host
        return address.is_global and not address.is_multicast and not address.is_unspecified
    except ValueError:
        return False


def resolve_public_addresses(host, port):
    """Reject mixed DNS answers and pin a checked IP for the actual socket."""
    addresses = []
    for family, _, _, _, endpoint in socket.getaddrinfo(host, port, type=socket.SOCK_STREAM):
        if family not in {socket.AF_INET, socket.AF_INET6}:
            raise UnsafeDestination("unsafe_destination")
        value = endpoint[0]
        address = ipaddress.ip_address(value)
        if not address.is_global or address.is_multicast or address.is_unspecified:
            raise UnsafeDestination("unsafe_destination")
        if value not in addresses:
            addresses.append(value)
    if not addresses:
        raise UnsafeDestination("unresolved_destination")
    return addresses


class _ProxyHandler(socketserver.StreamRequestHandler):
    def handle(self):
        upstream = None
        try:
            first = self.rfile.readline(8193)
            if len(first) > 8192:
                raise UnsafeDestination("request_header_limit")
            method, target, _ = first.decode("latin1").strip().split(" ", 2)
            raw_headers = []
            size = len(first)
            while True:
                line = self.rfile.readline(8193)
                size += len(line)
                if size > 65536:
                    raise UnsafeDestination("request_header_limit")
                if line in {b"\r\n", b"\n", b""}:
                    break
                raw_headers.append(line)
            url = "https://" + target + "/" if method == "CONNECT" else target
            if not is_public_http_url(url):
                raise UnsafeDestination("unsafe_destination")
            parsed = urlsplit(url)
            port = parsed.port or (443 if parsed.scheme == "https" else 80)
            addresses = resolve_public_addresses(parsed.hostname, port)
            # Resolve once for this connection and pass only the verified IP.
            upstream = socket.create_connection((addresses[0], port), timeout=8)
            upstream.settimeout(1)
            if method == "CONNECT":
                self.wfile.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                self.wfile.flush()
            else:
                if method not in {"GET", "HEAD", "POST", "OPTIONS"}:
                    raise UnsafeDestination("unsupported_method")
                path = urlunsplit(("", "", parsed.path or "/", parsed.query, ""))
                upstream.sendall(f"{method} {path} HTTP/1.1\r\n".encode("latin1"))
                for line in raw_headers:
                    name = line.split(b":", 1)[0].strip().lower()
                    if name not in {b"proxy-connection", b"proxy-authorization", b"connection", b"accept-encoding"}:
                        upstream.sendall(line)
                upstream.sendall(b"Connection: close\r\nAccept-Encoding: identity\r\n\r\n")
            self.connection.setblocking(False)
            upstream.setblocking(False)
            received = 0
            sent = 0
            until = time.monotonic() + 30
            while time.monotonic() < until:
                readable, _, _ = select.select([self.connection, upstream], [], [], min(0.5, max(0, until-time.monotonic())))
                for source in readable:
                    chunk = source.recv(65536)
                    if not chunk:
                        return
                    if source is upstream:
                        received += len(chunk)
                        if received > MAX_BODY_BYTES:
                            raise UnsafeDestination("response_body_limit")
                        self.connection.sendall(chunk)
                    else:
                        sent += len(chunk)
                        if sent > MAX_BODY_BYTES:
                            raise UnsafeDestination("request_body_limit")
                        upstream.sendall(chunk)
        except UnsafeDestination as exc:
            self.server.blocked_reason = str(exc)
            try:
                self.wfile.write(b"HTTP/1.1 403 Forbidden\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
            except OSError:
                pass
        except (OSError, ValueError):
            pass
        finally:
            if upstream:
                upstream.close()


class _Proxy(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True
    blocked_reason = ""


@contextmanager
def guarded_proxy():
    server = _Proxy(("127.0.0.1", 0), _ProxyHandler)
    thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.05}, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}", server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=1)


def fetch_in_worker(url, source_type, mode="static"):
    """The synchronous worker is invoked only inside its isolated process."""
    from engines.common.scraper import _classify_by_domain
    if not is_public_http_url(url) or _classify_by_domain(url) != source_type:
        return {"reason": "unsafe_or_unselected_url", "status": "failed"}
    try:
        # Validate before creating either a browser or a static session.
        parsed = urlsplit(url)
        resolve_public_addresses(parsed.hostname, parsed.port or (443 if parsed.scheme == "https" else 80))
        from scrapling.fetchers import Fetcher, DynamicFetcher
        from scrapling import Selector
        with guarded_proxy() as (proxy_url, proxy):
            # curl's environment must not silently bypass the guarded proxy.
            os.environ["NO_PROXY"] = ""
            os.environ["no_proxy"] = ""
            if mode == "static":
                target = url
                for redirect_count in range(6):
                    chunks = []
                    received = 0
                    def bounded_content(chunk):
                        nonlocal received
                        received += len(chunk)
                        if received > MAX_BODY_BYTES:
                            proxy.blocked_reason = "response_body_limit"
                            return 0
                        chunks.append(bytes(chunk))
                        return len(chunk)
                    page = Fetcher.get(target, proxy=proxy_url, timeout=15, retries=1, max_redirects=0,
                                       headers={"Accept-Encoding": "identity"}, accept_encoding="identity",
                                       follow_redirects=False, content_callback=bounded_content)
                    if getattr(page, "status", 200) not in {301, 302, 303, 307, 308}:
                        break
                    headers = getattr(page, "headers", {})
                    location = headers.get("location") or headers.get("Location") or ""
                    target = urljoin(target, location)
                    if not location or not is_public_http_url(target) or _classify_by_domain(target) != source_type:
                        return {"status": "failed", "reason": "unsafe_redirect", "final_url": target}
                else:
                    return {"status": "failed", "reason": "redirect_limit"}
            else:
                def setup(page):
                    try:
                        def guard(route):
                            target = route.request.url
                            if not is_public_http_url(target):
                                return route.abort()
                            if route.request.resource_type in {"image", "media", "font", "stylesheet"}:
                                return route.abort()
                            if route.request.is_navigation_request() and route.request.frame == page.main_frame:
                                if _classify_by_domain(target) != source_type:
                                    proxy.blocked_reason = "source_redirect_mismatch"
                                    return route.abort()
                                # Fetch this hop only. A redirect is inspected
                                # before the browser can reach its destination.
                                response = route.fetch(max_redirects=0, timeout=25_000,
                                                       headers={**route.request.headers, "accept-encoding": "identity"})
                                location = response.headers.get("location", "")
                                if response.status in {301, 302, 303, 307, 308} and location:
                                    destination = urljoin(target, location)
                                    if not is_public_http_url(destination) or _classify_by_domain(destination) != source_type:
                                        proxy.blocked_reason = "unsafe_redirect"
                                        return route.abort()
                                body = response.body()
                                if len(body) > MAX_BODY_BYTES:
                                    proxy.blocked_reason = "response_body_limit"
                                    return route.abort()
                                return route.fulfill(response=response, body=body)
                            # The proxy validates and pins every public
                            # auxiliary resource connection as well.
                            return route.continue_(headers={**route.request.headers, "accept-encoding": "identity"})
                        page.unroute_all(behavior="ignoreErrors")
                        page.route("**/*", guard)
                        page.context.route("**/*", guard)
                        page.context.on("page", lambda popup: popup.close())
                        page.route_web_socket("**/*", lambda websocket: websocket.close())
                    except Exception:
                        proxy.blocked_reason = "browser_guard_setup_failed"
                        page.close()
                        raise
                page = DynamicFetcher.fetch(
                    url, headless=True, proxy=proxy_url, network_idle=False, timeout=25_000,
                    disable_resources=True, page_setup=setup, retries=1, google_search=False,
                    extra_flags=["--proxy-bypass-list=<-loopback>", "--disable-quic",
                                 "--force-webrtc-ip-handling-policy=disable_non_proxied_udp"],
                    additional_args={"service_workers": "block", "accept_downloads": False},
                )
            final_url = str(getattr(page, "url", "") or url)
            if proxy.blocked_reason:
                return {"status": "failed", "reason": proxy.blocked_reason, "final_url": final_url}
            if not is_public_http_url(final_url) or _classify_by_domain(final_url) != source_type:
                return {"status": "failed", "reason": "unsafe_redirect", "final_url": final_url}
            status_code = getattr(page, "status", 200)
            if status_code not in range(200, 300):
                return {"status": "failed", "reason": f"http_{status_code}", "final_url": final_url}
            parsed_page = Selector(b"".join(chunks), url=final_url) if mode == "static" else page
            extracted = extract_page(parsed_page, source_type)
            return {**extracted, "status": "fetched" if extracted["content"] else "failed", "final_url": final_url,
                    "content_kind": "body" if extracted["content"] else "summary"}
    except UnsafeDestination:
        return {"status": "failed", "reason": "unsafe_destination"}
    except ImportError:
        return {"status": "failed", "reason": "scrapling_unavailable"}
    except Exception as exc:
        if "proxy" in locals() and proxy.blocked_reason:
            return {"status": "failed", "reason": proxy.blocked_reason}
        # No exception messages: browser/HTTP errors can contain URLs/tokens.
        name = type(exc).__name__
        reason = "browser_missing" if "Executable doesn't exist" in str(exc) else "fetch_timeout" if "Timeout" in name else name
        return {"status": "failed", "reason": reason}


def process_tree(root_pid):
    """Linux process identities including detached browser sessions/groups."""
    table = {}
    for path in Path("/proc").glob("[0-9]*/stat"):
        try:
            raw = path.read_text()
            fields = raw[raw.rfind(")") + 2:].split()
            table[int(path.parent.name)] = (int(fields[1]), int(fields[19]), fields[0])
        except (OSError, ValueError, IndexError):
            continue
    owned = {root_pid}
    while True:
        additions = {pid for pid, (parent, _, _) in table.items() if parent in owned}
        if additions <= owned:
            break
        owned |= additions
    return {pid: (table[pid][1], table[pid][2]) for pid in owned if pid in table}


def _same_process(pid, started):
    try:
        raw = Path(f"/proc/{pid}/stat").read_text()
        fields = raw[raw.rfind(")") + 2:].split()
        return int(fields[19]) == started
    except (OSError, ValueError, IndexError):
        return False


def terminate_process_tree(root_pid):
    """Freeze owners first, enumerate to a fixed point, then kill all sessions.

    Playwright launches Chromium detached=true, so a process-group-only kill
    is insufficient. Start times prevent PID reuse from targeting others.
    """
    owned = process_tree(root_pid)
    for _ in range(8):
        for pid, (started, _) in owned.items():
            if _same_process(pid, started):
                try:
                    os.kill(pid, signal.SIGSTOP)
                except ProcessLookupError:
                    pass
        found = process_tree(root_pid)
        new = {pid: identity for pid, identity in found.items() if pid not in owned}
        if not new:
            break
        owned.update(new)
    for pid, (started, _) in sorted(owned.items(), key=lambda item: item[0] == root_pid):
        if _same_process(pid, started):
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
    # Also terminate any ordinary worker-group descendants whose leader may
    # already have exited. Detached browser ownership uses the PPID tree above.
    try:
        os.killpg(root_pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


def enable_subreaper():
    """Orphaned detached browser descendants remain children of this worker."""
    if sys.platform == "linux":
        if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0) != 0:
            raise OSError("could not enable isolated worker child ownership")


def reap_worker_descendants():
    owned = process_tree(os.getpid())
    owned.pop(os.getpid(), None)
    for pid, (started, _) in owned.items():
        if _same_process(pid, started):
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
    until = time.monotonic() + 2
    while time.monotonic() < until:
        try:
            pid, _ = os.waitpid(-1, os.WNOHANG)
            if pid == 0:
                time.sleep(0.01)
            elif pid < 0:
                break
        except ChildProcessError:
            break


async def run_fetch_subprocess(url, source_type, mode="static", *, deadline=None, command=None):
    """Deadline/cancellation includes queueing, startup, work and process cleanup."""
    loop = asyncio.get_running_loop()
    remaining = min(18 if mode == "static" else 28, (deadline - loop.time()) if deadline else 30)
    if remaining <= 0:
        return {"status": "not_attempted", "reason": "fetch_deadline"}
    allowed_env = {key: value for key, value in os.environ.items() if key in {
        "PATH", "LANG", "LC_ALL", "LD_LIBRARY_PATH", "PYTHONPATH", "PLAYWRIGHT_BROWSERS_PATH",
        "SSL_CERT_FILE", "SSL_CERT_DIR", "FONTCONFIG_PATH", "TMPDIR", "DISPLAY", "HOME",
    }}
    # The optional command is a Python-only test seam; never accepted via HTTP.
    if command is not None:
        allowed_env = dict(os.environ)
    worker = await asyncio.create_subprocess_exec(
        *(command or [sys.executable, "-m", "engines.common.safe_fetch"]),
        stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL,
        start_new_session=True, env=allowed_env,
    )
    try:
        data = json.dumps({"url": url, "source_type": source_type, "mode": mode}).encode()
        output, _ = await asyncio.wait_for(worker.communicate(data), timeout=remaining)
        if worker.returncode != 0:
            return {"status": "failed", "reason": "fetch_worker_failed"}
        return json.loads(output.decode().strip().splitlines()[-1])
    except asyncio.TimeoutError:
        return {"status": "failed", "reason": "fetch_timeout"}
    finally:
        terminate_process_tree(worker.pid)
        await asyncio.shield(worker.wait())


if __name__ == "__main__":
    enable_subreaper()
    # Hard per-worker file cap also bounds browser download/temp-file abuse.
    resource.setrlimit(resource.RLIMIT_FSIZE, (MAX_BODY_BYTES, MAX_BODY_BYTES))
    try:
        request = json.loads(sys.stdin.buffer.read(65536))
        result = fetch_in_worker(request["url"], request["source_type"], request.get("mode", "static"))
        print(json.dumps(result, ensure_ascii=False))
    finally:
        reap_worker_descendants()
