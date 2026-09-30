package monitorplan

import "testing"

func TestCatalogHasSixStableVersionedTemplates(t *testing.T) {
	want := []struct{ id, name, analysisType string }{
		{"brand_daily", "品牌日常口碑", "brand"},
		{"product_launch", "新品上市", "event"},
		{"quality_complaint", "产品质量投诉", "brand"},
		{"competitor_update", "竞品动态", "competitor"},
		{"crisis", "突发危机", "event"},
		{"campaign_review", "营销活动复盘", "event"},
	}
	got := ListTemplates()
	if len(got) != len(want) {
		t.Fatalf("templates = %d, want %d", len(got), len(want))
	}
	for i, entry := range want {
		if got[i].ID != entry.id || got[i].Name != entry.name || got[i].DefaultAnalysisType != entry.analysisType || got[i].Version != 1 {
			t.Errorf("template[%d] = %+v, want %+v at version 1", i, got[i], entry)
		}
		found, ok := LookupTemplate(entry.id, 1)
		if !ok || found != got[i] {
			t.Errorf("lookup(%q,1) = %+v, %v", entry.id, found, ok)
		}
	}
	for _, key := range []struct {
		id      string
		version int
	}{
		{"unknown", 1}, {"brand_daily", 0}, {"brand_daily", 2},
	} {
		if _, ok := LookupTemplate(key.id, key.version); ok {
			t.Errorf("lookup(%q,%d) unexpectedly succeeded", key.id, key.version)
		}
	}
}

func TestValidateAnalysisType(t *testing.T) {
	for _, value := range []string{"event", "brand", "competitor", "industry"} {
		if err := ValidateAnalysisType(value); err != nil {
			t.Errorf("valid type %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "crisis", "Brand", "unknown"} {
		if err := ValidateAnalysisType(value); err == nil {
			t.Errorf("invalid type %q accepted", value)
		}
	}
}
