"""Network admission denies every non-public destination before any fetch."""
import pytest
from engines.common.scraper import _is_public_http_url


@pytest.mark.parametrize("url", [
    "http://127.0.0.1/x", "http://[::1]/", "http://[::ffff:127.0.0.1]/", "http://169.254.169.254/",
    "http://172.16.3.2/", "http://100.64.0.2/", "http://0.0.0.0/", "http://224.0.0.1/",
    "http://user:password@weibo.com/", "file:///etc/passwd", "https://localhost/",
])
def test_unsafe_url_is_never_fetched(url):
    assert not _is_public_http_url(url)


def test_dns_answer_with_any_private_address_fails_closed(monkeypatch):
    import socket
    from engines.common.safe_fetch import resolve_public_addresses
    monkeypatch.setattr(socket, "getaddrinfo", lambda *args, **kwargs: [
        (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", 443)),
        (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("172.20.0.2", 443)),
    ])
    with pytest.raises(ValueError):
        resolve_public_addresses("weibo.com", 443)


def test_dns_rebinding_is_rechecked_for_each_connection(monkeypatch):
    import socket
    from engines.common.safe_fetch import resolve_public_addresses
    answers = iter(["8.8.8.8", "169.254.169.254"])
    monkeypatch.setattr(socket, "getaddrinfo", lambda *args, **kwargs: [
        (socket.AF_INET, socket.SOCK_STREAM, 6, "", (next(answers), 443)),
    ])
    assert resolve_public_addresses("weibo.com", 443) == ["8.8.8.8"]
    with pytest.raises(ValueError):
        resolve_public_addresses("weibo.com", 443)
