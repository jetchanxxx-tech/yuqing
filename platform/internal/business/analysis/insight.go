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

// Quote is a verbatim quote from the source material with its platform.
type Quote struct {
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// Dimension is one analytical dimension's verdict. Each dimension is produced
// by its own LLM call with a dedicated persona, organized on the fixed
// skeleton: findings → data_points → quotes → deep_read → trend.
//
// 为什么分维度：单轮单视角的「一次调用出全部」只能产出各方面都平庸的概括，
// 且跨分析高度趋同。分维度独立深挖再汇总，结论才有层次。
type Dimension struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Findings   string   `json:"findings"`
	DataPoints []string `json:"data_points,omitempty"`
	Quotes     []Quote  `json:"quotes,omitempty"`
	DeepRead   string   `json:"deep_read"`
	Trend      string   `json:"trend"`
}

// InsightResult is the analysis engine output: summary + sentiments + topics + dimensions.
type InsightResult struct {
	Summary    string
	Sentiments []Sentiment
	Topics     []Topic
	Dimensions []Dimension
	// Warning 是引擎侧的部分降级说明（如某个维度调用失败但其余成功）。
	// 管线将其合并进任务的 warning 字段。
	Warning string
}

// InsightRequest carries the documents to analyze.
type InsightRequest struct {
	TenantID     string
	AnalysisID   string
	AnalysisType string
	Title        string // 分析任务名，供维度分析 prompt 定位分析对象
	Documents    []Document
	// Mode 是套餐裁剪模式（"" / full = 5 维；quick = 3 维速览）。
	// 空值引擎按 full 处理，既有调用方零改动。
	Mode string
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
	// Dimensions 是分析引擎的五维度研判结论。报告要基于这些结论撰写，
	// 而不是从聚合 JSON 重新概括（否则报告与摘要一样干瘪）。
	Dimensions []Dimension
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

// SetInsight 写入洞察结果（摘要/情感/话题/五维度），管线 analyzing 步骤调用。
func (s *Service) SetInsight(ctx context.Context, tenantID, analysisID string, r InsightResult) error {
	return s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		a.Summary = r.Summary
		a.Sentiments = r.Sentiments
		a.Topics = r.Topics
		a.Dimensions = r.Dimensions
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
