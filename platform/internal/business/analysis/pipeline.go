package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// TaskMessage 是投递到 analysis.tasks 的载荷。
//
// 必须携带 tenantID：store 按租户分桶，仅凭 analysisID 无法定位任务。
// （早期版本只投递裸 ID，管线拿到后无从查起。）
type TaskMessage struct {
	AnalysisID string `json:"analysis_id"`
	TenantID   string `json:"tenant_id"`
}

// Encode 序列化为队列载荷。
func (m TaskMessage) Encode() ([]byte, error) { return json.Marshal(m) }

// DecodeTaskMessage 解析队列载荷，兼容历史格式（裸 ID 字符串）。
func DecodeTaskMessage(body []byte) (TaskMessage, error) {
	var m TaskMessage
	if err := json.Unmarshal(body, &m); err == nil && m.AnalysisID != "" {
		return m, nil
	}
	// 兼容早期只发裸 ID 的消息：此时无法确定租户，交由调用方拒绝
	if s := string(body); s != "" && !looksLikeJSON(s) {
		return TaskMessage{AnalysisID: s}, nil
	}
	return TaskMessage{}, fmt.Errorf("analysis: malformed task message %q", string(body))
}

func looksLikeJSON(s string) bool {
	for _, r := range s {
		if r == '{' || r == '[' {
			return true
		}
		if r != ' ' {
			return false
		}
	}
	return false
}

// FetchRequest 是一次采集请求。
type FetchRequest struct {
	TenantID   string
	AnalysisID string
	Keywords   []string
	Sources    []string
	MaxResults int
}

// Fetcher 采集原始数据。生产实现经 HTTP 调用 Python query 引擎
// （Bocha 搜索 + Scrapling 抓取）；测试注入 fake。
type Fetcher interface {
	Fetch(ctx context.Context, req FetchRequest) ([]Document, error)
}

// 各阶段的进度百分比。前端据此渲染进度条。
const (
	progressBudget   = 10
	progressFetching = 25
	progressAnalyze  = 60
	progressReport   = 85
	progressDone     = 100
)

// Pipeline 推进单个分析任务的状态机，直到终态。
//
// 运行位置：必须与 Service 的 store 同进程 —— 内存 store 是进程私有的，
// 独立 worker 进程看不到 server 创建的任务（实测：worker 收到任务数为 0）。
// 接入 PostgreSQL store 后可将本管线移入独立 worker。
type Pipeline struct {
	svc     *Service
	fetcher Fetcher
	timeout time.Duration
	log     *slog.Logger
}

// NewPipeline 创建管线。timeout <= 0 时使用默认 3 分钟。
func NewPipeline(svc *Service, fetcher Fetcher, timeout time.Duration, log *slog.Logger) *Pipeline {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &Pipeline{svc: svc, fetcher: fetcher, timeout: timeout, log: log}
}

// Handle 处理一条任务消息。返回 error 表示任务未能正常走完
// （任务本身已被标记为 failed，调用方通常无需重试）。
func (p *Pipeline) Handle(ctx context.Context, msg TaskMessage) error {
	if msg.AnalysisID == "" || msg.TenantID == "" {
		return fmt.Errorf("analysis: task message missing analysis_id or tenant_id")
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	a, err := p.svc.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil {
		return fmt.Errorf("analysis: load task %s: %w", msg.AnalysisID, err)
	}
	// 幂等：任务已被取消或已完成后重复投递直接跳过
	if IsTerminal(string(a.State)) {
		p.log.Info("pipeline: task already terminal, skip",
			slog.String("analysis_id", msg.AnalysisID), slog.String("state", string(a.State)))
		return nil
	}

	p.log.Info("pipeline: start",
		slog.String("analysis_id", msg.AnalysisID), slog.String("tenant_id", msg.TenantID))

	// ① 预算检查（MVP 未接 LLM 计量，仅推进状态）
	if err := p.step(ctx, msg, StateAcquiringBudget, progressBudget); err != nil {
		return p.fail(msg, "budget_error", err)
	}

	// ② 数据采集
	if err := p.step(ctx, msg, StateFetching, progressFetching); err != nil {
		return p.fail(msg, "pipeline_error", err)
	}
	docs, err := p.fetcher.Fetch(ctx, FetchRequest{
		TenantID:   msg.TenantID,
		AnalysisID: msg.AnalysisID,
		Keywords:   a.Keywords,
		Sources:    a.Sources,
		MaxResults: 50,
	})
	if err != nil {
		return p.fail(msg, "fetch_failed", err)
	}
	p.svc.AddDocuments(ctx, msg.TenantID, msg.AnalysisID, docs)
	p.setDocCount(ctx, msg, len(docs))
	p.log.Info("pipeline: fetched", slog.String("analysis_id", msg.AnalysisID), slog.Int("docs", len(docs)))

	// ③ 分析（情感/话题，MVP 阶段直接推进）
	if err := p.step(ctx, msg, StateAnalyzing, progressAnalyze); err != nil {
		return p.fail(msg, "pipeline_error", err)
	}

	// ④ 报告生成
	if err := p.step(ctx, msg, StateGeneratingReport, progressReport); err != nil {
		return p.fail(msg, "pipeline_error", err)
	}

	// ⑤ 完成
	if err := p.step(ctx, msg, StateCompleted, progressDone); err != nil {
		return p.fail(msg, "pipeline_error", err)
	}
	p.log.Info("pipeline: completed", slog.String("analysis_id", msg.AnalysisID))
	return nil
}

// step 推进状态并写进度。上下文超时统一归为 timeout。
func (p *Pipeline) step(ctx context.Context, msg TaskMessage, to State, progress int) error {
	if err := ctx.Err(); err != nil {
		return err // 超时或取消，交由 fail 归类
	}
	return p.svc.advance(ctx, msg.TenantID, msg.AnalysisID, to, progress)
}

// fail 把任务标记为失败并记录错误码。任务已是终态时不做改动（幂等）。
func (p *Pipeline) fail(msg TaskMessage, code string, cause error) error {
	// 上下文超时单独归类，便于前端区分「采集失败」与「超时」
	if cause == context.DeadlineExceeded {
		code = "timeout"
	}
	p.log.Warn("pipeline: task failed",
		slog.String("analysis_id", msg.AnalysisID),
		slog.String("code", code),
		slog.String("err", cause.Error()))

	// 用独立上下文：原 ctx 可能已超时，无法再写库
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.svc.markFailed(ctx, msg.TenantID, msg.AnalysisID, code); err != nil {
		p.log.Error("pipeline: mark failed error",
			slog.String("analysis_id", msg.AnalysisID), slog.String("err", err.Error()))
	}
	return fmt.Errorf("analysis %s failed (%s): %w", msg.AnalysisID, code, cause)
}

func (p *Pipeline) setDocCount(ctx context.Context, msg TaskMessage, n int) {
	_ = p.svc.store.mutate(ctx, msg.TenantID, msg.AnalysisID, func(a *AnalysisResult) error {
		a.DocCount = n
		return nil
	})
}
