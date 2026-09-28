package monitorplan

import (
	"strings"
	"testing"
)

func TestResolveAnalysisTypePreservesTemplateAndUserChoice(t *testing.T) {
	cases := []struct {
		templateID string
		version    int
		selected   string
		wantType   string
		wantSource string
	}{
		{"brand_daily", 1, "", "brand", "template_default"},
		{"product_launch", 1, "", "event", "template_default"},
		{"quality_complaint", 1, "", "brand", "template_default"},
		{"competitor_update", 1, "", "competitor", "template_default"},
		{"crisis", 1, "", "event", "template_default"},
		{"campaign_review", 1, "", "event", "template_default"},
		{"quality_complaint", 1, "event", "event", "user_override"},
		{"product_launch", 1, "brand", "brand", "user_override"},
		{"crisis", 1, "industry", "industry", "user_override"},
		{"", 0, "event", "event", "manual"},
		{"", 0, "brand", "brand", "manual"},
		{"", 0, "competitor", "competitor", "manual"},
		{"", 0, "industry", "industry", "manual"},
		{"", 0, "", "", "legacy_empty"},
	}
	for _, tc := range cases {
		t.Run(tc.templateID+"/"+tc.selected, func(t *testing.T) {
			got, err := ResolveAnalysisType(tc.templateID, tc.version, tc.selected)
			if err != nil {
				t.Fatal(err)
			}
			if got.TemplateID != tc.templateID || got.TemplateVersion != tc.version || got.AnalysisType != tc.wantType || got.Source != tc.wantSource || got.Explanation == "" {
				t.Errorf("resolve = %+v, want template %q, type %q, source %q with explanation", got, tc.templateID, tc.wantType, tc.wantSource)
			}
			if tc.templateID != "" && (!strings.Contains(got.Explanation, tc.templateID) || !strings.Contains(got.Explanation, "1")) {
				t.Errorf("explanation %q must identify template and version", got.Explanation)
			}
		})
	}
}

func TestResolveAnalysisTypeRejectsInvalidTemplateVersionOrType(t *testing.T) {
	for _, tc := range []struct {
		id      string
		version int
		chosen  string
	}{
		{"bogus", 1, ""}, {"crisis", 0, ""}, {"crisis", 2, ""},
		{"crisis", 1, "crisis"}, {"", 0, "bogus"}, {"", 1, "brand"},
	} {
		if _, err := ResolveAnalysisType(tc.id, tc.version, tc.chosen); err == nil {
			t.Errorf("resolve(%q,%d,%q) accepted", tc.id, tc.version, tc.chosen)
		}
	}
}
