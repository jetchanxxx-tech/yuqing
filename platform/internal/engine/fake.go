package engine

import "context"

// FakeCrawlerEngine simulates the crawler engine for tests and local demos.
// Crawl always succeeds without any external side effects.
type FakeCrawlerEngine struct{}

var _ CrawlerEngine = FakeCrawlerEngine{}

// Crawl simulates a crawl: it records nothing and returns nil.
func (FakeCrawlerEngine) Crawl(_ context.Context, _ *CrawlReq) error {
	return nil
}
