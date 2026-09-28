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

func TestReportAdapterSendsSelectedTemplateToEngine(t *testing.T) {
	var got engine.ReportGenerateReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode report request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"report_id":"report-1","content":"ok"}`))
	}))
	defer server.Close()

	adapter := &engineReportAdapter{rep: engine.NewRealReportEngine(server.URL, "", nil)}
	if _, err := adapter.Generate(context.Background(), analysis.ReportRequest{Title: "report", TemplateID: "weekly"}); err != nil {
		t.Fatal(err)
	}
	if got.TemplateID != "weekly" {
		t.Fatalf("report template not sent to engine: %+v", got)
	}
}
