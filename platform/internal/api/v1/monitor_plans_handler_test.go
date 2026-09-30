package v1

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/business/monitorplan"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

func TestValidateMonitorConfigKeywordsAndSources(t *testing.T) {
	preview := map[string]monitorplan.Candidate{
		"keywords": {State: "proposed", Value: []string{"brand"}, Source: "input:brand_name"},
		"sources":  {State: "proposed", Value: []string{"news"}, Source: "runtime:authorized_source_and_plan_feature"},
	}
	tests := []struct {
		name     string
		keywords monitorplan.Candidate
		sources  monitorplan.Candidate
		base     monitorplan.Candidate
		wantErr  bool
	}{
		{"edited keywords", monitorplan.Candidate{State: "proposed", Value: []string{"new brand"}, Source: "user_edit"}, preview["sources"], preview["sources"], false},
		{"JSON keywords", monitorplan.Candidate{State: "proposed", Value: []any{"new brand"}, Source: "user_edit"}, preview["sources"], preview["sources"], false},
		{"empty keywords", monitorplan.Candidate{State: "proposed", Value: []string{}, Source: "user_edit"}, preview["sources"], preview["sources"], true},
		{"blank keywords", monitorplan.Candidate{State: "proposed", Value: []any{"  "}, Source: "user_edit"}, preview["sources"], preview["sources"], true},
		{"unverified source", preview["keywords"], monitorplan.Candidate{State: "proposed", Value: []any{"news", "forum"}, Source: preview["sources"].Source}, preview["sources"], true},
		{"forged source provenance", preview["keywords"], monitorplan.Candidate{State: "proposed", Value: []string{"news"}, Source: "runtime:verified"}, preview["sources"], true},
		{"unavailable proposed", preview["keywords"], monitorplan.Candidate{State: "proposed", Value: []string{"news"}, Source: "user_edit"}, monitorplan.Candidate{State: "unavailable", Source: "runtime:news", Reason: "not verified"}, true},
		{"unavailable carrying sources", preview["keywords"], monitorplan.Candidate{State: "unavailable", Value: []string{"forum"}, Source: "runtime:news", Reason: "not verified"}, monitorplan.Candidate{State: "unavailable", Source: "runtime:news", Reason: "not verified"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := map[string]monitorplan.Candidate{"keywords": test.keywords, "sources": test.sources}
			base := map[string]monitorplan.Candidate{"keywords": preview["keywords"], "sources": test.base}
			err := validateMonitorConfig(config, base)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateMonitorConfig() = %v, want error %v", err, test.wantErr)
			}
		})
	}
}

func TestMonitorPlanErrorRetainsStructuredDetails(t *testing.T) {
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Set(string(middleware.CtxRequestID), "rid")
	err := pkgerrors.WithDetails(pkgerrors.Wrap(pkgerrors.ErrConflict, "stale draft"), map[string]int{"revision": 2})
	monitorRespondError(ctx, err)
	var body map[string]any
	if jsonErr := json.Unmarshal(response.Body.Bytes(), &body); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if response.Code != 409 || body["code"] != "CONFLICT" || body["request_id"] != "rid" || body["details"].(map[string]any)["revision"] != float64(2) {
		t.Fatalf("envelope: %d %s", response.Code, response.Body.String())
	}
}
