package analysis

import (
	"context"
	"strings"
)

// Sentiment is one document's sentiment classification result.
type Sentiment struct {
	DocumentID string  `json:"document_id"`
	Sentiment  string  `json:"sentiment"` // positive, negative, neutral
	Level      string  `json:"level,omitempty"`     // 非常正面|正面|中性|负面|非常负面（5 级）
	Confidence float64 `json:"confidence,omitempty"` // 0-1 置信度
	Score      float64 `json:"score"`
}

// Topic is one clustered topic across documents.
type Topic struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Keywords []string `json:"keywords,omitempty"`
	DocCount int      `json:"doc_count"`
	Trend    string   `json:"trend,omitempty"` // rising, stable, falling
}

// InsightResult is the analysis engine output: summary + sentiments + topics.
type InsightResult struct {
	Summary    string
	Sentiments []Sentiment
	Topics     []Topic
}

// InsightRequest carries the documents to analyze.
type InsightRequest struct {
	TenantID     string
	AnalysisID   string
	AnalysisType string
	Documents    []Document
}

// InsightAnalyzer performs sentiment/topic/summary analysis.
// 生产实现经 HTTP 调用 Python insight 引擎（app 层适配）；测试注入 fake。
type InsightAnalyzer interface {
	Analyze(ctx context.Context, req InsightRequest) (InsightResult, error)
}

// ReportRequest carries the data to render a report.
type ReportRequest struct {
	TenantID   string
	AnalysisID string
	Title      string
	Documents  []Document
	Sentiments []Sentiment
	Topics     []Topic
	// InsightAvailable = 洞察引擎是否成功产出。false 时报告引擎不得把
	// 空情感数据渲染成 0/0/0（与「全部中性」无法区分，属误导）。
	InsightAvailable bool
}

// ReportResult is the generated report content.
type ReportResult struct {
	ReportID string
	Content  string
}

// ReportGenerator renders an analysis report.
// 生产实现经 HTTP 调用 Python report 引擎（app 层适配）；测试注入 fake。
type ReportGenerator interface {
	Generate(ctx context.Context, req ReportRequest) (ReportResult, error)
}

// SetInsight 写入洞察结果（摘要/情感/话题），管线 analyzing 步骤调用。
func (s *Service) SetInsight(ctx context.Context, tenantID, analysisID string, r InsightResult) error {
	return s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		a.Summary = r.Summary
		a.Sentiments = r.Sentiments
		a.Topics = r.Topics
		return nil
	})
}

// SetReport 写入生成的报告内容，管线 generating_report 步骤调用。
func (s *Service) SetReport(ctx context.Context, tenantID, analysisID, reportID, content string) error {
	return s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		a.ReportID = reportID
		a.ReportContent = content
		return nil
	})
}

// SetWarning 记录非致命降级原因（如引擎未配置/调用失败）。
// 追加语义：多条原因以「；」连接，相同原因不重复 —— 管线各步骤的
// 降级原因都不应被后写的覆盖。
func (s *Service) SetWarning(ctx context.Context, tenantID, analysisID, warning string) error {
	return s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		if warning == "" {
			return nil
		}
		if a.Warning == "" {
			a.Warning = warning
		} else if !strings.Contains(a.Warning, warning) {
			a.Warning = a.Warning + "；" + warning
		}
		return nil
	})
}
