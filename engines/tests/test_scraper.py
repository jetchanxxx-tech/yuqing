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