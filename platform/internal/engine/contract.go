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
	DocumentIDs  []string `json:"document_ids"`
	AnalysisID   string   `json:"analysis_id"`
	AnalysisType string   `json:"analysis_type"`
}

// InsightAnalyzeResp is the deep analysis response.
type InsightAnalyzeResp struct {
	Sentiments []SentimentResult `json:"sentiments"`
	Topics     []TopicResult     `json:"topics"`
	Summary    string            `json:"summary"`
}

// SentimentReq requests batched sentiment classification.
type SentimentReq struct {
	Documents  []Document   `json:"documents"`
	Model      string       `json:"model,omitempty"`
	AnalysisID string       `json:"analysis_id"`
}

// SentimentResp is the batched sentiment response.
type SentimentResp struct {
	Results []SentimentResult `json:"results"`
}

// ReportGenerateReq requests report generation from document IR.
type ReportGenerateReq struct {
	Title      string   `json:"title"`
	TemplateID string   `json:"template_id"`
	Format     string   `json:"format"` // html, markdown, pdf, docx
	Documents  []Document `json:"documents"`
	Sentiments []SentimentResult `json:"sentiments"`
	Topics     []TopicResult `json:"topics"`
	AnalysisID string   `json:"analysis_id"`
}

// ReportGenerateResp is the report generation response.
type ReportGenerateResp struct {
	ReportID string `json:"report_id"`
	FileKey  string `json:"file_key"`
	Format   string `json:"format"`
}

// ForumRunReq requests a multi-agent forum debate.
type ForumRunReq struct {
	Topic       string     `json:"topic"`
	Documents   []Document `json:"documents"`
	AnalysisID  string     `json:"analysis_id"`
	MaxRounds   int        `json:"max_rounds"`
}

// ForumRunResp is the forum debate response.
type ForumRunResp struct {
	Rounds   []ForumRound `json:"rounds"`
	Verdict  string       `json:"verdict"`
	Confidence float64    `json:"confidence"`
}

// CrawlReq schedules a platform-managed crawl.
type CrawlReq struct {
	Sources    []string `json:"sources"`
	Keywords   []string `json:"keywords"`
	AnalysisID string   `json:"analysis_id"`
	MaxDepth   int      `json:"max_depth"`
}

// Shared types used across engine contracts.

type Document struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Content     string `json:"content"`
	Author      string `json:"author,omitempty"`
	SourceType  string `json:"source_type"`
	SourceName  string `json:"source_name"`
	PublishedAt string `json:"published_at,omitempty"`
	ContentHash string `json:"content_hash"`
	MediaURL    string `json:"media_url,omitempty"`
}

type MediaResult struct {
	DocumentID   string `json:"document_id"`
	Transcript   string `json:"transcript,omitempty"`
	OCRText      string `json:"ocr_text,omitempty"`
	Objects      []string `json:"objects,omitempty"`
	Faces        int    `json:"faces,omitempty"`
}

type SentimentResult struct {
	DocumentID   string            `json:"document_id"`
	Sentiment    string            `json:"sentiment"` // positive, negative, neutral
	Score        float64           `json:"score"`
	Emotions     map[string]float64 `json:"emotions,omitempty"`
	Aspects      []AspectResult    `json:"aspects,omitempty"`
	Model        string            `json:"model"`
}

type AspectResult struct {
	Name      string  `json:"name"`
	Sentiment string  `json:"sentiment"`
	Score     float64 `json:"score"`
}

type TopicResult struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Keywords  []string `json:"keywords"`
	DocCount  int      `json:"doc_count"`
	Trend     string   `json:"trend"` // rising, stable, falling
}

type ForumRound struct {
	Round     int              `json:"round"`
	Agent     string           `json:"agent"`
	Statement string           `json:"statement"`
	Evidence  []string         `json:"evidence,omitempty"`
}

type SourceInfo struct {
	Name      string `json:"name"`
	DocCount  int    `json:"doc_count"`
	Status    string `json:"status"` // ok, partial, error
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
