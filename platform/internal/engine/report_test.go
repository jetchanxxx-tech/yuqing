package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRealReportEngineGenerate 覆盖报告生成的 HTTP transport。
func TestRealReportEngineGenerate(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/generate" {
			t.Errorf("path = %s, want /generate", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"report_id": "rep-1", "file_key": "reports/rep-1.html", "format": "html", "content": "<html>报告</html>"}`))
	}))
	defer srv.Close()

	e := NewRealReportEngine(srv.URL, "", func() string { return "sk-test" })
	resp, err := e.Generate(context.Background(), &ReportGenerateReq{
		Title:      "雅阁舆情报告",
		Format:     "html",
		Documents:  []Document{{ID: "d1", Title: "文档1"}},
		Sentiments: []SentimentResult{{DocumentID: "d1", Sentiment: "negative", Score: 0.8}},
		Topics:     []TopicResult{{ID: "t1", Name: "后排空间", DocCount: 1}},
		AnalysisID: "a1",
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if resp.ReportID != "rep-1" {
		t.Errorf("report_id = %q", resp.ReportID)
	}
	if resp.Content != "<html>报告</html>" {
		t.Errorf("content = %q", resp.Content)
	}
	if got["api_key"] != "sk-test" {
		t.Errorf("request api_key = %v, want sk-test", got["api_key"])
	}
	if got["title"] != "雅阁舆情报告" {
		t.Errorf("request title = %v", got["title"])
	}
}

// 报告引擎错误必须转为 error（管线据此降级）。
func TestRealReportEngineGenerateEngineError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	e := NewRealReportEngine(srv.URL, "", nil)
	if _, err := e.Generate(context.Background(), &ReportGenerateReq{Title: "t"}); err == nil {
		t.Fatal("want error for 502 response, got nil")
	}
}
