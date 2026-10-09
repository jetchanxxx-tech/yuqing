package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
	"log/slog"
	"strings"
	"time"
)

// TaskMessage 是投递到 analysis.tasks 的载荷。
//
// 必须携带 tenantID：store 按租户分桶，仅凭 analysisID 无法定位任务。
// （早期版本只投递裸 ID，管线拿到后无从查起。）
type TaskMessage struct {
	RunID      string `json:"run_id,omitempty"`
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
	TenantID     string
	AnalysisID   string
	Keywords     []string
	Sources      []string
	ExcludeWords []string
	DateFrom     string
	DateTo       string
	MaxResults   int
}

// RetrievalCoverage is the versioned, tenant-scoped audit of this run's search candidates.
type RetrievalCoverage struct {
	AdmissionVersion    string           `json:"admission_version"`
	ProviderCandidates  int              `json:"provider_candidates"`
	UnusableCount       int              `json:"unusable_count"`
	IrrelevantCount     int              `json:"irrelevant_count"`
	SourceMismatchCount int              `json:"source_mismatch_count"`
	AcceptedCount       int              `json:"accepted_count"`
	CandidateTruncated  bool             `json:"candidate_truncated"`
	PerKeyword          []map[string]any `json:"per_keyword"`
}

type FetchResult struct {
	Documents []Document
	Warning   string
	Coverage  *RetrievalCoverage
}

type coverageFetcher interface {
	FetchWithCoverage(ctx context.Context, req FetchRequest) (FetchResult, error)
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
	svc       *Service
	fetcher   Fetcher
	analyzer  InsightAnalyzer // nil = 未配置，跳过分析并记录 warning
	generator ReportGenerator // nil = 未配置，跳过报告并记录 warning
	reportSvc reportSvc       // nil = 跳过报告记录创建（向后兼容）
	timeout   time.Duration
	log       *slog.Logger
	// modeFor 按租户返回套餐裁剪模式（quick/full）；nil = 全部 full。
	modeFor func(tenantID string) string
}

// reportSvc 报告服务的最小接口（避免 business/analysis → business/report 循环依赖）。
// 返回报告 ID（空串表示创建失败但非致命）。
type reportSvc interface {
	CreateFromAnalysis(ctx context.Context, tenantID, analysisID, format, createdBy string) (string, error)
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

// WithAnalyzer 注入洞察分析器（情感/话题/摘要）。builder 风格保证
// 既有 NewPipeline 调用零改动。
func (p *Pipeline) WithAnalyzer(a InsightAnalyzer) *Pipeline {
	p.analyzer = a
	return p
}

// WithGenerator 注入报告生成器。
func (p *Pipeline) WithGenerator(g ReportGenerator) *Pipeline {
	p.generator = g
	return p
}

// WithModeFor 注入套餐模式解析（租户 → quick/full）。nil = 全部完整模式。
func (p *Pipeline) WithModeFor(fn func(tenantID string) string) *Pipeline {
	p.modeFor = fn
	return p
}

// WithReportSvc 注入报告服务，用于在管线完成后创建 reports 表记录。
// nil = 跳过记录创建（向后兼容旧配置）。
func (p *Pipeline) WithReportSvc(svc reportSvc) *Pipeline {
	p.reportSvc = svc
	return p
}

// modeForTenant 解析租户当前的分析模式；未注入或解析失败按完整模式。
func (p *Pipeline) modeForTenant(tenantID string) string {
	if p.modeFor == nil {
		return ""
	}
	return p.modeFor(tenantID)
}

// Handle 处理一条任务消息。返回 error 表示任务未能正常走完
// （任务本身已被标记为 failed，调用方通常无需重试）。
func (p *Pipeline) Handle(ctx context.Context, msg TaskMessage) error {
	if msg.AnalysisID == "" || msg.TenantID == "" {
		return fmt.Errorf("analysis: task message missing analysis_id or tenant_id")
	}

	resolved, current, err := p.svc.ResolveTask(ctx, msg)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	msg = resolved
	ctx = billingpolicy.WithRun(ctx, msg.RunID)
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	a, err := p.svc.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil {
		return fmt.Errorf("analysis: load task %s: %w", msg.AnalysisID, err)
	}
	// 幂等：任务已被取消或已完成后重复投递直接跳过
	if IsTerminal(string(a.State)) {
		if a.State == StateCompleted && a.ReportContent != "" {
			if _, durable := p.reportSvc.(durableReportService); durable {
				return p.ensureReportRecord(ctx, msg, a)
			}
		}
		p.log.Info("pipeline: task already terminal, skip",
			slog.String("analysis_id", msg.AnalysisID), slog.String("state", string(a.State)))
		return nil
	}

	p.log.Info("pipeline: start",
		slog.String("analysis_id", msg.AnalysisID), slog.String("tenant_id", msg.TenantID))

	// ① 预算检查（MVP 未接 LLM 计量，仅推进状态）
	if err := p.step(ctx, msg, StateAcquiringBudget, progressBudget); err != nil {
		return p.fail(ctx, msg, "budget_error", err)
	}

	// ② 数据采集
	if err := p.step(ctx, msg, StateFetching, progressFetching); err != nil {
		return p.fail(ctx, msg, "pipeline_error", err)
	}
	fetchReq := FetchRequest{
		TenantID:     msg.TenantID,
		AnalysisID:   msg.AnalysisID,
		Keywords:     a.Keywords,
		Sources:      a.Sources,
		ExcludeWords: a.ExcludeWords,
		DateFrom:     a.DateFrom,
		DateTo:       a.DateTo,
		MaxResults:   50,
	}
	var docs []Document
	var coverageWarning string
	var retrievalCoverage *RetrievalCoverage
	if fetcher, ok := p.fetcher.(coverageFetcher); ok {
		result, fetchErr := fetcher.FetchWithCoverage(ctx, fetchReq)
		docs, coverageWarning, retrievalCoverage, err = result.Documents, result.Warning, result.Coverage, fetchErr
	} else {
		docs, err = p.fetcher.Fetch(ctx, fetchReq)
	}
	if err != nil {
		return p.fail(ctx, msg, "fetch_failed", err)
	}
	if coverageWarning != "" {
		_ = p.svc.SetWarning(ctx, msg.TenantID, msg.AnalysisID, coverageWarning)
	}
	if retrievalCoverage != nil {
		if retrievalCoverage.AdmissionVersion != "lexical-v1" || retrievalCoverage.AcceptedCount != len(docs) {
			return p.fail(ctx, msg, "admission_protocol_error", fmt.Errorf("inconsistent search admission contract"))
		}
		if err := p.svc.store.mutate(ctx, msg.TenantID, msg.AnalysisID, func(a *AnalysisResult) error {
			a.RetrievalCoverage = retrievalCoverage
			return nil
		}); err != nil {
			return p.fail(ctx, msg, "coverage_store_failed", err)
		}
	}
	docs, missingDates := filterFetchedDocuments(docs, a)
	if missingDates > 0 {
		_ = p.svc.SetWarning(ctx, msg.TenantID, msg.AnalysisID,
			fmt.Sprintf("%d documents excluded: published_at missing or invalid; date coverage incomplete", missingDates))
	}
	if err := p.svc.SaveDocuments(ctx, msg.TenantID, msg.AnalysisID, docs); err != nil {
		return p.fail(ctx, msg, "document_store_failed", err)
	}
	p.setDocCount(ctx, msg, len(docs))
	if retrievalCoverage != nil && len(docs) <= 1 {
		return p.fail(ctx, msg, "insufficient_relevant_evidence", fmt.Errorf("only %d relevant usable documents; full report requires at least 2", len(docs)))
	}
	p.log.Info("pipeline: fetched", slog.String("analysis_id", msg.AnalysisID), slog.Int("docs", len(docs)))

	// ③ 分析（情感/话题/摘要）。只有完整洞察才允许进入 completed；
	// 任何 warning 或缺失结果都失败，采集文档仍保留以便重跑。
	if err := p.step(ctx, msg, StateAnalyzing, progressAnalyze); err != nil {
		return p.fail(ctx, msg, "pipeline_error", err)
	}
	insight, warn := p.runInsight(ctx, msg, docs)
	if p.analyzer != nil && (warn != "" || !insightComplete(insight)) {
		reason := warn
		if reason == "" {
			reason = "insight result incomplete"
		}
		return p.fail(ctx, msg, "insight_failed", errors.New(reason))
	}
	if retrievalCoverage != nil && !topicsGrounded(insight.Topics, docs) {
		return p.fail(ctx, msg, "topic_evidence_invalid", fmt.Errorf("topic references are not grounded in admitted documents"))
	}
	if err := p.svc.SetInsight(ctx, msg.TenantID, msg.AnalysisID, insight); err != nil {
		return p.fail(ctx, msg, "insight_store_failed", err)
	}

	// ④ 报告生成（同样非致命降级）
	if err := p.step(ctx, msg, StateGeneratingReport, progressReport); err != nil {
		return p.fail(ctx, msg, "pipeline_error", err)
	}
	// insightAvailable = 洞察「有产出」而非「零告警」：部分维度失败时
	// 报告仍应基于已有结论撰写（并在报告内说明缺失），而不是整体降级。
	if warn := p.runReport(ctx, msg, a.ReportTemplateID, docs, insight, insightHasOutput(insight)); warn != "" {
		_ = p.svc.SetWarning(ctx, msg.TenantID, msg.AnalysisID, warn)
	}

	// ⑤ 完成
	if err := p.step(ctx, msg, StateCompleted, progressDone); err != nil {
		return p.fail(ctx, msg, "pipeline_error", err)
	}
	completed, err := p.svc.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil {
		return err
	}
	if completed.ReportContent != "" {
		if err := p.ensureReportRecord(ctx, msg, completed); err != nil {
			return err
		}
	}
	p.log.Info("pipeline: completed", slog.String("analysis_id", msg.AnalysisID))
	return nil
}

// topicsGrounded prevents invented topic counts and unrelated document references.
func topicsGrounded(topics []Topic, docs []Document) bool {
	allowed := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		allowed[doc.ID] = struct{}{}
	}
	assigned := make(map[string]struct{})
	for _, topic := range topics {
		if len(topic.DocIDs) == 0 || topic.DocCount != len(topic.DocIDs) {
			return false
		}
		for _, id := range topic.DocIDs {
			if _, ok := allowed[id]; !ok {
				return false
			}
			if _, ok := assigned[id]; ok {
				return false
			}
			assigned[id] = struct{}{}
		}
	}
	return true
}

// filterFetchedDocuments guards the persistence and analysis boundary even if
// the upstream search provider returns results outside the requested scope.
func filterFetchedDocuments(docs []Document, snapshot *AnalysisResult) ([]Document, int) {
	filtered := make([]Document, 0, len(docs))
	missingDates := 0
	for _, doc := range docs {
		if len(snapshot.Sources) > 0 {
			allowed := false
			for _, source := range snapshot.Sources {
				if doc.SourceType == source {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		excluded := false
		content := strings.ToLower(doc.Title + " " + doc.Content)
		for _, word := range snapshot.ExcludeWords {
			if trimmed := strings.TrimSpace(word); trimmed != "" && strings.Contains(content, strings.ToLower(trimmed)) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		if snapshot.DateFrom != "" || snapshot.DateTo != "" {
			published, err := time.Parse(time.RFC3339Nano, doc.PublishedAt)
			if err != nil {
				published, err = time.Parse(time.DateOnly, doc.PublishedAt)
			}
			if err != nil {
				missingDates++
				continue
			}
			day := published.Format("2006-01-02")
			if snapshot.DateFrom != "" && day < snapshot.DateFrom || snapshot.DateTo != "" && day > snapshot.DateTo {
				continue
			}
		}
		filtered = append(filtered, doc)
	}
	return filtered, missingDates
}

// step 推进状态并写进度。上下文超时统一归为 timeout。
func (p *Pipeline) step(ctx context.Context, msg TaskMessage, to State, progress int) error {
	if err := ctx.Err(); err != nil {
		return err // 超时或取消，交由 fail 归类
	}
	return p.svc.advance(ctx, msg.TenantID, msg.AnalysisID, to, progress)
}

// insightHasOutput 判定洞察是否有可保存/可用的产出 —— 哪怕只是部分
// 维度成功。warn 只描述降级程度，不决定「要不要保存」或「洞察是否可用」。
// （P0 修复引入：两处判定必须同进同退，故收敛为单一谓词。）
func insightHasOutput(r InsightResult) bool {
	return r.Summary != "" || len(r.Sentiments) > 0 || len(r.Dimensions) > 0
}

func insightComplete(r InsightResult) bool {
	return r.Summary != "" && len(r.Sentiments) > 0 && r.Warning == ""
}

// runInsight 执行情感/话题/摘要分析。返回 warning 非空表示降级
// （未配置或调用失败），此时 insight 为零值。
func (p *Pipeline) runInsight(ctx context.Context, msg TaskMessage, docs []Document) (InsightResult, string) {
	if p.analyzer == nil {
		return InsightResult{}, "insight engine not configured"
	}
	res, err := p.analyzer.Analyze(ctx, InsightRequest{
		TenantID:     msg.TenantID,
		AnalysisID:   msg.AnalysisID,
		AnalysisType: p.analysisType(ctx, msg),
		Title:        p.analysisName(ctx, msg),
		Documents:    docs,
		Mode:         p.modeForTenant(msg.TenantID),
	})
	if err != nil {
		p.log.Warn("pipeline: insight analysis failed",
			slog.String("analysis_id", msg.AnalysisID), slog.String("err", err.Error()))
		return InsightResult{}, "insight analysis failed: " + err.Error()
	}
	// 引擎侧的部分降级（如某个维度调用失败）也要记为 warning ——
	// 分析成功但结论不全时，用户必须能看到原因。
	return res, res.Warning
}

// runReport 生成报告。warning 非空表示降级（报告未生成）。
// insightAvailable = 洞察步骤是否成功（失败时报告引擎不得渲染 0/0/0）。
func (p *Pipeline) runReport(ctx context.Context, msg TaskMessage, templateID string, docs []Document, insight InsightResult, insightAvailable bool) string {
	if p.generator == nil {
		return "report engine not configured"
	}
	res, err := p.generator.Generate(ctx, ReportRequest{
		TenantID:         msg.TenantID,
		AnalysisID:       msg.AnalysisID,
		TemplateID:       templateID,
		Title:            p.analysisName(ctx, msg) + " 舆情监测报告",
		Documents:        docs,
		Sentiments:       insight.Sentiments,
		Topics:           insight.Topics,
		Dimensions:       insight.Dimensions,
		InsightAvailable: insightAvailable,
	})
	if err != nil {
		p.log.Warn("pipeline: report generation failed",
			slog.String("analysis_id", msg.AnalysisID), slog.String("err", err.Error()))
		return "report generation failed: " + err.Error()
	}
	if err := p.svc.SetReport(ctx, msg.TenantID, msg.AnalysisID, res.ReportID, res.Content); err != nil {
		p.log.Warn("pipeline: store report failed", slog.String("err", err.Error()))
		return "report storage failed: " + err.Error()
	}
	return ""
}

type durableReportService interface {
	CreateFromAnalysisOnce(context.Context, string, string, string, string, string) (string, error)
}

func (p *Pipeline) ensureReportRecord(ctx context.Context, msg TaskMessage, a *AnalysisResult) error {
	if p.reportSvc == nil {
		return nil
	}
	if durable, ok := p.reportSvc.(durableReportService); ok {
		_, err := durable.CreateFromAnalysisOnce(ctx, msg.TenantID, msg.AnalysisID, "html", a.CreatedBy, reportRunKey(a))
		return err
	}
	if _, err := p.reportSvc.CreateFromAnalysis(ctx, msg.TenantID, msg.AnalysisID, "html", a.CreatedBy); err != nil {
		p.log.Warn("pipeline: create report record failed", "analysis_id", msg.AnalysisID, "err", err)
	}
	return nil
}

// createdBy 从 analysis 对象提取 created_by UUID。
func (p *Pipeline) createdBy(ctx context.Context, msg TaskMessage) string {
	a, err := p.svc.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil || a == nil {
		return ""
	}
	return a.CreatedBy
}

// analysisType 读取任务的类型（供分析器提示词使用）。
func (p *Pipeline) analysisType(ctx context.Context, msg TaskMessage) string {
	a, err := p.svc.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil {
		return ""
	}
	return a.AnalysisType
}

// analysisName 读取任务名称（用作报告标题）。
func (p *Pipeline) analysisName(ctx context.Context, msg TaskMessage) string {
	a, err := p.svc.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil {
		return "舆情分析"
	}
	return a.Name
}

// fail 把任务标记为失败并记录错误码。任务已是终态时不做改动（幂等）。
func (p *Pipeline) fail(taskCtx context.Context, msg TaskMessage, code string, cause error) error {
	if errors.Is(taskCtx.Err(), context.Canceled) {
		return taskCtx.Err()
	}
	if errors.Is(cause, context.Canceled) {
		return cause
	}
	// 上下文超时单独归类，便于前端区分「采集失败」与「超时」
	if cause == context.DeadlineExceeded {
		code = "timeout"
	}
	p.log.Warn("pipeline: task failed",
		slog.String("analysis_id", msg.AnalysisID),
		slog.String("code", code),
		slog.String("err", cause.Error()))

	// 用独立上下文：原 ctx 可能已超时，无法再写库
	ctx, cancel := context.WithTimeout(billingpolicy.WithRun(context.Background(), msg.RunID), 5*time.Second)
	defer cancel()
	if err := p.svc.markFailed(ctx, msg.TenantID, msg.AnalysisID, code); err != nil {
		p.log.Error("pipeline: mark failed error",
			slog.String("analysis_id", msg.AnalysisID), slog.String("err", err.Error()))
	}
	return fmt.Errorf("analysis %s failed (%s): %w", msg.AnalysisID, code, cause)
}

func (p *Pipeline) setDocCount(ctx context.Context, msg TaskMessage, n int) {
	_ = p.svc.store.mutate(ctx, msg.TenantID, msg.AnalysisID, func(a *AnalysisResult) error {
		if a.State == StateCanceled {
			return fmt.Errorf("analysis: cannot update canceled task document count")
		}
		a.DocCount = n
		return nil
	})
}

func reportRunKey(a *AnalysisResult) string {
	if a.CurrentRunID != "" {
		return a.CurrentRunID
	}
	return a.StartedAt.UTC().Format(time.RFC3339Nano)
}
