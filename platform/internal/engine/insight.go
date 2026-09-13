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

// RealInsightEngine is an HTTP transport to the Python insight engine
// (sentiment analysis + topic clustering + summary via DeepSeek).
type RealInsightEngine struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
	apiKeyFunc func() string // nil = engine 从其环境变量读取
}

// NewRealInsightEngine creates a transport backed by the Python
// insight_engine endpoints /analyze and /sentiment.
// apiKeyFunc returns the current DeepSeek key from platform settings.
func NewRealInsightEngine(baseURL, authToken string, apiKeyFunc func() string) *RealInsightEngine {
	return &RealInsightEngine{
		baseURL:    baseURL,
		authToken:  authToken,
		apiKeyFunc: apiKeyFunc,
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

// Analyze requests sentiment + topics + summary for the given documents.
func (e *RealInsightEngine) Analyze(ctx context.Context, req *InsightAnalyzeReq) (*InsightAnalyzeResp, error) {
	if e.apiKeyFunc != nil {
		req.APIKey = e.apiKeyFunc()
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("insight: marshal request: %w", err)
	}
	body, err := e.post(ctx, "/analyze", b)
	if err != nil {
		return nil, err
	}
	var resp InsightAnalyzeResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("insight: decode response: %w", err)
	}
	return &resp, nil
}

// Sentiment requests batched sentiment classification.
func (e *RealInsightEngine) Sentiment(ctx context.Context, req *SentimentReq) (*SentimentResp, error) {
	if e.apiKeyFunc != nil {
		req.APIKey = e.apiKeyFunc()
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("insight: marshal request: %w", err)
	}
	body, err := e.post(ctx, "/sentiment", b)
	if err != nil {
		return nil, err
	}
	var resp SentimentResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("insight: decode response: %w", err)
	}
	return &resp, nil
}

func (e *RealInsightEngine) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("insight: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("X-Internal-Token", e.authToken)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("insight: call engine: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("insight: engine returned %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// Compile-time interface check.
var _ InsightEngine = (*RealInsightEngine)(nil)
