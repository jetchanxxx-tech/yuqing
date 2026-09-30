"""
Tests for PageScraper (Scrapling-based web scraper).

TDD RED: These tests should fail first with "Scrapling not installed" or
          import errors, then pass after installing Scrapling.
"""
import pytest
from engines.common.scraper import PageScraper, ScrapedDocument, _hash_content, _classify_by_domain


class TestContentHash:
    def test_same_content_same_hash(self):
        h1 = _hash_content("hello world")
        h2 = _hash_content("hello world")
        assert h1 == h2

    def test_different_content_different_hash(self):
        h1 = _hash_content("hello world")
        h2 = _hash_content("hello WORLD")
        assert h1 != h2

    def test_hash_length_is_16(self):
        assert len(_hash_content("test")) == 16


class TestPageScraperInit:
    def test_scraper_creates_without_scrapling_installed(self):
        """Scraper should create even when Scrapling is not installed (lazy import)."""
        scraper = PageScraper()
        assert scraper is not None
        assert scraper._available in (True, False)  # True if installed, False if not

    def test_scraper_defaults(self):
        scraper = PageScraper()
        assert scraper.headless is True
        assert scraper.timeout == 30


class TestScraperFetchNoScrapling:
    def test_fetch_returns_empty_doc_when_not_installed(self):
        """When Scrapling is not installed, fetch() returns doc with error field set."""
        scraper = PageScraper()
        doc = scraper.fetch("https://example.com", source_type="news")
        assert isinstance(doc, ScrapedDocument)
        assert doc.url == "https://example.com"

    @pytest.mark.asyncio
    async def test_search_and_fetch_returns_empty_when_not_installed(self):
        scraper = PageScraper()
        docs = await scraper.search_and_fetch("test", sources=["news"], max_per_source=2)
        assert isinstance(docs, list)
        # Without Scrapling, docs should be empty or error docs
        for d in docs:
            assert isinstance(d, ScrapedDocument)


class TestSourceModeMapping:
    def test_dynamic_sources_map_to_stealth(self):
        for src in ("weibo", "xiaohongshu", "douyin", "bilibili"):
            assert PageScraper._source_mode(src) == "stealth"

    def test_static_sources_map_to_static(self):
        for src in ("news", "rss", "custom_web"):
            assert PageScraper._source_mode(src) == "static"


class TestPresetURLs:
    def test_preset_urls_for_each_source(self):
        for src in ("weibo", "news", "xiaohongshu", "bilibili", "rss", "custom_web"):
            urls = PageScraper._preset_search_urls("雅阁后排", src)
            assert isinstance(urls, list)
            assert len(urls) > 0


class TestDomainClassification:
    """P0 数据源标签修复：URL 域名 → 数据源类型映射"""

    def test_weibo_urls(self):
        assert _classify_by_domain("https://weibo.com/123456") == "weibo"
        assert _classify_by_domain("https://sina.com.cn/news/abc") == "weibo"

    def test_xiaohongshu_urls(self):
        assert _classify_by_domain("https://www.xiaohongshu.com/explore/abc") == "xiaohongshu"
        assert _classify_by_domain("https://xhslink.com/test") == "xiaohongshu"

    def test_bilibili_urls(self):
        assert _classify_by_domain("https://www.bilibili.com/video/BV1xx") == "bilibili"
        assert _classify_by_domain("https://b23.tv/abc123") == "bilibili"

    def test_douyin_urls(self):
        assert _classify_by_domain("https://www.douyin.com/video/123") == "douyin"
        assert _classify_by_domain("https://www.iesdouyin.com/share/abc") == "douyin"

    def test_zhihu_urls(self):
        assert _classify_by_domain("https://www.zhihu.com/question/123") == "zhihu"

    def test_36kr_urls(self):
        assert _classify_by_domain("https://36kr.com/p/123456") == "news"

    def test_unknown_url_returns_custom_web(self):
        assert _classify_by_domain("https://example.com/article/123") == "custom_web"
        assert _classify_by_domain("https://unknown-site.com") == "custom_web"

    def test_classification_is_case_insensitive(self):
        assert _classify_by_domain("https://WEIBO.COM/123") == "weibo"
        assert _classify_by_domain("https://Bilibili.Com/video") == "bilibili"


if __name__ == "__main__":
    pytest.main([__file__, "-v"])

class TestBochaSourceScope:
    def test_platform_classification(self):
        assert _classify_by_domain("https://www.toutiao.com/article/123") == "toutiao"
        assert _classify_by_domain("https://www.ixigua.com/123") == "xigua"
        assert _classify_by_domain("https://mp.weixin.qq.com/s/abc") == "wechat"
        assert _classify_by_domain("https://fakeweibo.com/123") == "custom_web"

    @pytest.mark.asyncio
    async def test_provider_summary_without_page_fetch(self, monkeypatch):
        scraper = PageScraper()
        calls = []

        async def search(keyword, api_key, include="", count=20):
            calls.append(include)
            return [
                {"url": "https://www.toutiao.com/article/123", "title": "test title", "summary": "test provider summary", "snippet": "short",
                 "site_name": "Toutiao", "published_at": "2026-09-30T10:00:00+08:00"},
                {"url": "https://www.bilibili.com/video/123", "title": "other", "summary": "not selected"},
            ]

        monkeypatch.setattr(scraper, "_bocha_search", search)
        monkeypatch.setattr(scraper, "fetch", lambda *args, **kwargs: pytest.fail("must not crawl provider result"))
        docs = await scraper.search_and_fetch("test", ["toutiao"], bocha_key="fixture-key")
        assert calls == ["toutiao.com"]
        assert len(docs) == 1
        assert (docs[0].source_type, docs[0].content, docs[0].published_at) == (
            "toutiao", "test provider summary", "2026-09-30T10:00:00+08:00")

    @pytest.mark.asyncio
    async def test_request_uses_documented_include_and_summary(self, monkeypatch):
        import httpx
        from engines.common import scraper as scraper_module

        def handler(request):
            body = __import__("json").loads(request.content)
            assert body["include"] == "weibo.com|mp.weixin.qq.com"
            assert body["summary"] is True
            assert body["count"] <= 50
            return httpx.Response(200, json={"data": {"webPages": {"value": [{
                "url": "https://weibo.com/123", "name": "test title", "summary": "test summary",
                "siteName": "Weibo", "datePublished": "2026-09-30T10:00:00+08:00",
            }]}}})

        real_client = httpx.AsyncClient
        monkeypatch.setattr(scraper_module.httpx, "AsyncClient", lambda **kwargs: real_client(
            transport=httpx.MockTransport(handler), **kwargs))
        scraper = PageScraper()
        docs = await scraper.search_and_fetch("test", ["weibo", "wechat"], bocha_key="fixture-key")
        assert len(docs) == 1
        assert (docs[0].content, docs[0].published_at) == (
            "test summary", "2026-09-30T10:00:00+08:00")


@pytest.mark.asyncio
async def test_unrelated_and_spa_candidates_are_rejected_before_limit(monkeypatch):
    scraper = PageScraper()
    calls = []
    async def search(keyword, key, include="", count=20):
        calls.append(count)
        return [
            {"url": "https://www.douyin.com/video/1", "title": "DeepSeek FP8", "snippet": "FP8 technology"},
            {"url": "https://www.douyin.com/video/2", "title": "GS8", "summary": "We're sorry but react app doesn't work properly without JavaScript enabled."},
            {"url": "https://www.douyin.com/video/3", "title": "传祺GS8越野版", "summary": "传祺GS8试驾体验"},
        ]
    monkeypatch.setattr(scraper, "_bocha_search", search)
    stats = {}
    docs = await scraper.search_and_fetch("gs8", ["douyin"], max_per_source=1, bocha_key="fixture-key", coverage=stats)
    assert [doc.title for doc in docs] == ["传祺GS8越野版"]
    assert stats["provider_candidates"] == 3
    assert stats["unusable_count"] == 1
    assert stats["irrelevant_count"] == 1
    assert calls == [20]


@pytest.mark.asyncio
async def test_bocha_http_success_without_search_payload_is_not_empty_success(monkeypatch):
    import httpx
    from engines.common import scraper as scraper_module
    real_client = httpx.AsyncClient
    monkeypatch.setattr(scraper_module.httpx,"AsyncClient",lambda **kwargs: real_client(
        transport=httpx.MockTransport(lambda req: httpx.Response(200,json={"code":401,"message":"invalid key"})), **kwargs))
    with pytest.raises(ValueError,match="invalid search response"):
        await PageScraper()._bocha_search("gs8","fixture-key")


@pytest.mark.asyncio
async def test_gs8_twenty_candidate_regression_yields_one_usable_evidence(monkeypatch):
    scraper = PageScraper()
    spa = "We're sorry but react app doesn't work properly without JavaScript enabled."
    candidates = [
        {"url":f"https://jingxuan.douyin.com/m/video/{i}","title":f"高达模型第{i}期","summary":spa}
        for i in range(17)
    ] + [
        {"url":"https://www.douyin.com/video/gl8","title":"别克GL8试驾","summary":"别克商务车评测"},
        {"url":"https://www.douyin.com/video/fp8","title":"DeepSeek FP8 技术","summary":"FP8模型技术争议"},
        {"url":"https://www.douyin.com/video/gs8","title":"传祺GS8越野版","summary":"传祺 GS8 改装体验"},
    ]
    async def search(keyword,key,include="",count=20):
        assert count <= 50 and include == "douyin.com|iesdouyin.com"
        return candidates
    monkeypatch.setattr(scraper,"_bocha_search",search)
    coverage = {}
    docs = await scraper.search_and_fetch("gs8",["douyin"],max_per_source=20,bocha_key="fixture",coverage=coverage)
    assert [d.title for d in docs] == ["传祺GS8越野版"]
    assert (coverage["provider_candidates"],coverage["unusable_count"],coverage["irrelevant_count"]) == (20,17,2)
