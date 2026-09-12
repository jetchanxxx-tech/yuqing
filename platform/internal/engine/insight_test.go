package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRealInsightEngineAnalyze 覆盖 HTTP transport：请求体含文档与 api_key，
// 响应解析为 InsightAnalyzeResp。
func TestRealInsightEngineAnalyze(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analyze" {
			t.Errorf("path = %s, want /analyze", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"sentiments": [{"document_id": "d1", "sentiment": "negative", "level": "非常负面", "confidence": 0.92, "score": 0.8}],
			"topics": [{"id": "t1", "name": "后排空间", "keywords": ["后排"], "doc_count": 1, "trend": "rising"}],
			"summary": "舆情总体可控"
		}`))
	}))
	defer srv.Close()

	e := NewRealInsightEngine(srv.URL, "", func() string { return "sk-test" })
	resp, err := e.Analyze(context.Background(), &InsightAnalyzeReq{
		DocumentIDs:  []string{"d1"},
		Documents:    []Document{{ID: "d1", Title: "标题", Content: "内容", SourceType: "news"}},
		AnalysisID:   "a1",
		AnalysisType: "brand",
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	if len(resp.Sentiments) != 1 || resp.Sentiments[0].Sentiment != "negative" || resp.Sentiments[0].Score != 0.8 {
		t.Errorf("sentiments = %+v, want 1 negative 0.8", resp.Sentiments)
	}
	if resp.Sentiments[0].Level != "非常负面" || resp.Sentiments[0].Confidence != 0.92 {
		t.Errorf("sentiment level/confidence = %+v, want 非常负面/0.92", resp.Sentiments[0])
	}
	if len(resp.Topics) != 1 || resp.Topics[0].Name != "后排空间" || resp.Topics[0].DocCount != 1 {
		t.Errorf("topics = %+v", resp.Topics)
	}
	if resp.Summary != "舆情总体可控" {
		t.Errorf("summary = %q", resp.Summary)
	}

	// 请求体校验：文档内容与 api_key 必须透传
	if got["analysis_id"] != "a1" {
		t.Errorf("request analysis_id = %v", got["analysis_id"])
	}
	if got["api_key"] != "sk-test" {
		t.Errorf("request api_key = %v, want sk-test", got["api_key"])
	}
	docs, ok := got["documents"].([]any)
	if !ok || len(docs) != 1 {
		t.Fatalf("request documents = %v, want 1 item", got["documents"])
	}
	if docs[0].(map[string]any)["title"] != "标题" {
		t.Errorf("request doc title = %v", docs[0])
	}
}

// 引擎错误状态（如 LLM key 未配置 503）必须转为 error 而非静默空结果。
func TestRealInsightEngineAnalyzeEngineError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail": "DEEPSEEK_API_KEY 未配置"}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	e := NewRealInsightEngine(srv.URL, "", nil)
	_, err := e.Analyze(context.Background(), &InsightAnalyzeReq{
		Documents: []Document{{ID: "d1"}},
	})
	if err == nil {
		t.Fatal("want error for 503 response, got nil")
	}
}

// Sentiment 批量接口同样走 transport。
func TestRealInsightEngineSentiment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sentiment" {
			t.Errorf("path = %s, want /sentiment", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results": [{"document_id": "d1", "sentiment": "positive", "score": 0.9}]}`))
	}))
	defer srv.Close()

	e := NewRealInsightEngine(srv.URL, "", nil)
	resp, err := e.Sentiment(context.Background(), &SentimentReq{
		Documents: []Document{{ID: "d1"}},
	})
	if err != nil {
		t.Fatalf("Sentiment failed: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Sentiment != "positive" {
		t.Errorf("results = %+v", resp.Results)
	}
}
