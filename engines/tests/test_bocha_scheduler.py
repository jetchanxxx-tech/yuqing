"""Supplier concurrency/rate/retry contract shared across accepted query runs."""
import asyncio
import json

import httpx
import pytest

from engines.common import scraper as module


@pytest.mark.asyncio
async def test_service_limiter_bounds_concurrent_jobs_and_start_spacing(monkeypatch):
    from engines.common.scraper import ProviderLimiter
    limiter = ProviderLimiter(interval=0.02, concurrency=2)
    starts = []
    in_flight = 0
    maximum = 0

    async def one():
        nonlocal in_flight, maximum
        async with limiter.slot(asyncio.get_running_loop().time() + 2):
            starts.append(asyncio.get_running_loop().time())
            in_flight += 1
            maximum = max(maximum, in_flight)
            await asyncio.sleep(0.06)
            in_flight -= 1

    await asyncio.gather(*(one() for _ in range(6)))
    assert maximum <= 2
    assert all(b - a >= 0.018 for a, b in zip(starts, starts[1:]))


@pytest.mark.asyncio
@pytest.mark.parametrize("status,expected", [(429, 2), (503, 2), (400, 1), (401, 1)])
async def test_supplier_retry_is_once_only_transient_and_uses_real_http(monkeypatch, status, expected):
    calls = []
    def handler(request):
        calls.append(json.loads(request.content))
        return httpx.Response(status, headers={"Retry-After": "0"}, json={"error": "offline"})
    actual_client = httpx.AsyncClient
    monkeypatch.setattr(module.httpx, "AsyncClient", lambda **kwargs: actual_client(
        **{**kwargs, "transport": httpx.MockTransport(handler)}))
    with pytest.raises(httpx.HTTPStatusError):
        await module.PageScraper()._bocha_search("iPhone18 Pro续航", "fixture", include="toutiao.com")
    assert len(calls) == expected
