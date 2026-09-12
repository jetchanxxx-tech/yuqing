package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
			Timeout: 60 * time.Second,
		},
		timeout: 60 * time.Second,
	}
}

// searchReq matches the Python engine's SearchRequest model (v0.3.0).
type searchReq struct {
	Keywords     []string `json:"keywords"`
	Sources      []string `json:"sources"`
	MaxResults   int      `json:"max_results"`
	DateFrom     string   `json:"date_from,omitempty"`
	DateTo       string   `json:"date_to,omitempty"`
	AnalysisID   string   `json:"analysis_id"`
	ExcludeWords []string `json:"exclude_words,omitempty"`
	BochaAPIKey  string   `json:"bocha_api_key,omitempty"`
}

// searchResp matches the Python engine's SearchResponse model.
type searchResp struct {
	Documents  []map[string]any `json:"documents"`
	TotalCount int              `json:"total_count"`
}

// Crawl sends a crawl request to the Python query engine.
func (e *RealCrawlerEngine) Crawl(ctx context.Context, req *CrawlReq) error {
	body := searchReq{
		Keywords:   req.Keywords,
		Sources:    req.Sources,
		MaxResults: req.MaxDepth,
		AnalysisID: req.AnalysisID,
	}
	// Inject Bocha API key from admin-configurable platform settings.
	if e.bochaKeyFunc != nil {
		body.BochaAPIKey = e.bochaKeyFunc()
	}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("crawler: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/search", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("crawler: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("X-Internal-Token", e.authToken)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("crawler: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("crawler: engine returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result searchResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("crawler: decode response: %w", err)
	}

	return nil // Documents are ingested downstream by the analysis worker.
}

// Compile-time interface check.
var _ CrawlerEngine = (*RealCrawlerEngine)(nil)