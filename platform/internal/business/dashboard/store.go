// Package dashboard provides pre-aggregated analytics for the dashboard.
package dashboard

import "context"

// Store defines the dashboard data contract.
type Store interface {
	Overview(ctx context.Context, tenantID string, r RangeFilter) (*Overview, error)
	Trend(ctx context.Context, tenantID string, r RangeFilter) (*TrendSeries, error)
	SourceBreakdown(ctx context.Context, tenantID string, r RangeFilter) (*SourceBreakdown, error)
	TopTopics(ctx context.Context, tenantID string, r RangeFilter) ([]Topic, error)
}

// RangeFilter limits dashboard queries to a date range.
type RangeFilter struct {
	From string
	To   string
}

// Overview is the dashboard summary card data.
type Overview struct {
	TotalAnalyses  int     `json:"total_analyses"`
	TotalDocs      int     `json:"total_docs"`
	SentimentPos   int     `json:"sentiment_pos"`
	SentimentNeg   int     `json:"sentiment_neg"`
	SentimentNeu   int     `json:"sentiment_neu"`
	SuccessRate    float64 `json:"success_rate"`
	ActiveTasks    int     `json:"active_tasks"`
}

// TrendSeries is a time series of document counts and sentiment.
type TrendSeries struct {
	Dates  []string  `json:"dates"`
	Counts []int     `json:"counts"`
	Scores []float64 `json:"scores"` // avg sentiment per day
}

// SourceBreakdown shows document distribution by source type.
type SourceBreakdown struct {
	Sources []SourceShare `json:"sources"`
}

// SourceShare is one source's portion.
type SourceShare struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Pct   float64 `json:"pct"`
}

// Topic is a clustered topic from analysis.
type Topic struct {
	Name     string `json:"name"`
	DocCount int    `json:"doc_count"`
	Trend    string `json:"trend"`
}
