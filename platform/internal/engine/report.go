package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
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
		httpClient: &http.Client{Timeout: 660 * time.Second}, // GLM 思考型五维并发实测 266s，180s 会截断（生产实测踩坑）
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
		var problem struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(data, &problem) == nil && problem.Code == "ACCOUNTING_DURABILITY_ERROR" {
			return nil, pkgerrors.ErrAccountingDurability
		}
		return nil, fmt.Errorf("report: engine returned %d: %s", resp.StatusCode, string(data))
	}

	var out ReportGenerateResp
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("report: decode response: %w", err)
	}
	return &out, nil
}

// GenerateStream requests a report in the given format (html/docx) and returns
// the response body as a stream. Caller must close the returned ReadCloser.
func (e *RealReportEngine) GenerateStream(ctx context.Context, req *ReportGenerateReq, format string) (io.ReadCloser, error) {
	if e.apiKeyFunc != nil {
		req.APIKey = e.apiKeyFunc()
	}
	if e.llmOptsFunc != nil {
		req.LLMBaseURL, req.LLMModel = e.llmOptsFunc()
	}
	req.Format = format
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

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("report: engine returned %d: %s", resp.StatusCode, string(data))
	}

	return resp.Body, nil
}

// ExportStream converts the published snapshot, without supplier credentials or
// a new analysis/model call. The caller has already checked tenant and plan.
func (e *RealReportEngine) ExportStream(ctx context.Context, content string) (io.ReadCloser, error) {
	body, err := json.Marshal(struct {
		HTML   string `json:"html_content"`
		Format string `json:"format"`
	}{content, "docx"})
	if err != nil {
		return nil, fmt.Errorf("report: encode export: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/export", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		request.Header.Set("X-Internal-Token", e.authToken)
	}
	response, err := e.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("report: export: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, fmt.Errorf("report: export engine returned %d", response.StatusCode)
	}
	return response.Body, nil
}

// Compile-time interface check.
var _ ReportEngine = (*RealReportEngine)(nil)
