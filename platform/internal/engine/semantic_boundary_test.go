package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func semanticSearchFixture() map[string]any {
	doc := map[string]any{"id": "d1", "title": "iPhone18 Pro", "content": "iPhone18 Pro battery review", "url": "https://www.toutiao.com/article/1", "final_url": "https://www.toutiao.com/article/1", "source_type": "toutiao", "content_kind": "body", "semantic_classification": "direct", "semantic_reason": "related model", "evidence_spans": []string{"iPhone18 Pro"}, "observed_spans": []map[string]int{{"start": 0, "end": len([]rune("iPhone18 Pro battery review"))}}}
	return map[string]any{"documents": []any{doc}, "total_count": 1, "coverage": map[string]any{"admission_version": "semantic-v2", "accepted_count": 1, "direct_count": 1, "context_count": 0, "provider_candidates": 1, "per_keyword": []any{}, "per_platform": []any{}, "candidate_audit": []any{doc}, "research_intent": map[string]any{"version": "semantic-v2", "topic": "iPhone18 family"}}}
}

func TestSemanticTransportRejectsUnboundEvidence(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"lexical for accepted new run": func(v map[string]any) { v["coverage"].(map[string]any)["admission_version"] = "lexical-v1" },
		"inflated direct count":        func(v map[string]any) { v["coverage"].(map[string]any)["direct_count"] = 2 },
		"invalid classification": func(v map[string]any) {
			v["documents"].([]any)[0].(map[string]any)["semantic_classification"] = "irrelevant"
		},
		"invented quote": func(v map[string]any) {
			v["documents"].([]any)[0].(map[string]any)["evidence_spans"] = []string{"an invented quote"}
		},
		"empty quote": func(v map[string]any) { v["documents"].([]any)[0].(map[string]any)["evidence_spans"] = []string{} },
		"mislabelled platform": func(v map[string]any) {
			v["documents"].([]any)[0].(map[string]any)["url"] = "https://weibo.com/1"
			v["documents"].([]any)[0].(map[string]any)["final_url"] = "https://weibo.com/1"
		},
		"unknown audit ID": func(v map[string]any) {
			v["coverage"].(map[string]any)["candidate_audit"] = []any{map[string]any{"id": "unknown", "semantic_classification": "direct"}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			body := semanticSearchFixture()
			mutate(body)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(body) }))
			defer server.Close()
			_, err := NewRealCrawlerEngine(server.URL, "", nil).SearchWithCoverage(context.Background(), &CrawlReq{RunID: "accepted-run", Sources: []string{"toutiao"}})
			if err == nil {
				t.Fatal("unbound semantic evidence reached the platform")
			}
		})
	}
}

func TestSemanticTransportPreservesFailureAuditAndUsesFullStageBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{"code": "semantic_admission_failed", "coverage": semanticSearchFixture()["coverage"]}})
	}))
	defer server.Close()
	crawler := NewRealCrawlerEngine(server.URL, "", nil)
	if crawler.httpClient.Timeout < 960*time.Second {
		t.Errorf("query transport timeout=%v, must cover the 900s stage", crawler.httpClient.Timeout)
	}
	result, err := crawler.SearchWithCoverage(context.Background(), &CrawlReq{RunID: "accepted-run"})
	if err == nil || result.Coverage.AdmissionVersion != "semantic-v2" || result.Coverage.ProviderCandidates != 1 {
		t.Fatalf("failed acquisition lost audit: %+v / %v", result, err)
	}
}

func TestSemanticTransportAcceptsBoundEvidenceAndForwardsResearchContext(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		response := semanticSearchFixture()
		response["coverage"].(map[string]any)["out_of_range_date_count"] = 4
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	crawler := NewRealCrawlerEngine(server.URL, "", nil).WithLLMOpts(func() (string, string) { return "https://provider.example.invalid/v1", "sandbox-model" }).WithLLMKey(func() string { return "isolated-model-key" })
	result, err := crawler.SearchWithCoverage(context.Background(), &CrawlReq{RunID: "accepted-run", TenantID: "team", AnalysisID: "analysis", Title: "新品研究", AnalysisType: "brand", ScenarioTemplateID: "product_launch", ResearchQuestion: "关注续航与价格", Sources: []string{"toutiao"}, Keywords: []string{"iphone18", "iphone18pro", "iphone18max"}})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"run_id": "accepted-run", "tenant_id": "team", "title": "新品研究", "analysis_type": "brand", "scenario_template_id": "product_launch", "research_question": "关注续航与价格", "llm_model": "sandbox-model", "llm_api_key": "isolated-model-key"} {
		if request[key] != want {
			t.Errorf("research context %s=%v", key, request[key])
		}
	}
	encoded, err := json.Marshal(result.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip map[string]any
	_ = json.Unmarshal(encoded, &roundtrip)
	if roundtrip["out_of_range_date_count"] != float64(4) || len(result.Documents[0].ObservedSpans) != 1 {
		t.Fatalf("provenance lost at transport: %s", encoded)
	}
}
