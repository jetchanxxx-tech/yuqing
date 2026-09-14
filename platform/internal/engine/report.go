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

// RealReportEngine is an HTTP transport to the Python report engine
// (LLM insight + HTML template rendering).
type RealReportEngine struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
	apiKeyFunc func() string // nil = engine 从其环境变量读取
	// llmOptsFunc 返回 (base_url, model)，后台可配置经此零重启透传；nil = 引擎用自身默认
	llmOptsFunc func() (string, string)
}

// NewRealReportEngine creates a transport backed by the Python
// report_engine /generate endpoint.
func NewRealReportEngine(baseURL, authToken string, apiKeyFunc func() string) *RealReportEngine {
	return &RealReportEngine{
		baseURL:    baseURL,
		authToken:  authToken,
		apiKeyFunc: apiKeyFunc,
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}
}

// WithLLMOpts 注入 LLM 供应商配置读取器（后台「数据源配置」在线修改，
// 每次请求实时读取 —— 换供应商/key 零重启生效）。nil = 引擎用自身默认。
func (e *RealReportEngine) WithLLMOpts(f func() (string, string)) *RealReportEngine {
	e.llmOptsFunc = f
	return e
}


// Generate requests an HTML report for the analysis and returns its content.
func (e *RealReportEngine) Generate(ctx context.Context, req *ReportGenerateReq) (*ReportGenerateResp, error) {
	if e.apiKeyFunc != nil {
		req.APIKey = e.apiKeyFunc()
	}
	if e.llmOptsFunc != nil {
		req.LLMBaseURL, req.LLMModel = e.llmOptsFunc()
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("report: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/generate", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("report: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("X-Internal-Token", e.authToken)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("report: call engine: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("report: engine returned %d: %s", resp.StatusCode, string(data))
	}

	var out ReportGenerateResp
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("report: decode response: %w", err)
	}
	return &out, nil
}

// Compile-time interface check.
var _ ReportEngine = (*RealReportEngine)(nil)
