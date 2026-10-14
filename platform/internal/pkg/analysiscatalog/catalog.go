// Package analysiscatalog owns the built-in scenes shared by analysis and plans.
package analysiscatalog

const Version = 1

type Scene struct {
	ID, Name, DefaultAnalysisType string
}

var scenes = [...]Scene{
	{"brand_daily", "品牌日常口碑", "brand"},
	{"product_launch", "新品上市", "event"},
	{"quality_complaint", "产品质量投诉", "brand"},
	{"competitor_update", "竞品动态", "competitor"},
	{"crisis", "突发危机", "event"},
	{"campaign_review", "营销活动复盘", "event"},
}

func Scenes() []Scene { return append([]Scene{}, scenes[:]...) }

func Lookup(id string) (Scene, bool) {
	for _, scene := range scenes {
		if scene.ID == id {
			return scene, true
		}
	}
	return Scene{}, false
}

func IsAnalysisType(value string) bool {
	switch value {
	case "event", "brand", "competitor", "industry":
		return true
	default:
		return false
	}
}
