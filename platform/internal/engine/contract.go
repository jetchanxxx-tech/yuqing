// Package engine defines Go-side contracts for Python analysis engines.
package engine

import "context"

// QuerySearchReq is a multi-source search request.
type QuerySearchReq struct {
	Keywords     []string `json:"keywords"`
	Sources      []string `json:"sources"`
	MaxResults   int      `json:"max_results"`
	DateFrom     string   `json:"date_from,omitempty"`
	DateTo       string   `json:"date_to,omitempty"`
	AnalysisID   string   `json:"analysis_id"`
	ExcludeWords []string `json:"exclude_words,omitempty"`
}

// QuerySearchResp is the search response.
type QuerySearchResp struct {
	Documents  []Document   `json:"documents"`
	TotalCount int          `json:"total_count"`
	Sources    []SourceInfo `json:"sources"`
}

// MediaAnalyzeReq requests multimodal analysis.
type MediaAnalyzeReq struct {
	DocumentIDs []string `json:"document_ids"`
	AnalysisID  string   `json:"analysis_id"`
}

// MediaAnalyzeResp is the media analysis response.
type MediaAnalyzeResp struct {
	Results []MediaResult `json:"results"`
}

// InsightAnalyzeReq requests deep analysis + sentiment + topics.
type InsightAnalyzeReq struct {
	Resilient          bool           `json:"resilient"`
	ReusableComponents map[string]any `json:"reusable_components,omitempty"`
	RunID              string         `json:"run_id"`
	DocumentIDs        []string       `json:"document_ids"`
	Documents          []Document     `json:"documents,omitempty"`
	ContextDocuments   []Document     `json:"context_documents"`
	ResearchIntent     map[string]any `json:"research_intent,omitempty"`
	AnalysisID         string         `json:"analysis_id"`
	AnalysisType       string         `json:"analysis_type"`
	ScenarioTemplateID string         `json:"scenario_template_id,omitempty"`
	Title              string         `json:"title,omitempty"` // 分析任务名（供维度分析 prompt 用）
	DateFrom           string         `json:"date_from,omitempty"`
	DateTo             string         `json:"date_to,omitempty"`
	APIKey             string         `json:"api_key,omitempty"`
	// LLM 供应商可配置（后台「数据源配置」在线修改，零重启透传引擎）：
	LLMBaseURL string `json:"llm_base_url,omitempty"`
	LLMModel   string `json:"llm_model,omitempty"`
	// Mode 是套餐裁剪模式："" / "full" = 5 维完整研判（思考型），
	// "quick" = 3 维速览（热度/情感/原因，支持的模型降低推理强度）。
	// Lite/体验档传 quick。
	Mode string `json:"mode,omitempty"`
}

// InsightAnalyzeResp is the deep analysis response.
type InsightAnalyzeResp struct {
	BatchComponents []map[string]any  `json:"batch_components,omitempty"`
	Sentiments      []SentimentResult `json:"sentiments"`
	Topics          []TopicResult     `json:"topics"`
	Summary         string            `json:"summary"`
	// Dimensions 是五个维度的独立研判（背景/热度/情感观点/群体差异/深层原因）。
	Dimensions []DimensionResult `json:"dimensions"`
	// Warning 是引擎侧的降级原因（如某个维度调用失败），与平台侧 warning 合并展示。
	Warning          string         `json:"warning"`
	MaterialCoverage map[string]any `json:"material_coverage,omitempty"`
}

// DimensionResult is one analytical dimension's verdict, organized on the
// fixed skeleton: findings → data → quotes → deep_read → trend.
type DimensionResult struct {
	Recommendations []map[string]any `json:"recommendations,omitempty"`
	Complete        bool             `json:"complete,omitempty"`
	BatchComponents []map[string]any `json:"batch_components,omitempty"`
	QuoteLocations  []map[string]any `json:"quote_locations,omitempty"`
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Findings        string           `json:"findings"`
	DataPoints      []string         `json:"data_points,omitempty"`
	Quotes          []QuoteResult    `json:"quotes,omitempty"`
	DeepRead        string           `json:"deep_read"`
	Trend           string           `json:"trend"`
}

// QuoteResult is a verbatim quote from the source material with its platform.
type QuoteResult struct {
	DocumentID string `json:"document_id,omitempty"`
	Text       string `json:"text"`
	Source     string `json:"source"`
}

// SentimentReq requests batched sentiment classification.
type SentimentReq struct {
	RunID      string     `json:"run_id"`
	Documents  []Document `json:"documents"`
	Model      string     `json:"model,omitempty"`
	AnalysisID string     `json:"analysis_id"`
	APIKey     string     `json:"api_key,omitempty"`
}

// SentimentResp is the batched sentiment response.
type SentimentResp struct {
	Results []SentimentResult `json:"results"`
}

// ReportGenerateReq requests report generation from document IR.
type ReportGenerateReq struct {
	Resilient               bool              `json:"resilient"`
	DeliveryLevel           string            `json:"delivery_level,omitempty"`
	DeliveryReasons         []string          `json:"delivery_reasons"`
	DeliveryPolicy          string            `json:"delivery_policy,omitempty"`
	UnverifiedDocuments     []Document        `json:"unverified_documents"`
	Summary                 string            `json:"summary"`
	Mode                    string            `json:"mode"`
	AnalysisType            string            `json:"analysis_type,omitempty"`
	ScenarioTemplateID      string            `json:"scenario_template_id,omitempty"`
	RunID                   string            `json:"run_id"`
	Title                   string            `json:"title"`
	DateFrom                string            `json:"date_from,omitempty"`
	DateTo                  string            `json:"date_to,omitempty"`
	TemplateID              string            `json:"template_id"`
	Format                  string            `json:"format"` // html, markdown, pdf, docx
	Documents               []Document        `json:"documents"`
	ContextDocuments        []Document        `json:"context_documents"`
	ResearchIntent          map[string]any    `json:"research_intent,omitempty"`
	InsightMaterialCoverage map[string]any    `json:"insight_material_coverage,omitempty"`
	Sentiments              []SentimentResult `json:"sentiments"`
	Topics                  []TopicResult     `json:"topics"`
	// Dimensions 是分析引擎产出的五维度研判，报告基于它撰写。
	Dimensions []DimensionResult `json:"dimensions,omitempty"`
	AnalysisID string            `json:"analysis_id"`
	APIKey     string            `json:"api_key,omitempty"`
	LLMBaseURL string            `json:"llm_base_url,omitempty"`
	LLMModel   string            `json:"llm_model,omitempty"`
	// InsightAvailable = 洞察引擎是否成功产出（false 时报告引擎不得
	// 把空情感数据渲染成 0/0/0）。
	InsightAvailable bool `json:"insight_available"`
}

// ReportGenerateResp is the report generation response.
type ReportGenerateResp struct {
	DeliveryLevel    string         `json:"delivery_level"`
	DeliveryReasons  []string       `json:"delivery_reasons"`
	Warning          string         `json:"warning,omitempty"`
	MaterialCoverage map[string]any `json:"material_coverage,omitempty"`
	ReportID         string         `json:"report_id"`
	FileKey          string         `json:"file_key"`
	Format           string         `json:"format"`
	Content          string         `json:"content"`
}

// ForumRunReq requests a multi-agent forum debate.
type ForumRunReq struct {
	Topic      string     `json:"topic"`
	Documents  []Document `json:"documents"`
	AnalysisID string     `json:"analysis_id"`
	MaxRounds  int        `json:"max_rounds"`
}

// ForumRunResp is the forum debate response.
type ForumRunResp struct {
	Rounds     []ForumRound `json:"rounds"`
	Verdict    string       `json:"verdict"`
	Confidence float64      `json:"confidence"`
}

// CrawlReq schedules a platform-managed crawl.
type CrawlReq struct {
	Resilient             bool             `json:"resilient"`
	DeliveryPolicy        string           `json:"delivery_policy"`
	SeedURLs              []string         `json:"seed_urls"`
	ResumeFromRunID       string           `json:"resume_from_run_id,omitempty"`
	RetainedCandidates    []map[string]any `json:"retained_candidates"`
	TenantID              string           `json:"tenant_id"`
	Title                 string           `json:"title"`
	AnalysisType          string           `json:"analysis_type"`
	ScenarioTemplateID    string           `json:"scenario_template_id,omitempty"`
	ResearchQuestion      string           `json:"research_question,omitempty"`
	ResearchIntentVersion string           `json:"research_intent_version,omitempty"`
	RunID                 string           `json:"run_id,omitempty"`
	Sources               []string         `json:"sources"`
	Keywords              []string         `json:"keywords"`
	AnalysisID            string           `json:"analysis_id"`
	MaxDepth              int              `json:"max_depth"`
	DateFrom              string           `json:"date_from,omitempty"`
	DateTo                string           `json:"date_to,omitempty"`
	ExcludeWords          []string         `json:"exclude_words,omitempty"`
	ResearchIntent        map[string]any   `json:"research_intent,omitempty"`
}

// Shared types used across engine contracts.

type Document struct {
	ID                     string            `json:"id"`
	Title                  string            `json:"title"`
	URL                    string            `json:"url"`
	Content                string            `json:"content"`
	Author                 string            `json:"author,omitempty"`
	SourceType             string            `json:"source_type"`
	SourceName             string            `json:"source_name"`
	PublishedAt            string            `json:"published_at,omitempty"`
	ContentHash            string            `json:"content_hash"`
	MediaURL               string            `json:"media_url,omitempty"`
	SourcePublishedAt      string            `json:"source_published_at,omitempty"`
	DateSources            map[string]string `json:"date_sources,omitempty"`
	ObservedSpans          []map[string]int  `json:"observed_spans,omitempty"`
	ContentKind            string            `json:"content_kind,omitempty"`
	FetchStatus            string            `json:"fetch_status,omitempty"`
	FetchReason            string            `json:"fetch_reason,omitempty"`
	FinalURL               string            `json:"final_url,omitempty"`
	Truncated              bool              `json:"truncated,omitempty"`
	SemanticClassification string            `json:"semantic_classification,omitempty"`
	SemanticReason         string            `json:"semantic_reason,omitempty"`
	EvidenceSpans          []string          `json:"evidence_spans,omitempty"`
}

type MediaResult struct {
	DocumentID string   `json:"document_id"`
	Transcript string   `json:"transcript,omitempty"`
	OCRText    string   `json:"ocr_text,omitempty"`
	Objects    []string `json:"objects,omitempty"`
	Faces      int      `json:"faces,omitempty"`
}

type SentimentResult struct {
	DocumentID string             `json:"document_id"`
	Sentiment  string             `json:"sentiment"`            // positive, negative, neutral
	Level      string             `json:"level,omitempty"`      // 非常正面|正面|中性|负面|非常负面
	Confidence float64            `json:"confidence,omitempty"` // 0-1 置信度
	Score      float64            `json:"score"`
	Emotions   map[string]float64 `json:"emotions,omitempty"`
	Aspects    []AspectResult     `json:"aspects,omitempty"`
	Model      string             `json:"model"`
}

type AspectResult struct {
	Name      string  `json:"name"`
	Sentiment string  `json:"sentiment"`
	Score     float64 `json:"score"`
}

type TopicResult struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
	DocCount int      `json:"doc_count"`
	DocIDs   []string `json:"doc_ids,omitempty"`
	Trend    string   `json:"trend"` // rising, stable, falling
}

type ForumRound struct {
	Round     int      `json:"round"`
	Agent     string   `json:"agent"`
	Statement string   `json:"statement"`
	Evidence  []string `json:"evidence,omitempty"`
}

type SourceInfo struct {
	Name     string `json:"name"`
	DocCount int    `json:"doc_count"`
	Status   string `json:"status"` // ok, partial, error
}

// Engine interfaces consumed by the business layer.

type QueryEngine interface {
	Search(ctx context.Context, req *QuerySearchReq) (*QuerySearchResp, error)
}

type MediaEngine interface {
	Analyze(ctx context.Context, req *MediaAnalyzeReq) (*MediaAnalyzeResp, error)
}

type InsightEngine interface {
	Analyze(ctx context.Context, req *InsightAnalyzeReq) (*InsightAnalyzeResp, error)
	Sentiment(ctx context.Context, req *SentimentReq) (*SentimentResp, error)
}

type ReportEngine interface {
	Generate(ctx context.Context, req *ReportGenerateReq) (*ReportGenerateResp, error)
}

type ForumEngine interface {
	RunForum(ctx context.Context, req *ForumRunReq) (*ForumRunResp, error)
}

type CrawlerEngine interface {
	Crawl(ctx context.Context, req *CrawlReq) error
}
