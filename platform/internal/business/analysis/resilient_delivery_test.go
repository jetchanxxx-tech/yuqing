package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func deliveryWire(t *testing.T, a *AnalysisResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	return wire
}

// The absent flag remains covered by the existing strict pipeline contracts.
// These cases fail if the new rollout still discards usable material or has no
// independently rendered receipt when Python cannot generate a report.
func TestResilientDeliveryAlwaysPreservesAnHonestArtifact(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	for _, tc := range []struct {
		name, want string
		docs       []Document
		err        error
	}{
		{"unavailable supplier", "empty", nil, errors.New("supplier unavailable")},
		{"materials without inference", "brief", []Document{{ID: "raw", Title: "<script>unsafe</script>", Content: "retained source", SourceType: "news"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, svc := newTestPipeline(t, &fakeFetcher{docs: tc.docs, err: tc.err}, time.Minute)
			a, err := svc.Create(context.Background(), CreateAnalysisRequest{TenantID: "resilient", Name: tc.name, Sources: []string{"news"}})
			if err != nil {
				t.Fatal(err)
			}
			if err = p.Handle(context.Background(), TaskMessage{TenantID: "resilient", AnalysisID: a.ID}); err != nil {
				t.Fatalf("best available delivery: %v", err)
			}
			got, err := svc.Get(context.Background(), "resilient", a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StateCompleted || deliveryWire(t, got)["delivery_level"] != tc.want {
				t.Fatalf("delivery state/level = %s/%v, want completed/%s", got.State, deliveryWire(t, got)["delivery_level"], tc.want)
			}
			if got.ReportContent == "" || strings.Contains(got.ReportContent, "<script>unsafe</script>") {
				t.Fatalf("missing or unsafe deterministic artifact: %q", got.ReportContent)
			}
			if len(deliveryWire(t, got)["delivery_reasons"].([]any)) == 0 {
				t.Fatal("degradation has no saved reason")
			}
		})
	}
}

func TestResilientFullOnlyPreservesBriefButDoesNotComplete(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	p, svc := newTestPipeline(t, &fakeFetcher{docs: []Document{{ID: "raw", Content: "source", SourceType: "news"}}}, time.Minute)
	var req CreateAnalysisRequest
	if err := json.Unmarshal([]byte(`{"tenant_id":"strict","name":"strict","sources":["news"],"delivery_policy":"full_only"}`), &req); err != nil {
		t.Fatal(err)
	}
	a, err := svc.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Handle(context.Background(), TaskMessage{TenantID: "strict", AnalysisID: a.ID})
	a, err = svc.Get(context.Background(), "strict", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateFailed || a.ReportContent == "" || deliveryWire(t, a)["delivery_level"] != "brief" {
		t.Fatalf("full_only lost retained diagnostic artifact: %+v", a)
	}
}

func TestResilientCreateRejectsUnsafeOrUnselectedSeedURLs(t *testing.T) {
	for _, seed := range []string{"http://127.0.0.1/admin", "http://169.254.169.254/latest", "https://user:secret@example.com/", "https://weibo.com/item"} {
		_, svc := newTestPipeline(t, &fakeFetcher{}, time.Minute)
		raw, _ := json.Marshal(map[string]any{"tenant_id": "seeds", "name": "safe", "sources": []string{"news"}, "seed_urls": []string{seed}})
		var req CreateAnalysisRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create(context.Background(), req); err == nil {
			t.Errorf("unsafe/unselected seed accepted: %s", seed)
		}
	}
}
