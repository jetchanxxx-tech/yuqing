package monitorplan

import "fmt"

const CatalogVersion = 1

var templates = [...]Template{
	{ID: "brand_daily", Version: CatalogVersion, Name: "品牌日常口碑", DefaultAnalysisType: "brand"},
	{ID: "product_launch", Version: CatalogVersion, Name: "新品上市", DefaultAnalysisType: "event"},
	{ID: "quality_complaint", Version: CatalogVersion, Name: "产品质量投诉", DefaultAnalysisType: "brand"},
	{ID: "competitor_update", Version: CatalogVersion, Name: "竞品动态", DefaultAnalysisType: "competitor"},
	{ID: "crisis", Version: CatalogVersion, Name: "突发危机", DefaultAnalysisType: "event"},
	{ID: "campaign_review", Version: CatalogVersion, Name: "营销活动复盘", DefaultAnalysisType: "event"},
}

// ListTemplates returns a copy of the current versioned catalog in stable order.
func ListTemplates() []Template {
	return append([]Template{}, templates[:]...)
}

// LookupTemplate rejects versions that are not present in this catalog.
func LookupTemplate(id string, version int) (Template, bool) {
	for _, template := range templates {
		if template.ID == id && template.Version == version {
			return template, true
		}
	}
	return Template{}, false
}

// ValidateAnalysisType accepts only the four existing analysis perspectives.
// Legacy empty values are handled separately by ResolveAnalysisType.
func ValidateAnalysisType(value string) error {
	switch value {
	case "event", "brand", "competitor", "industry":
		return nil
	default:
		return fmt.Errorf("monitorplan: invalid analysis_type %q", value)
	}
}
