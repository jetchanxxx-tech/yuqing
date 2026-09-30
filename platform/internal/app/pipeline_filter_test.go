package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/engine"
)

func TestEngineFetcher_forwardsFilterSnapshot(t *testing.T) {
	var received engine.CrawlReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"documents":[],"total_count":0,"coverage":{"admission_version":"lexical-v1","accepted_count":0}}`))
	}))
	defer server.Close()
	fetcher := &engineFetcher{crawler: engine.NewRealCrawlerEngine(server.URL, "", nil)}
	_, err := fetcher.Fetch(context.Background(), analysis.FetchRequest{
		AnalysisID: "a1", Keywords: []string{"topic"}, Sources: []string{"news"},
		DateFrom: "2026-09-01", DateTo: "2026-09-28", ExcludeWords: []string{"advert"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if received.DateFrom != "2026-09-01" || received.DateTo != "2026-09-28" || len(received.ExcludeWords) != 1 || received.ExcludeWords[0] != "advert" {
		t.Fatalf("forwarded request = %+v", received)
	}
}

func TestEngineFetcherExposesQueryCoverageLimitations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"documents":[],"total_count":0,"coverage":{"admission_version":"lexical-v1","accepted_count":0,"warning":"missing published dates","filter_limitations":"post-fetch only","unverifiable_date_count":2}}`))
	}))
	defer server.Close()

	fetcher := &engineFetcher{crawler: engine.NewRealCrawlerEngine(server.URL, "", nil)}
	result, err := fetcher.FetchWithCoverage(context.Background(), analysis.FetchRequest{DateFrom: "2026-09-01"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Warning, "missing published dates") || !strings.Contains(result.Warning, "post-fetch only") {
		t.Fatalf("source coverage warning lost: %q", result.Warning)
	}
}
