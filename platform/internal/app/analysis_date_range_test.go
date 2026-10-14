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

// These exercise the real app adapters and engine HTTP transports, rather than
// inferring that the selected calendar bounds survive from the fetch contract.
func TestAnalysisDateRangeReachesInsightAndReportTransports(t *testing.T) {
	for _, endpoint := range []string{"/analyze", "/generate"} {
		t.Run(endpoint, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != endpoint || r.Method != http.MethodPost {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode engine request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				if endpoint == "/analyze" {
					_, _ = w.Write([]byte(`{"sentiments":[],"topics":[],"summary":"date scope","dimensions":[]}`))
				} else {
					_, _ = w.Write([]byte(`{"report_id":"date-report","file_key":"reports/date.html","format":"html","content":"<p>date scope</p>"}`))
				}
			}))
			defer server.Close()

			if endpoint == "/analyze" {
				var request analysis.InsightRequest
				if err := json.Unmarshal([]byte(`{"Title":"window","DateFrom":"2026-09-10","DateTo":"2026-09-20"}`), &request); err != nil {
					t.Fatal(err)
				}
				adapter := engineInsightAdapter{ins: engine.NewRealInsightEngine(server.URL, "", nil)}
				if _, err := adapter.Analyze(context.Background(), request); err != nil {
					t.Fatal(err)
				}
			} else {
				var request analysis.ReportRequest
				if err := json.Unmarshal([]byte(`{"Title":"window","DateFrom":"2026-09-10","DateTo":"2026-09-20"}`), &request); err != nil {
					t.Fatal(err)
				}
				adapter := engineReportAdapter{rep: engine.NewRealReportEngine(server.URL, "", nil)}
				if _, err := adapter.Generate(context.Background(), request); err != nil {
					t.Fatal(err)
				}
			}
			body := <-requests
			if body["date_from"] != "2026-09-10" || body["date_to"] != "2026-09-20" {
				t.Fatalf("selected window not forwarded to %s: date_from=%v date_to=%v", endpoint, body["date_from"], body["date_to"])
			}
		})
	}
}
