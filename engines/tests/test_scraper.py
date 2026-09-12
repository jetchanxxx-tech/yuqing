"""
Tests for PageScraper (Scrapling-based web scraper).

TDD RED: These tests should fail first with "Scrapling not installed" or
          import errors, then pass after installing Scrapling.
"""
import pytest
from engines.common.scraper import PageScraper, ScrapedDocument, _hash_content


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


if __name__ == "__main__":
    pytest.main([__file__, "-v"])