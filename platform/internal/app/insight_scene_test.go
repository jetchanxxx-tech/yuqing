package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/engine"
)

func TestInsightAdapterPreservesSceneAndVerifiedQuoteProvenance(t *testing.T) {
	var got engine.InsightAnalyzeReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode insight request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dimensions":[{"id":"heat","quotes":[{"document_id":"doc-1","text":"连续原文","source":"新闻"}]}]}`))
	}))
	defer server.Close()
	adapter := &engineInsightAdapter{ins: engine.NewRealInsightEngine(server.URL, "", nil)}
	result, err := adapter.Analyze(context.Background(), analysis.InsightRequest{
		AnalysisType: "event", ScenarioTemplateID: "quality_complaint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ScenarioTemplateID != "quality_complaint" || got.AnalysisType != "event" {
		t.Fatalf("scene/perspective lost at HTTP boundary: %+v", got)
	}
	if len(result.Dimensions) != 1 || len(result.Dimensions[0].Quotes) != 1 || result.Dimensions[0].Quotes[0].DocumentID != "doc-1" {
		t.Fatalf("quote provenance lost: %+v", result)
	}
	back := toEngineDimensions(result.Dimensions)
	if back[0].Quotes[0].DocumentID != "doc-1" {
		t.Fatal("report conversion lost quote provenance")
	}
}
