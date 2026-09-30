package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealCrawlerEngineSendsConfiguredFilters(t *testing.T) {
	var got searchReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode search request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"documents":[],"total_count":0,"coverage":{"admission_version":"lexical-v1","accepted_count":0}}`))
	}))
	defer server.Close()

	crawler := NewRealCrawlerEngine(server.URL, "", nil)
	_, err := crawler.Search(context.Background(), &CrawlReq{
		Keywords: []string{"品牌"}, Sources: []string{"news"},
		DateFrom: "2026-09-01", DateTo: "2026-09-28", ExcludeWords: []string{"歧义"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.DateFrom != "2026-09-01" || got.DateTo != "2026-09-28" ||
		len(got.ExcludeWords) != 1 || got.ExcludeWords[0] != "歧义" {
		t.Fatalf("configured filters were not forwarded: %+v", got)
	}
}

func TestRealCrawlerEngineExposesCoverageWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"documents":[],"total_count":0,"coverage":{"admission_version":"lexical-v1","accepted_count":0,"warning":"missing published dates","filter_limitations":"post-fetch only","unverifiable_date_count":2}}`))
	}))
	defer server.Close()

	crawler := NewRealCrawlerEngine(server.URL, "", nil)
	result, err := crawler.SearchWithCoverage(context.Background(), &CrawlReq{DateFrom: "2026-09-01"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.Warning != "missing published dates" || result.Coverage.UnverifiableDateCount != 2 {
		t.Fatalf("coverage lost in transport: %+v", result.Coverage)
	}
}

func TestRealCrawlerRejectsLegacyAdmissionProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"documents":[],"total_count":0}`))
	}))
	defer server.Close()
	_, err := NewRealCrawlerEngine(server.URL, "", nil).SearchWithCoverage(context.Background(), &CrawlReq{})
	if err == nil {
		t.Fatal("missing admission protocol must not be treated as zero results")
	}
}
