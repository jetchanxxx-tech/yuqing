package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/retrieval"
	"io"
	"net/http"
	"strings"
	"time"
)

// RealCrawlerEngine is an HTTP transport to the Python query engine.
// It replaces FakeCrawlerEngine once the Python engine is deployed.
type RealCrawlerEngine struct {
	baseURL        string
	authToken      string
	httpClient     *http.Client
	timeout        time.Duration
	bochaKeyFunc   func() string // nil = env BOCHA_API_KEY
	llmOptsFunc    func() (string, string)
	llmKeyFunc     func() string
	searchSettings func() (string, string, string, string)
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
	Resilient             bool             `json:"resilient"`
	DeliveryPolicy        string           `json:"delivery_policy"`
	SeedURLs              []string         `json:"seed_urls"`
	ResumeFromRunID       string           `json:"resume_from_run_id,omitempty"`
	RetainedCandidates    []map[string]any `json:"retained_candidates"`
	SearchProvider        string           `json:"search_provider,omitempty"`
	SearchAPIKey          string           `json:"search_api_key,omitempty"`
	SearchBaseURL         string           `json:"search_base_url,omitempty"`
	FetchProvider         string           `json:"fetch_provider,omitempty"`
	TenantID              string           `json:"tenant_id"`
	Title                 string           `json:"title"`
	AnalysisType          string           `json:"analysis_type"`
	ScenarioTemplateID    string           `json:"scenario_template_id,omitempty"`
	ResearchQuestion      string           `json:"research_question,omitempty"`
	ResearchIntentVersion string           `json:"research_intent_version,omitempty"`
	RunID                 string           `json:"run_id,omitempty"`
	Keywords              []string         `json:"keywords"`
	Sources               []string         `json:"sources"`
	MaxResults            int              `json:"max_results"`
	DateFrom              string           `json:"date_from,omitempty"`
	DateTo                string           `json:"date_to,omitempty"`
	AnalysisID            string           `json:"analysis_id"`
	ExcludeWords          []string         `json:"exclude_words,omitempty"`
	BochaAPIKey           string           `json:"bocha_api_key,omitempty"`
	LLMBaseURL            string           `json:"llm_base_url,omitempty"`
	LLMModel              string           `json:"llm_model,omitempty"`
	LLMAPIKey             string           `json:"llm_api_key,omitempty"`
	ResearchIntent        map[string]any   `json:"research_intent,omitempty"`
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
		Resilient: req.Resilient, DeliveryPolicy: req.DeliveryPolicy, SeedURLs: append([]string{}, req.SeedURLs...), ResumeFromRunID: req.ResumeFromRunID, RetainedCandidates: append([]map[string]any{}, req.RetainedCandidates...),
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
	if e.searchSettings != nil {
		body.SearchProvider, body.SearchAPIKey, body.SearchBaseURL, body.FetchProvider = e.searchSettings()
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
		if strings.Contains(problem.Detail.Code, "accounting") || strings.Contains(problem.Detail.Code, "billing") {
			return SearchResult{Coverage: problem.Detail.Coverage}, fmt.Errorf("crawler: %s: %w", problem.Detail.Code, pkgerrors.ErrAccountingDurability)
		}
		if problem.Detail.Code == "run_canceled" {
			return SearchResult{Coverage: problem.Detail.Coverage}, fmt.Errorf("crawler: %s: %w", problem.Detail.Code, context.Canceled)
		}
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
	var deliveryHint string
	_ = json.Unmarshal(result.Coverage.Extra["delivery_hint"], &deliveryHint)
	if req.Resilient && deliveryHint == "brief" {
		for _, d := range result.Documents {
			if d.SemanticClassification != "uncertain" || strings.TrimSpace(d.Content) == "" {
				return SearchResult{}, fmt.Errorf("crawler: raw brief material must remain uncertain")
			}
			if err := retrieval.HardBoundary(retrieval.Material{ID: d.ID, Title: d.Title, Content: d.Content, URL: d.URL, FinalURL: d.FinalURL, SourceType: d.SourceType, PublishedAt: d.PublishedAt, DateSources: d.DateSources}, retrieval.Bounds{Sources: req.Sources, ExcludeWords: req.ExcludeWords, DateFrom: req.DateFrom, DateTo: req.DateTo}); err != nil {
				return SearchResult{}, fmt.Errorf("crawler: raw material boundary: %w", err)
			}
		}
		if result.Coverage.DirectCount != 0 || result.Coverage.ContextCount != 0 {
			return SearchResult{}, fmt.Errorf("crawler: uncertain materials cannot claim semantic admission")
		}
		return result, nil
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

// WithSearchSettings accepts only trusted server configuration callbacks.
func (e *RealCrawlerEngine) WithSearchSettings(fn func() (string, string, string, string)) *RealCrawlerEngine {
	e.searchSettings = fn
	return e
}

func (e *RealCrawlerEngine) Capabilities(ctx context.Context) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/capabilities", nil)
	if err != nil {
		return nil, err
	}
	if e.authToken != "" {
		req.Header.Set("X-Internal-Token", e.authToken)
	}
	client := *e.httpClient
	client.Timeout = 10 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query capabilities unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || !json.Valid(data) {
		return nil, fmt.Errorf("invalid query capabilities")
	}
	return data, nil
}
