// Package analysis provides the analysis orchestration service.
package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/queue"
)

// topicAnalysisTasks is the queue topic for newly scheduled analysis runs.
const topicAnalysisTasks = "analysis.tasks"

// TopicAnalysisTasks 是分析任务的队列主题（导出供组合根订阅）。
const TopicAnalysisTasks = topicAnalysisTasks

// analysisStore 是分析任务的持久化契约：内存实现 memoryStore（重启即失），
// PostgreSQL 实现 pgStore（store_pg.go）。
//
// 方法保持包私有：装配只经 NewService（内存）/ NewPGService（PostgreSQL），
// 上层（api/pipeline）一律走 Service 方法，不直接触碰 store。
type analysisStore interface {
	put(ctx context.Context, tenantID string, a *AnalysisResult) error
	get(ctx context.Context, tenantID, analysisID string) (*AnalysisResult, error)
	list(ctx context.Context, tenantID string) ([]AnalysisResult, error)
	mutate(ctx context.Context, tenantID, analysisID string, fn func(*AnalysisResult) error) error
}

// Service orchestrates analysis tasks through their lifecycle.
// Runs are persisted in the injected store keyed by tenant, then published
// to the queue. The queue stays the only cross-process handoff point.
type Service struct {
	queue       queue.Queue
	concurrency int
	store       analysisStore
	docs        documentStore
}

// NewService creates an analysis orchestration service over in-memory stores
// (进程重启即丢数据，生产接线见 NewPGService).
func NewService(q queue.Queue, concurrency int) *Service {
	return NewServiceWithStore(q, concurrency, newMemoryStore(), newMemoryDocumentStore())
}

// NewServiceWithStore 用指定 store 装配分析服务（store 与 docs 都必须非 nil，
// 传 nil 会在首次读写时 panic —— 不做静默退回内存实现，那等于悄悄丢持久化）。
//
// 参数类型包私有：外部包请用 NewService（内存）或 NewPGService（PostgreSQL）。
func NewServiceWithStore(q queue.Queue, concurrency int, store analysisStore, docs documentStore) *Service {
	return &Service{
		queue:       q,
		concurrency: concurrency,
		store:       store,
		docs:        docs,
	}
}

// NewPGService 装配 PostgreSQL 持久化的分析服务：任务与采集文档跨进程重启
// 存活（组合根取池后调用，池来自 db.Manager.Platform(ctx) 或 pgxpool.New）。
func NewPGService(pool *pgxpool.Pool, q queue.Queue, concurrency int) *Service {
	return NewServiceWithStore(q, concurrency, newPGStore(pool), newPGDocumentStore(pool))
}

// CreateAnalysisRequest holds the parameters for a new analysis.
type CreateAnalysisRequest struct {
	TenantID     string   `json:"tenant_id"`
	UserID       string   `json:"user_id"`
	Name         string   `json:"name"`
	AnalysisType string   `json:"analysis_type"`
	Keywords     []string `json:"keywords"`
	Sources      []string `json:"sources"`
	DateFrom     string   `json:"date_from,omitempty"`
	DateTo       string   `json:"date_to,omitempty"`
}

// AnalysisResult is the full result of a completed analysis.
type AnalysisResult struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	AnalysisType string    `json:"analysis_type"`
	State        State     `json:"state"`
	Progress     int       `json:"progress"`
	ErrorCode    string    `json:"error_code,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	CreatedAt    time.Time `json:"created_at"`

	// 采集参数 —— 管线据此知道搜什么。
	// 不持久化关键词则任务无法被处理（管线只拿到 ID，无从得知检索词）。
	Keywords []string `json:"keywords,omitempty"`
	Sources  []string `json:"sources,omitempty"`
	DocCount int      `json:"doc_count"`

	// 洞察与报告 —— 管线 analyzing/generating_report 步骤写入。
	Summary    string      `json:"summary,omitempty"`
	Warning    string      `json:"warning,omitempty"`
	Sentiments []Sentiment `json:"sentiments,omitempty"`
	Topics     []Topic     `json:"topics,omitempty"`
	Dimensions []Dimension `json:"dimensions,omitempty"`
	ReportID   string      `json:"report_id,omitempty"`

	// ReportContent 是 KB 级 HTML 正文，不随分析对象序列化：
	// GET /analyses 与 /analyses/:id 是高频生命周期端点（详情页按秒轮询
	// state），内联正文会让每次轮询都传输整份报告。
	// 正文经 GET /analyses/:id/result 的 report.content 按需返回。
	ReportContent string `json:"-"`
}

// Create validates parameters, persists the analysis as queued, and
// publishes a task so workers pick it up.
func (s *Service) Create(ctx context.Context, req CreateAnalysisRequest) (*AnalysisResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("analysis: name is required")
	}
	if req.TenantID == "" {
		return nil, fmt.Errorf("analysis: tenant_id is required")
	}

	now := time.Now().UTC()
	result := &AnalysisResult{
		ID:           id.New(),
		Name:         req.Name,
		AnalysisType: req.AnalysisType,
		State:        StateQueued,
		Progress:     0,
		CreatedAt:    now,
		Keywords:     req.Keywords,
		Sources:      req.Sources,
	}

	if err := s.store.put(ctx, req.TenantID, result); err != nil {
		return nil, err
	}

	// 消息必须带 tenantID：store 按租户分桶，仅凭 analysisID 无法定位任务。
	payload, err := json.Marshal(TaskMessage{AnalysisID: result.ID, TenantID: req.TenantID})
	if err != nil {
		return nil, fmt.Errorf("analysis: marshal task: %w", err)
	}
	if err := s.queue.Publish(ctx, topicAnalysisTasks, payload); err != nil {
		return nil, fmt.Errorf("analysis: failed to enqueue: %w", err)
	}
	return result, nil
}

// Get returns one analysis scoped to the tenant.
func (s *Service) Get(ctx context.Context, tenantID, analysisID string) (*AnalysisResult, error) {
	return s.store.get(ctx, tenantID, analysisID)
}

// List returns all analyses of a tenant in creation order.
func (s *Service) List(ctx context.Context, tenantID string) ([]AnalysisResult, error) {
	return s.store.list(ctx, tenantID)
}

// Cancel aborts an active analysis. Terminal analyses cannot be canceled.
func (s *Service) Cancel(ctx context.Context, tenantID, analysisID string) error {
	return s.transition(ctx, tenantID, analysisID, StateCanceled)
}

// Transition moves an analysis along the state machine. It is the single
// write path used by workers to advance queued → … → completed.
func (s *Service) Transition(ctx context.Context, tenantID, analysisID, to string) error {
	if !IsValidState(to) {
		return fmt.Errorf("analysis: invalid target state %q", to)
	}
	return s.transition(ctx, tenantID, analysisID, State(to))
}

func (s *Service) transition(ctx context.Context, tenantID, analysisID string, to State) error {
	now := time.Now().UTC()
	err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		if !CanTransition(string(a.State), string(to)) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict,
				fmt.Sprintf("analysis cannot transition from %s to %s", a.State, to))
		}
		a.State = to
		if IsTerminal(string(to)) {
			a.FinishedAt = now
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Rerun requeues a terminal analysis (completed/failed/canceled) as a fresh
// queued run and republishes its task.
//
// 上一轮的产物一并清空：warning 是追加语义（SetWarning 只增不删），
// 残留的旧降级原因会让重跑成功的任务仍显示「部分维度分析失败」；
// 旧洞察/报告挂在 queued 任务上也与状态自相矛盾。旧报告记录仍在
// reports 列表（report.Service 独立存储），此处只解除 analyses 行上的关联。
func (s *Service) Rerun(ctx context.Context, tenantID, analysisID string) error {
	err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		if !IsTerminal(string(a.State)) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict,
				fmt.Sprintf("only terminal analyses can be rerun, current state is %s", a.State))
		}
		a.State = StateQueued
		a.Progress = 0
		a.ErrorCode = ""
		a.StartedAt = time.Time{}
		a.FinishedAt = time.Time{}
		a.Summary = ""
		a.Warning = ""
		a.Sentiments = nil
		a.Topics = nil
		a.Dimensions = nil
		a.ReportID = ""
		a.ReportContent = ""
		return nil
	})
	if err != nil {
		return err
	}

	// Re-enqueue: same payload contract as Create（JSON + tenantID）。
	payload, err := json.Marshal(TaskMessage{AnalysisID: analysisID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("analysis: marshal task: %w", err)
	}
	if err := s.queue.Publish(ctx, topicAnalysisTasks, payload); err != nil {
		return fmt.Errorf("analysis: failed to re-enqueue: %w", err)
	}
	return nil
}

// advance 推进状态并写入进度（管线专用；外部修改状态请用 Transition）。
func (s *Service) advance(ctx context.Context, tenantID, analysisID string, to State, progress int) error {
	now := time.Now().UTC()
	return s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		if IsTerminal(string(a.State)) {
			// 任务已被取消/已完成（用户操作或重复投递），不再推进
			return pkgerrors.Wrap(pkgerrors.ErrConflict,
				fmt.Sprintf("analysis already terminal (%s)", a.State))
		}
		if !CanTransition(string(a.State), string(to)) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict,
				fmt.Sprintf("analysis cannot transition from %s to %s", a.State, to))
		}
		a.State = to
		a.Progress = progress
		if a.StartedAt.IsZero() {
			a.StartedAt = now
		}
		if IsTerminal(string(to)) {
			a.FinishedAt = now
		}
		return nil
	})
}

// markFailed 标记任务失败并记录错误码。已是终态时不做改动（幂等）。
func (s *Service) markFailed(ctx context.Context, tenantID, analysisID, code string) error {
	return s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		if IsTerminal(string(a.State)) {
			return nil // 用户已取消或任务已完成，保留原状态
		}
		a.State = StateFailed
		a.ErrorCode = code
		a.FinishedAt = time.Now().UTC()
		return nil
	})
}
