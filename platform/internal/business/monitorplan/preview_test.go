package monitorplan

import (
	"context"
	"strings"
	"testing"
)

func TestPreviewSixTemplatesSevenCandidatesAndNoEnabledStates(t *testing.T) {
	knownPlan := func(code string) bool { return code == "pro" }
	inputs := map[string]string{"brand_name": "品牌", "product_name": "新品", "competitor_name": "对手", "scenario": "活动"}
	for _, template := range ListTemplates() {
		t.Run(template.ID, func(t *testing.T) {
			got, err := NewService(nil, nil, knownPlan).Preview(context.Background(), PreviewRequest{TenantID: "tenant", PlanCode: "pro", TemplateID: template.ID, TemplateVersion: template.Version, Inputs: inputs})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Config) != 7 || got.AnalysisType.AnalysisType != template.DefaultAnalysisType {
				t.Fatalf("preview = %+v", got)
			}
			for _, key := range []string{"keywords", "exclude_words", "sources", "monitoring_cycle", "risk_tags", "alert_rules", "report_template"} {
				item, ok := got.Config[key]
				if !ok || item.Source == "" || item.State != "proposed" && item.State != "unavailable" {
					t.Errorf("%s: %+v", key, item)
				}
			}
			if got.Config["sources"].State != "unavailable" || got.Config["monitoring_cycle"].State != "unavailable" || got.Config["alert_rules"].State != "unavailable" || got.Config["report_template"].State != "unavailable" {
				t.Errorf("unwired config presented as available: %+v", got.Config)
			}
		})
	}
}

func TestPreviewFailClosedAndPreservesPerspective(t *testing.T) {
	svc := NewService(nil, func(_ context.Context, _, _, _ string) (SourceCapability, error) {
		return SourceCapability{Available: true, Authorized: true, FeatureEnabled: false}, nil
	}, func(code string) bool { return code == "pro" })
	base := PreviewRequest{TenantID: "tenant", PlanCode: "pro", TemplateID: "quality_complaint", TemplateVersion: 1, AnalysisType: "event", Inputs: map[string]string{"product_name": "耳机"}}
	got, err := svc.Preview(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if got.AnalysisType.AnalysisType != "event" || got.AnalysisType.Source != "user_override" || got.Config["sources"].State != "unavailable" {
		t.Fatalf("preview = %+v", got)
	}
	base.Inputs = nil
	if _, err := svc.Preview(context.Background(), base); err == nil {
		t.Fatal("missing required input accepted")
	}
	base.Inputs = map[string]string{"product_name": "耳机"}
	base.PlanCode = "unknown"
	if _, err := svc.Preview(context.Background(), base); err == nil {
		t.Fatal("unknown plan accepted")
	}
	base.PlanCode = "pro"
	base.TemplateVersion = 2
	if _, err := svc.Preview(context.Background(), base); err == nil {
		t.Fatal("unknown template version accepted")
	}
}

func TestPreviewSourceRequiresAllThreeRuntimeGates(t *testing.T) {
	for _, capability := range []SourceCapability{
		{Available: true, Authorized: true}, {Available: true, FeatureEnabled: true}, {Authorized: true, FeatureEnabled: true},
	} {
		svc := NewService(nil, func(_ context.Context, _, _, _ string) (SourceCapability, error) { return capability, nil }, func(string) bool { return true })
		got, err := svc.Preview(context.Background(), PreviewRequest{TenantID: "tenant", PlanCode: "pro", TemplateID: "brand_daily", TemplateVersion: 1, Inputs: map[string]string{"brand_name": "品牌"}})
		if err != nil || got.Config["sources"].State != "unavailable" {
			t.Errorf("capability=%+v: preview=%+v, %v", capability, got, err)
		}
	}
	svc := NewService(nil, func(_ context.Context, _, _, _ string) (SourceCapability, error) {
		return SourceCapability{Available: true, Authorized: true, FeatureEnabled: true}, nil
	}, func(string) bool { return true })
	got, err := svc.Preview(context.Background(), PreviewRequest{TenantID: "tenant", PlanCode: "pro", TemplateID: "brand_daily", TemplateVersion: 1, Inputs: map[string]string{"brand_name": "品牌"}})
	if err != nil || got.Config["sources"].State != "proposed" {
		t.Errorf("authorized source not proposed: %+v, %v", got, err)
	}
}

func TestPreviewRejectsMissingTemplateClearly(t *testing.T) {
	svc := NewService(nil, nil, func(code string) bool { return code == "pro" })
	_, err := svc.Preview(context.Background(), PreviewRequest{TenantID: "tenant", PlanCode: "pro", Inputs: map[string]string{"brand_name": "品牌"}})
	if err == nil || strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("missing template error = %v", err)
	}
}
