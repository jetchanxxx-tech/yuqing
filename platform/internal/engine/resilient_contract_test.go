package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResilientQueryRetainsUncertainMaterialsWithoutPromotingToDirect(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"documents":[{"id":"raw","title":"source","content":"retained material","url":"https://www.chinanews.com.cn/news/2026/10/30/item.shtml","source_type":"news","semantic_classification":"uncertain"}],"total_count":1,"coverage":{"admission_version":"semantic-v2","provider_candidates":1,"accepted_count":1,"direct_count":0,"context_count":0,"delivery_hint":"brief","failure":{"code":"model_unavailable"},"per_keyword":[],"per_platform":[],"candidate_audit":[]}}`))
	}))
	defer server.Close()
	var req CrawlReq
	if err := json.Unmarshal([]byte(`{"run_id":"new-run","analysis_id":"analysis","sources":["news"],"resilient":true,"delivery_policy":"best_available","seed_urls":["https://www.chinanews.com.cn/"]}`), &req); err != nil {
		t.Fatal(err)
	}
	result, err := NewRealCrawlerEngine(server.URL, "", nil).SearchWithCoverage(context.Background(), &req)
	if err != nil {
		t.Fatalf("uncertain brief materials discarded: %v", err)
	}
	if len(result.Documents) != 1 || result.Documents[0].SemanticClassification != "uncertain" || result.Coverage.DirectCount != 0 {
		t.Fatalf("raw material promoted or lost: %+v", result)
	}
	if got["resilient"] != true || got["delivery_policy"] != "best_available" {
		t.Fatalf("resilient request fields not forwarded: %+v", got)
	}
}
