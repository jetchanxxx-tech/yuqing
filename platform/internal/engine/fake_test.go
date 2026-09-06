package engine

import (
	"context"
	"testing"
)

func TestFakeCrawlerEngine_CrawlReturnsNil(t *testing.T) {
	f := FakeCrawlerEngine{}
	req := &CrawlReq{
		Sources:    []string{"weibo", "news"},
		Keywords:   []string{"雅阁后排"},
		AnalysisID: "01ABCDEFGHJKMNPQRSTVWXYZ",
		MaxDepth:   2,
	}
	if err := f.Crawl(context.Background(), req); err != nil {
		t.Fatalf("FakeCrawlerEngine.Crawl returned error: %v", err)
	}
}

func TestFakeCrawlerEngine_CrawlAcceptsNilRequest(t *testing.T) {
	f := FakeCrawlerEngine{}
	// A simulated crawl must never fail, even for an empty request.
	if err := f.Crawl(context.Background(), nil); err != nil {
		t.Fatalf("FakeCrawlerEngine.Crawl(nil) returned error: %v", err)
	}
}

func TestFakeCrawlerEngine_SatisfiesContract(t *testing.T) {
	// Compile-time contract: usable wherever a real CrawlerEngine is expected.
	var _ CrawlerEngine = FakeCrawlerEngine{}
}
