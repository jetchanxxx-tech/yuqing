// Package dashboard provides pre-aggregated analytics for the dashboard.
package dashboard

import (
	"context"
	"sort"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/business/report"
)

// Sources returns real per-source document counts aggregated from all completed analyses.
// Falls back to zero-value breakdown when no documents are available.
func (s *Service) Sources(ctx context.Context, tenantID string) (*SourceBreakdown, error) {
	docs := s.analysisSvc.AllDocuments(ctx, tenantID)
	if len(docs) == 0 {
		return &SourceBreakdown{Sources: []SourceShare{}}, nil
	}

	counts := make(map[string]int)
	for _, d := range docs {
		if d.SourceType != "" {
			counts[d.SourceType]++
		}
	}

	total := 0
	for _, c := range counts {
		total += c
	}
	var rows []SourceShare
	for src, cnt := range counts {
		var pct float64
		if total > 0 {
			pct = float64(cnt) / float64(total) * 100
		}
		rows = append(rows, SourceShare{
			Name:  displayName(src),
			Count: cnt,
			Pct:   pct,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
	return &SourceBreakdown{Sources: rows}, nil
}

func displayName(src string) string {
	switch src {
	case "weibo":
		return "微博"
	case "weixin":
		return "公众号"
	case "news":
		return "新闻"
	case "xiaohongshu":
		return "小红书"
	case "bilibili":
		return "B站"
	case "douyin":
		return "抖音"
	case "zhihu":
		return "知乎"
	case "kuaishou":
		return "快手"
	default:
		return "其他"
	}
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
	reportSvc  *report.Service
}

// NewService wires the dashboard to live services.
func NewService(analysisSvc *analysis.Service, reportSvc *report.Service) *Service {
	return &Service{analysisSvc: analysisSvc, reportSvc: reportSvc}
}

// Overview computes summary cards from the tenant's analyses.
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

// Trend returns daily analysis counts grouped by creation date.
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

// Topics returns aggregated topic statistics from all completed analyses.
func (s *Service) Topics(ctx context.Context, tenantID string) ([]Topic, error) {
	items, err := s.analysisSvc.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Aggregate topics across all completed analyses
	topicCounts := make(map[string]int)
	for _, a := range items {
		if a.State != analysis.StateCompleted {
			continue
		}
		for _, t := range a.Topics {
			topicCounts[t.Name] += t.DocCount
		}
	}

	// Convert to slice and sort by doc count (descending)
	var topics []Topic
	for name, count := range topicCounts {
		topics = append(topics, Topic{
			Name:     name,
			DocCount: count,
			Trend:    "stable", // Default trend; real trend calculation would require historical data
		})
	}

	sort.Slice(topics, func(i, j int) bool {
		return topics[i].DocCount > topics[j].DocCount
	})

	// Return top 4 topics (or fewer if less available)
	if len(topics) > 4 {
		topics = topics[:4]
	}

	return topics, nil
}
