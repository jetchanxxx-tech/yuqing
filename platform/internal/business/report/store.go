// Package report manages report lifecycle and format exports.
package report

import "context"

// Store defines the report persistence contract.
type Store interface {
	Create(ctx context.Context, tenantID string, r Report) error
	Get(ctx context.Context, tenantID, reportID string) (*Report, error)
	List(ctx context.Context, tenantID string, f Filter) ([]Report, error)
	UpdateStatus(ctx context.Context, tenantID, reportID, status string) error
}

// Report represents a generated analysis report.
type Report struct {
	ID          string `json:"id"`
	AnalysisID  string `json:"analysis_id"`
	Title       string `json:"title"`
	Format      string `json:"format"`
	Status      string `json:"status"`
	FileKey     string `json:"file_key,omitempty"`
	SummaryJSON string `json:"summary_json,omitempty"`
}

// Filter narrows report queries.
type Filter struct {
	AnalysisID string
	Status     string
	Limit      int
	Offset     int
}

// Supported export formats.
var Formats = []string{"html", "markdown", "pdf", "docx"}
