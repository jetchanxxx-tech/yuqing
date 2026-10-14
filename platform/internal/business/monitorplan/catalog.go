package monitorplan

import (
	"fmt"
	"github.com/yuqing/platform/internal/pkg/analysiscatalog"
)

const CatalogVersion = analysiscatalog.Version

// ListTemplates returns a copy of the current versioned catalog in stable order.
func ListTemplates() []Template {
	out := make([]Template, 0, len(analysiscatalog.Scenes()))
	for _, scene := range analysiscatalog.Scenes() {
		out = append(out, Template{ID: scene.ID, Version: CatalogVersion, Name: scene.Name, DefaultAnalysisType: scene.DefaultAnalysisType})
	}
	return out
}

// LookupTemplate rejects versions that are not present in this catalog.
func LookupTemplate(id string, version int) (Template, bool) {
	if version != CatalogVersion {
		return Template{}, false
	}
	scene, ok := analysiscatalog.Lookup(id)
	if !ok {
		return Template{}, false
	}
	return Template{ID: scene.ID, Version: CatalogVersion, Name: scene.Name, DefaultAnalysisType: scene.DefaultAnalysisType}, true
}

// ValidateAnalysisType accepts only the four existing analysis perspectives.
// Legacy empty values are handled separately by ResolveAnalysisType.
func ValidateAnalysisType(value string) error {
	if analysiscatalog.IsAnalysisType(value) {
		return nil
	}
	return fmt.Errorf("monitorplan: invalid analysis_type %q", value)
}
