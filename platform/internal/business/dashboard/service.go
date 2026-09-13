// Package dashboard provides pre-aggregated analytics for the dashboard.
package dashboard

import (
	"context"
	"sort"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/business/report"
)

// fixedSources are the four source rows the MVP dashboard always shows.
// Simplified until the documents store can back real per-source counts.
var fixedSources = []SourceShare{
	{Name: "微博", Count: 40, Pct: 40},
	{Name: "公众号", Count: 30, Pct: 30},
	{Name: "新闻", Count: 20, Pct: 20},
	{Name: "小红书", Count: 10, Pct: 10},
}

// fixedTopics are the four predefined topic rows for the MVP dashboard.
var fixedTopics = []Topic{
	{Name: "后排空间", DocCount: 128, Trend: "rising"},
	{Name: "座椅舒适度", DocCount: 86, Trend: "rising"},
	{Name: "静谧性表现", DocCount: 54, Trend: "stable"},
	{Name: "价格与优惠", DocCount: 31, Trend: "falling"},
}

// Service aggregates dashboard data from the analysis and report services.
type Service struct {
	analysisSvc *analysis.Service
	reportSvc   *report.Service // reserved for document-level metrics
}

// NewService wires the dashboard to live services.
// reportSvc may be nil until report-backed metrics are implemented.
func NewService(analysisSvc *analysis.Service, reportSvc *report.Service) *Service {
	return &Service{analysisSvc: analysisSvc, reportSvc: reportSvc}
}

// Overview computes summary cards from the tenant's analyses:
// total runs, active runs and the completed success rate.
func (s *Service) Overview(ctx context.Context, tenantID string) (*Overview, error) {
	items, err := s.analysisSvc.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	total := len(items)
	active, completed := 0, 0
	for _, a := range items {
		if analysis.IsActive(string(a.State)) {
			active++
		}
		if a.State == analysis.StateCompleted {
			completed++
		}
	}

	successRate := 0.0
	if total > 0 {
		successRate = float64(completed) / float64(total) * 100
	}

	return &Overview{
		TotalAnalyses: total,
		ActiveTasks:   active,
		SuccessRate:   successRate,
	}, nil
}

// Trend returns daily analysis counts grouped by creation date. Scores is
// the average sentiment per day; document-level sentiment is not persisted
// yet, so it is emitted as a zero-aligned slice (never null) to keep the
// frontend chart contract intact.
func (s *Service) Trend(ctx context.Context, tenantID string) (*TrendSeries, error) {
	items, err := s.analysisSvc.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for _, a := range items {
		counts[a.CreatedAt.Format("2006-01-02")]++
	}

	dates := make([]string, 0, len(counts))
	for d := range counts {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	series := make([]int, 0, len(dates))
	scores := make([]float64, 0, len(dates))
	for _, d := range dates {
		series = append(series, counts[d])
		scores = append(scores, 0)
	}
	return &TrendSeries{Dates: dates, Counts: series, Scores: scores}, nil
}

// Sources returns the fixed four-source breakdown (MVP placeholder).
func (s *Service) Sources(_ context.Context, _ string) (*SourceBreakdown, error) {
	rows := make([]SourceShare, len(fixedSources))
	copy(rows, fixedSources)
	return &SourceBreakdown{Sources: rows}, nil
}

// Topics returns the four predefined topic rows (MVP placeholder).
func (s *Service) Topics(_ context.Context, _ string) ([]Topic, error) {
	topics := make([]Topic, len(fixedTopics))
	copy(topics, fixedTopics)
	return topics, nil
}
