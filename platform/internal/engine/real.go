package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/yuqing/platform/internal/pkg/retrieval"
	"io"
	"net/http"
	"time"
)

// RealCrawlerEngine is an HTTP transport to the Python query engine.
// It replaces FakeCrawlerEngine once the Python engine is deployed.
type RealCrawlerEngine struct {
	baseURL      string
	authToken    string
	httpClient   *http.Client
	timeout      time.Duration
	bochaKeyFunc func() string // nil = env BOCHA_API_KEY
	llmOptsFunc  func() (string, string)
	llmKeyFunc   func() string
}

func (e *RealCrawlerEngine) WithLLMOpts(f func() (string, string)) *RealCrawlerEngine {
	e.llmOptsFunc = f
	return e
}

func (e *RealCrawlerEngine) WithLLMKey(f func() string) *RealCrawlerEngine {
	e.llmKeyFunc = f
	return e
}

// NewRealCrawlerEngine creates a crawler backed by the Python query_engine /search endpoint.
// bochaKeyFunc returns the current Bocha API key from platform settings (admin-configurable).
// If nil, the Python engine reads BOCHA_API_KEY from its own environment.
func NewRealCrawlerEngine(baseURL, authToken string, bochaKeyFunc func() string) *RealCrawlerEngine {
	return &RealCrawlerEngine{
		baseURL:      baseURL,
		authToken:    authToken,
		bochaKeyFunc: bochaKeyFunc,
		httpClient: &http.Client{
			// 一次 /search = Bocha 搜索 + 逐 URL 抓正文（含慢站 30s 超时重试），
			// 60s 会把总时长破线的成功采集误判为失败（生产实测），放宽到 180s
			Timeout: 960 * time.Second,
		},
		timeout: 960 * time.Second,
	}
}

// searchReq matches the Python engine's SearchRequest model (v0.3.0).
type searchReq struct {
	TenantID              string         `json:"tenant_id"`
	Title                 string         `json:"title"`
	AnalysisType          string         `json:"analysis_type"`
	ScenarioTemplateID    string         `json:"scenario_template_id,omitempty"`
	ResearchQuestion      string         `json:"research_question,omitempty"`
	ResearchIntentVersion string         `json:"research_intent_version,omitempty"`
	RunID                 string         `json:"run_id,omitempty"`
	Keywords              []string       `json:"keywords"`
	Sources               []string       `json:"sources"`
	MaxResults            int            `json:"max_results"`
	DateFrom              string         `json:"date_from,omitempty"`
	DateTo                string         `json:"date_to,omitempty"`
	AnalysisID            string         `json:"analysis_id"`
	ExcludeWords          []string       `json:"exclude_words,omitempty"`
	BochaAPIKey           string         `json:"bocha_api_key,omitempty"`
	LLMBaseURL            string         `json:"llm_base_url,omitempty"`
	LLMModel              string         `json:"llm_model,omitempty"`
	LLMAPIKey             string         `json:"llm_api_key,omitempty"`
	ResearchIntent        map[string]any `json:"research_intent,omitempty"`
}

type SearchCoverage = retrieval.Coverage

type SearchResult struct {
	Documents  []Document     `json:"documents"`
	TotalCount int            `json:"total_count"`
	Coverage   SearchCoverage `json:"coverage"`
}

// Search 调用 Python query 引擎采集数据并返回文档。
func (e *RealCrawlerEngine) Search(ctx context.Context, req *CrawlReq) ([]Document, error) {
	result, err := e.SearchWithCoverage(ctx, req)
	return result.Documents, err
}

func (e *RealCrawlerEngine) SearchWithCoverage(ctx context.Context, req *CrawlReq) (SearchResult, error) {
	body := searchReq{
		TenantID: req.TenantID, Title: req.Title, AnalysisType: req.AnalysisType, ScenarioTemplateID: req.ScenarioTemplateID, ResearchQuestion: req.ResearchQuestion, ResearchIntentVersion: req.ResearchIntentVersion,
		RunID:          req.RunID,
		Keywords:       req.Keywords,
		Sources:        req.Sources,
		MaxResults:     req.MaxDepth,
		AnalysisID:     req.AnalysisID,
		DateFrom:       req.DateFrom,
		DateTo:         req.DateTo,
		ExcludeWords:   req.ExcludeWords,
		ResearchIntent: req.ResearchIntent,
	}
	// Inject Bocha API key from admin-configurable platform settings.
	if e.bochaKeyFunc != nil {
		body.BochaAPIKey = e.bochaKeyFunc()
	}
	if e.llmOptsFunc != nil {
		body.LLMBaseURL, body.LLMModel = e.llmOptsFunc()
	}
	if e.llmKeyFunc != nil {
		body.LLMAPIKey = e.llmKeyFunc()
	}
	b, err := json.Marshal(body)
	if err != nil {
		return SearchResult{}, fmt.Errorf("crawler: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/search", bytes.NewReader(b))
	if err != nil {
		return SearchResult{}, fmt.Errorf("crawler: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("X-Internal-Token", e.authToken)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return SearchResult{}, fmt.Errorf("crawler: fetch: %w", err)
	}
	defer resp.Body.Close()

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (128<<20)+1))
	if readErr != nil || len(data) > 128<<20 {
		return SearchResult{}, fmt.Errorf("crawler: response read failed or exceeded evidence limit")
	}
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Detail struct {
				Code     string         `json:"code"`
				Coverage SearchCoverage `json:"coverage"`
			} `json:"detail"`
		}
		_ = json.Unmarshal(data, &problem)
		return SearchResult{Coverage: problem.Detail.Coverage}, fmt.Errorf("crawler: engine returned %d (%s)", resp.StatusCode, problem.Detail.Code)
	}
	var result SearchResult
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("crawler: decode response: %w", err)
	}
	if result.Coverage.AdmissionVersion != "lexical-v1" && result.Coverage.AdmissionVersion != retrieval.SemanticVersion {
		return result, fmt.Errorf("crawler: unsupported admission protocol %q", result.Coverage.AdmissionVersion)
	}
	if result.Documents == nil || result.TotalCount != len(result.Documents) || result.Coverage.AcceptedCount != len(result.Documents) {
		return result, fmt.Errorf("crawler: inconsistent admitted document count")
	}
	if req.RunID != "" || result.Coverage.AdmissionVersion == retrieval.SemanticVersion {
		materials := make([]retrieval.Material, 0, len(result.Documents))
		for _, d := range result.Documents {
			materials = append(materials, retrieval.Material{ID: d.ID, Title: d.Title, Content: d.Content, URL: d.URL, FinalURL: d.FinalURL, SourceType: d.SourceType, PublishedAt: d.PublishedAt, Classification: d.SemanticClassification, Reason: d.SemanticReason, EvidenceSpans: d.EvidenceSpans, ObservedSpans: d.ObservedSpans, DateSources: d.DateSources})
		}
		if err := retrieval.ValidateResult(&result.Coverage, materials, retrieval.Bounds{Sources: req.Sources, ExcludeWords: req.ExcludeWords, DateFrom: req.DateFrom, DateTo: req.DateTo}, true); err != nil {
			result.Documents = nil
			return result, fmt.Errorf("crawler: admission protocol: %w", err)
		}
	}
	return result, nil
}

// Crawl 实现 CrawlerEngine 接口；采集结果由 Search 返回给管线消费。
// 保留此方法是为了接口兼容（旧调用方只关心是否触发成功）。
func (e *RealCrawlerEngine) Crawl(ctx context.Context, req *CrawlReq) error {
	_, err := e.Search(ctx, req)
	return err
}

// Compile-time interface check.
var _ CrawlerEngine = (*RealCrawlerEngine)(nil)
