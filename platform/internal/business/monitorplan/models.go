package monitorplan

import (
	"encoding/json"
	"time"
)

// Template is immutable catalog metadata. It does not represent an enabled plan.
type Template struct {
	ID                  string `json:"template_id"`
	Version             int    `json:"template_version"`
	Name                string `json:"name"`
	DefaultAnalysisType string `json:"default_analysis_type"`
}

// AnalysisTypeResolution explains the provenance of the analysis perspective.
// Source is template_default, user_override, manual, or legacy_empty.
type AnalysisTypeResolution struct {
	TemplateID      string `json:"template_id,omitempty"`
	TemplateVersion int    `json:"template_version,omitempty"`
	AnalysisType    string `json:"analysis_type"`
	Source          string `json:"source"`
	Explanation     string `json:"explanation"`
}

// Candidate is a suggestion, never a claim that a setting is active.
type Candidate struct {
	State  string `json:"state"`
	Value  any    `json:"value,omitempty"`
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`
}

type PreviewRequest struct {
	TenantID        string            `json:"-"`
	PlanCode        string            `json:"-"`
	TemplateID      string            `json:"template_id"`
	TemplateVersion int               `json:"template_version"`
	AnalysisType    string            `json:"analysis_type,omitempty"`
	Inputs          map[string]string `json:"inputs"`
}

type Preview struct {
	AnalysisType AnalysisTypeResolution `json:"analysis_type_resolution"`
	Config       map[string]Candidate   `json:"config"`
	Warnings     []string               `json:"warnings"`
}

type Plan struct {
	ID              string               `json:"plan_id"`
	TenantID        string               `json:"tenant_id"`
	OwnerID         string               `json:"owner_id"`
	Name            string               `json:"name"`
	TemplateID      string               `json:"template_id"`
	TemplateVersion int                  `json:"template_version"`
	AnalysisType    string               `json:"analysis_type"`
	Inputs          map[string]string    `json:"inputs"`
	Config          map[string]Candidate `json:"config"`
	Revision        int                  `json:"revision"`
	State           string               `json:"state"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// ClonePlan prevents callers from mutating an in-memory draft's maps.
func ClonePlan(plan Plan) Plan {
	data, _ := json.Marshal(plan)
	var copied Plan
	_ = json.Unmarshal(data, &copied)
	return copied
}
