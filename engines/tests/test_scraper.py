"""Actual candidate transport and source taxonomy; semantic admission lives at /search."""
import json
import httpx
import pytest

from engines.common.scraper import PageScraper, _hash_content, _classify_by_domain
from engines.common import scraper as module


def test_content_hash_is_stable_distinct_and_sixteen_chars():
    assert len(_hash_content("hello")) == 16
    assert _hash_content("hello") == _hash_content("hello")
    assert _hash_content("hello") != _hash_content("Hello")


def test_scraper_initialization_and_real_browser_routing():
    instance = PageScraper()
    assert instance.headless and instance.timeout == 30
    assert instance._available in (True, False)
    assert instance._source_mode("wechat") == "static"
    assert instance._source_mode("douyin") == "dynamic"
    assert instance._source_mode("xigua") == "dynamic"


@pytest.mark.parametrize("url,source", [
    ("https://weibo.com/123", "weibo"), ("https://weibo.cn/123", "weibo"),
    ("https://sina.com.cn/news/abc", "news"), ("https://www.xiaohongshu.com/explore/abc", "xiaohongshu"),
    ("https://xhslink.com/test", "xiaohongshu"), ("https://www.bilibili.com/video/BV1xx", "bilibili"),
    ("https://b23.tv/abc", "bilibili"), ("https://www.douyin.com/video/1", "douyin"),
    ("https://www.iesdouyin.com/share/1", "douyin"), ("https://www.zhihu.com/question/1", "zhihu"),
    ("https://36kr.com/p/1", "news"), ("https://news.qq.com/rain/1", "news"),
    ("https://WEIBO.COM/1", "weibo"), ("https://www.toutiao.com/article/1", "toutiao"),
    ("https://www.ixigua.com/1", "xigua"), ("https://mp.weixin.qq.com/s/abc", "wechat"),
    ("https://fakeweibo.com/1", "custom_web"), ("https://weibo.com.evil.org/1", "custom_web"),
    ("https://example.com/1", "custom_web"),
])
def test_source_is_classified_by_exact_domain_suffix(url, source):
    assert _classify_by_domain(url) == source


@pytest.mark.asyncio
async def test_old_live_lexical_route_and_presets_are_disabled():
    with pytest.raises(RuntimeError, match="authenticated semantic-v2"):
        await PageScraper().search_and_fetch("test", sources=["news"], bocha_key="fixture")


@pytest.mark.asyncio
async def test_missing_scrapling_is_truthful_summary_fallback_signal(monkeypatch):
    instance = PageScraper()
    monkeypatch.setattr(instance, "_available", False)
    outcome = await instance.fetch_candidate("https://weibo.com/1", "weibo")
    assert outcome.status == "failed" and outcome.reason == "scrapling_unavailable"
    assert not outcome.content


@pytest.mark.asyncio
async def test_actual_bocha_http_includes_domains_summary_freshness_and_maps_fields(monkeypatch):
    requests = []
    def handler(request):
        requests.append(json.loads(request.content))
        return httpx.Response(200, json={"data": {"webPages": {"value": [{
            "url": "https://weibo.com/123", "name": "真实标题", "summary": "网页摘要", "snippet": "短摘要",
            "siteName": "微博", "datePublished": "2026-10-12T10:00:00+08:00",
        }]}}})
    client = httpx.AsyncClient
    monkeypatch.setattr(module.httpx, "AsyncClient", lambda **kwargs: client(**{**kwargs, "transport": httpx.MockTransport(handler)}))
    values = await PageScraper()._bocha_search("主题表达", "fixture-key", include="weibo.com|weibo.cn", count=500, freshness="2026-10-01..2026-10-15")
    assert requests == [{"query": "主题表达", "count": 50, "summary": True,
                         "freshness": "2026-10-01..2026-10-15", "include": "weibo.com|weibo.cn"}]
    assert values[0] == {"url": "https://weibo.com/123", "title": "真实标题", "summary": "网页摘要", "snippet": "短摘要",
                         "site_name": "微博", "published_at": "2026-10-12T10:00:00+08:00"}


@pytest.mark.asyncio
async def test_bocha_http_success_without_payload_is_never_empty_success(monkeypatch):
    client = httpx.AsyncClient
    monkeypatch.setattr(module.httpx, "AsyncClient", lambda **kwargs: client(**{**kwargs,
        "transport": httpx.MockTransport(lambda _: httpx.Response(200, json={"code": 401, "message": "bad key"}))}))
    with pytest.raises(ValueError, match="invalid search response"):
        await PageScraper()._bocha_search("主题", "fixture-key")
