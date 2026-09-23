package analysis

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/business/report"
)

// fakeFetcher 注入采集结果，避免测试依赖真实 Python 引擎。
type fakeFetcher struct {
	docs []Document
	err  error
	// delay 用于触发超时场景
	delay time.Duration
	// 记录收到的请求，验证参数透传
	gotReq FetchRequest
	calls  int
}

func (f *fakeFetcher) Fetch(ctx context.Context, req FetchRequest) ([]Document, error) {
	f.calls++
	f.gotReq = req
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.docs, f.err
}

func newTestPipeline(t *testing.T, fetcher Fetcher, timeout time.Duration) (*Pipeline, *Service) {
	t.Helper()
	svc, _ := newTestAnalysisService(t)
	p := NewPipeline(svc, fetcher, timeout, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return p, svc
}

// newTestPipelineWithReportSvc creates a pipeline with report service injected.
func newTestPipelineWithReportSvc(t *testing.T, fetcher Fetcher, reportSvc reportSvc) (*Pipeline, *Service) {
	t.Helper()
	svc, _ := newTestAnalysisService(t)
	p := NewPipeline(svc, fetcher, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// fakeGenerator 已在 insight_test.go 定义，此处复用
	p = p.WithGenerator(&fakeGenerator{res: ReportResult{
		ReportID: "gen-test",
		Content:  "<h1>测试报告</h1>",
	}})
	p = p.WithReportSvc(reportSvc)
	return p, svc
}

func sampleDocs(n int) []Document {
	docs := make([]Document, n)
	for i := range docs {
		docs[i] = Document{
			ID:         "doc-" + string(rune('a'+i)),
			Title:      "测试文档",
			URL:        "https://example.com/" + string(rune('a'+i)),
			Content:    "内容",
			SourceType: "news",
			SourceName: "新闻",
		}
	}
	return docs
}

// ── 正常流程 ────────────────────────────────────────────

func TestPipeline_completesTaskWithDocuments(t *testing.T) {
	fetcher := &fakeFetcher{docs: sampleDocs(3)}
	p, svc := newTestPipeline(t, fetcher, 5*time.Second)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateAnalysisRequest{
		TenantID: "t1", Name: "雅阁后排监测",
		Keywords: []string{"雅阁后排"}, Sources: []string{"news"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	got, err := svc.Get(ctx, "t1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCompleted {
		t.Errorf("state = %q, want completed", got.State)
	}
	if got.Progress != 100 {
		t.Errorf("progress = %d, want 100", got.Progress)
	}
	if got.DocCount != 3 {
		t.Errorf("doc_count = %d, want 3", got.DocCount)
	}
	if !got.FinishedAt.After(time.Time{}) {
		t.Error("finished_at should be set")
	}
	if got.ErrorCode != "" {
		t.Errorf("error_code = %q, want empty", got.ErrorCode)
	}

	// 文档应可读回
	docs := svc.Documents(ctx, "t1", created.ID)
	if len(docs) != 3 {
		t.Errorf("stored docs = %d, want 3", len(docs))
	}
}

func TestPipeline_passesParamsToFetcher(t *testing.T) {
	fetcher := &fakeFetcher{}
	p, svc := newTestPipeline(t, fetcher, 5*time.Second)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{
		TenantID: "t1", Name: "测试",
		Keywords: []string{"雅阁", "本田"}, Sources: []string{"weibo", "news"},
	})
	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err != nil {
		t.Fatal(err)
	}

	if fetcher.calls != 1 {
		t.Fatalf("fetcher calls = %d, want 1", fetcher.calls)
	}
	if len(fetcher.gotReq.Keywords) != 2 || fetcher.gotReq.Keywords[0] != "雅阁" {
		t.Errorf("keywords = %v, want [雅阁 本田]", fetcher.gotReq.Keywords)
	}
	if len(fetcher.gotReq.Sources) != 2 {
		t.Errorf("sources = %v, want 2 items", fetcher.gotReq.Sources)
	}
	if fetcher.gotReq.TenantID != "t1" || fetcher.gotReq.AnalysisID != created.ID {
		t.Errorf("tenant/analysis id not forwarded: %+v", fetcher.gotReq)
	}
}

// ── 失败路径 ────────────────────────────────────────────

func TestPipeline_fetchErrorMarksFailed(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("bocha api unreachable")}
	p, svc := newTestPipeline(t, fetcher, 5*time.Second)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err == nil {
		t.Fatal("Handle should return error when fetch fails")
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.State != StateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
	if got.ErrorCode != "fetch_failed" {
		t.Errorf("error_code = %q, want fetch_failed", got.ErrorCode)
	}
	if !got.FinishedAt.After(time.Time{}) {
		t.Error("finished_at should be set on failure")
	}
}

func TestPipeline_timeoutMarksFailedWithTimeoutCode(t *testing.T) {
	// 采集耗时超过管线超时 → 应归类为 timeout 而非 fetch_failed
	fetcher := &fakeFetcher{delay: 2 * time.Second}
	p, svc := newTestPipeline(t, fetcher, 200*time.Millisecond)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err == nil {
		t.Fatal("Handle should return error on timeout")
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.State != StateFailed {
		t.Errorf("state = %q, want failed", got.State)
	}
	if got.ErrorCode != "timeout" {
		t.Errorf("error_code = %q, want timeout", got.ErrorCode)
	}
}

func TestPipeline_terminalTaskIsSkipped(t *testing.T) {
	fetcher := &fakeFetcher{docs: sampleDocs(1)}
	p, svc := newTestPipeline(t, fetcher, 5*time.Second)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err := svc.Cancel(ctx, "t1", created.ID); err != nil {
		t.Fatal(err)
	}

	// 重复投递已取消的任务：应静默跳过，不改状态、不调采集
	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err != nil {
		t.Errorf("Handle on terminal task should not error, got %v", err)
	}
	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.State != StateCanceled {
		t.Errorf("state = %q, want canceled (unchanged)", got.State)
	}
	if fetcher.calls != 0 {
		t.Errorf("fetcher should not be called for terminal task, calls = %d", fetcher.calls)
	}
}

// ── 输入校验 ────────────────────────────────────────────

func TestPipeline_rejectsIncompleteMessage(t *testing.T) {
	p, _ := newTestPipeline(t, &fakeFetcher{}, time.Second)
	ctx := context.Background()

	cases := []struct {
		name string
		msg  TaskMessage
	}{
		{"缺 analysis_id", TaskMessage{TenantID: "t1"}},
		{"缺 tenant_id", TaskMessage{AnalysisID: "a1"}},
		{"全空", TaskMessage{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.Handle(ctx, tc.msg); err == nil {
				t.Error("expected error for incomplete message")
			}
		})
	}
}

// ── 报告记录创建（P0：报告中心闭环）─────────────────────

// fakeReportSvc 记录 CreateFromAnalysis 调用。
type fakeReportSvc struct {
	mu      sync.Mutex
	calls   []struct{ tenantID, analysisID, format, createdBy string }
	records map[string]*report.Report // keyed by analysisID, stores created reports
}

func newFakeReportSvc() *fakeReportSvc {
	return &fakeReportSvc{records: make(map[string]*report.Report)}
}

func (f *fakeReportSvc) CreateFromAnalysis(ctx context.Context, tenantID, analysisID, format, createdBy string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct{ tenantID, analysisID, format, createdBy string}{tenantID, analysisID, format, createdBy})
	return "report-" + analysisID, nil
}

// TestPipeline_createsReportRecordAfterCompletion 是报告中心闭环的 TDD 测试。
// RED：analyses 已含 report_content，但 reports 表从未写入 → 当前 reports 表行数 = 0。
// GREEN：管线完成后调 CreateFromAnalysis，reports 表新增 1 行。
func TestPipeline_createsReportRecordAfterCompletion(t *testing.T) {
	fetcher := &fakeFetcher{docs: sampleDocs(3)}
	reportSvc := newFakeReportSvc()
	p, svc := newTestPipelineWithReportSvc(t, fetcher, reportSvc)

	ctx := context.Background()
	created, err := svc.Create(ctx, CreateAnalysisRequest{
		TenantID: "t_report", UserID: "user-123",
		Name: "报告中心测试", Keywords: []string{"测试"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t_report"}); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// RED 断言：管线完成后 reports 表应有 1 条记录（当前 = 0，会失败）
	reportSvc.mu.Lock()
	gotCalls := len(reportSvc.calls)
	reportSvc.mu.Unlock()
	if gotCalls == 0 {
		t.Errorf("REPORT RECORD: reports 表行数 = 0（从未写入）；want ≥ 1 — 管线 runReport 尚未调 CreateFromAnalysis")
	}

	// GREEN 断言：调用的参数正确
	if gotCalls > 0 {
		reportSvc.mu.Lock()
		call := reportSvc.calls[0]
		reportSvc.mu.Unlock()
		if call.tenantID != "t_report" {
			t.Errorf("tenantID = %q, want t_report", call.tenantID)
		}
		if call.analysisID != created.ID {
			t.Errorf("analysisID = %q, want %s", call.analysisID, created.ID)
		}
		if call.format != "html" {
			t.Errorf("format = %q, want html", call.format)
		}
		if call.createdBy != "user-123" {
			t.Errorf("createdBy = %q, want user-123", call.createdBy)
		}
	}
}

// TestPipeline_createsNewReportOnRerun 是 Rerun 多报告策略的 TDD 测试。
// RED：Rerun 后同一 analysis_id 仍只有 1 条报告记录（覆盖逻辑或未新建）。
// GREEN：Rerun 后 reports 表同一 analysis_id 有 2 条记录（版本 1 和 2）。
func TestPipeline_createsNewReportOnRerun(t *testing.T) {
	reportSvc := newFakeReportSvc()
	fetcher := &fakeFetcher{docs: sampleDocs(2)}
	p, svc := newTestPipelineWithReportSvc(t, fetcher, reportSvc)

	ctx := context.Background()
	created, _ := svc.Create(ctx, CreateAnalysisRequest{
		TenantID: "t_rerun", UserID: "user-456",
		Name: "Rerun 测试", Keywords: []string{"测试"},
	})

	// 第一次分析
	p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t_rerun"})

	// Rerun：发布新消息，同一 pipeline 处理
	_ = svc.Rerun(ctx, "t_rerun", created.ID)
	p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t_rerun"})

	reportSvc.mu.Lock()
	gotCalls := len(reportSvc.calls)
	reportSvc.mu.Unlock()

	// RED 断言：同一 analysis_id 应有 2 条报告记录（当前 = 1，不会为 Rerun 新建）
	if gotCalls < 2 {
		t.Errorf("REPORT RECORD (RERUN): 同一 analysis_id 报告数 = %d；want 2 — Rerun 应新建报告记录而非覆盖", gotCalls)
	}
}

// ── 消息编解码 ──────────────────────────────────────────

func TestTaskMessage_roundTrip(t *testing.T) {
	orig := TaskMessage{AnalysisID: "01ABC", TenantID: "01XYZ"}
	body, err := orig.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTaskMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	if got != orig {
		t.Errorf("round trip = %+v, want %+v", got, orig)
	}
}

func TestDecodeTaskMessage_compatWithBareID(t *testing.T) {
	// 兼容早期只投递裸 ID 的消息格式
	got, err := DecodeTaskMessage([]byte("01OLDFORMAT"))
	if err != nil {
		t.Fatalf("bare id should decode: %v", err)
	}
	if got.AnalysisID != "01OLDFORMAT" {
		t.Errorf("analysis_id = %q, want 01OLDFORMAT", got.AnalysisID)
	}
	// 裸 ID 无租户信息，管线会拒绝 —— 这是预期行为
	if got.TenantID != "" {
		t.Errorf("tenant_id should be empty for bare id, got %q", got.TenantID)
	}
}

func TestDecodeTaskMessage_rejectsMalformed(t *testing.T) {
	if _, err := DecodeTaskMessage([]byte("{not json")); err == nil {
		t.Error("malformed JSON should error")
	}
	if _, err := DecodeTaskMessage([]byte("")); err == nil {
		t.Error("empty body should error")
	}
}

// ── 回归：早期只发裸 ID 导致任务永卡 queued ──────────────

func TestCreate_publishesMessageWithTenantID(t *testing.T) {
	svc, q := newTestAnalysisService(t)
	ch := subscribeTasks(t, q)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}

	body := nextTask(t, ch)
	msg, err := DecodeTaskMessage([]byte(body))
	if err != nil {
		t.Fatalf("published payload is not a valid task message: %v\npayload=%q", err, body)
	}
	if msg.AnalysisID != created.ID {
		t.Errorf("analysis_id = %q, want %q", msg.AnalysisID, created.ID)
	}
	if msg.TenantID != "t1" {
		t.Errorf("tenant_id = %q, want t1（缺租户会导致管线无法定位任务）", msg.TenantID)
	}
	if strings.Contains(body, "{") == false {
		t.Errorf("payload should be JSON, got %q", body)
	}
}
