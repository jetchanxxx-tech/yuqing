package analysis

import (
	"context"
	"errors"
	"testing"
)

// TestService_setInsightStoresAnalysisResults 覆盖洞察结果的写入与读回。
func TestService_setInsightStoresAnalysisResults(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}

	insight := InsightResult{
		Summary: "舆情总体可控",
		Sentiments: []Sentiment{
			{DocumentID: "d1", Sentiment: "negative", Score: 0.8},
			{DocumentID: "d2", Sentiment: "positive", Score: 0.9},
		},
		Topics: []Topic{
			{ID: "t1", Name: "后排空间", Keywords: []string{"后排"}, DocCount: 2, Trend: "rising"},
		},
	}
	if err := svc.SetInsight(ctx, "t1", created.ID, insight); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(ctx, "t1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "舆情总体可控" {
		t.Errorf("summary = %q", got.Summary)
	}
	if len(got.Sentiments) != 2 || got.Sentiments[0].Sentiment != "negative" {
		t.Errorf("sentiments = %+v", got.Sentiments)
	}
	if len(got.Topics) != 1 || got.Topics[0].Name != "后排空间" || got.Topics[0].DocCount != 2 {
		t.Errorf("topics = %+v", got.Topics)
	}
}

// TestService_setReportStoresContent 覆盖报告内容的写入与读回。
func TestService_setReportStoresContent(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})

	if err := svc.SetReport(ctx, "t1", created.ID, "rep-1", "<html>报告</html>"); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.ReportID != "rep-1" {
		t.Errorf("report_id = %q", got.ReportID)
	}
	if got.ReportContent != "<html>报告</html>" {
		t.Errorf("report_content = %q", got.ReportContent)
	}
}

// TestService_setWarningStoresWarning 覆盖降级警告的写入与读回。
func TestService_setWarningStoresWarning(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})

	if err := svc.SetWarning(ctx, "t1", created.ID, "insight engine not configured"); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.Warning != "insight engine not configured" {
		t.Errorf("warning = %q", got.Warning)
	}
}

// 多条降级原因必须**追加**而非覆盖 —— 洞察失败后报告再失败，
// 两条原因都应保留（审核发现 D2）。
func TestService_setWarningAppendsMultipleReasons(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})

	if err := svc.SetWarning(ctx, "t1", created.ID, "insight analysis failed: llm down"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWarning(ctx, "t1", created.ID, "report generation failed: timeout"); err != nil {
		t.Fatal(err)
	}
	// 相同原因重复写入不重复追加
	if err := svc.SetWarning(ctx, "t1", created.ID, "report generation failed: timeout"); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	want := "insight analysis failed: llm down；report generation failed: timeout"
	if got.Warning != want {
		t.Errorf("warning = %q, want %q", got.Warning, want)
	}
}

// ── 管线：分析步骤 ───────────────────────────────────────

type fakeAnalyzer struct {
	res    InsightResult
	err    error
	gotReq InsightRequest
	calls  int
}

func (f *fakeAnalyzer) Analyze(ctx context.Context, req InsightRequest) (InsightResult, error) {
	f.calls++
	f.gotReq = req
	return f.res, f.err
}

type fakeGenerator struct {
	res    ReportResult
	err    error
	gotReq ReportRequest
	calls  int
}

func (f *fakeGenerator) Generate(ctx context.Context, req ReportRequest) (ReportResult, error) {
	f.calls++
	f.gotReq = req
	return f.res, f.err
}

// 分析成功：任务 completed，洞察与报告写入 store。
func TestPipeline_analyzeAndReportPopulateResults(t *testing.T) {
	fetcher := &fakeFetcher{docs: sampleDocs(2)}
	analyzer := &fakeAnalyzer{res: InsightResult{
		Summary: "总体可控",
		Sentiments: []Sentiment{
			{DocumentID: "doc-a", Sentiment: "negative", Score: 0.7},
			{DocumentID: "doc-b", Sentiment: "positive", Score: 0.8},
		},
		Topics:     []Topic{{ID: "t1", Name: "后排空间", DocCount: 2}},
		Dimensions: sampleDimensions(),
	}}
	generator := &fakeGenerator{res: ReportResult{ReportID: "rep-1", Content: "<html>x</html>"}}

	p, svc := newTestPipeline(t, fetcher, 5e9)
	p = p.WithAnalyzer(analyzer).WithGenerator(generator)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{
		TenantID: "t1", Name: "雅阁", AnalysisType: "brand",
		Keywords: []string{"雅阁"}, Sources: []string{"news"},
	})
	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.State != StateCompleted {
		t.Fatalf("state = %q, want completed (warning=%q)", got.State, got.Warning)
	}
	if got.Summary != "总体可控" {
		t.Errorf("summary = %q", got.Summary)
	}
	if len(got.Sentiments) != 2 {
		t.Errorf("sentiments = %+v", got.Sentiments)
	}
	if got.ReportID != "rep-1" || got.ReportContent != "<html>x</html>" {
		t.Errorf("report = %q / %q", got.ReportID, got.ReportContent)
	}
	if got.Warning != "" {
		t.Errorf("warning = %q, want empty", got.Warning)
	}

	// analyzer 收到的请求应含文档与参数
	if analyzer.calls != 1 || len(analyzer.gotReq.Documents) != 2 {
		t.Errorf("analyzer got %d calls, %d docs", analyzer.calls, len(analyzer.gotReq.Documents))
	}
	if analyzer.gotReq.AnalysisType != "brand" || analyzer.gotReq.AnalysisID != created.ID {
		t.Errorf("analyzer req = %+v", analyzer.gotReq)
	}
	if generator.calls != 1 || len(generator.gotReq.Documents) != 2 {
		t.Errorf("generator got %d calls, %d docs", generator.calls, len(generator.gotReq.Documents))
	}
	// 洞察成功 → 报告引擎被告知洞察可用（审核 D1：防止渲染 0/0/0 误导）
	if !generator.gotReq.InsightAvailable {
		t.Error("InsightAvailable should be true when insight succeeded")
	}
	// 五维度结论必须随请求透传给报告引擎 —— 报告要基于维度研判撰写，
	// 而不是从聚合 JSON 重新概括（这正是此前结论干瘪的根因）。
	if len(generator.gotReq.Dimensions) != 2 {
		t.Fatalf("generator dimensions = %d, want 2", len(generator.gotReq.Dimensions))
	}
	if generator.gotReq.Dimensions[0].ID != "background" {
		t.Errorf("generator dim[0] = %q", generator.gotReq.Dimensions[0].ID)
	}
	// 维度结论也要落库供 /result 展示
	if len(got.Dimensions) != 2 || got.Dimensions[0].Name != "背景与事件概述" {
		t.Errorf("stored dimensions = %+v", got.Dimensions)
	}
}

// 分析失败不致命：任务仍 completed，但记录 warning。
func TestPipeline_analyzeFailureRecordsWarningNotFailed(t *testing.T) {
	fetcher := &fakeFetcher{docs: sampleDocs(1)}
	analyzer := &fakeAnalyzer{err: errors.New("llm unavailable")}
	generator := &fakeGenerator{res: ReportResult{ReportID: "rep-1", Content: "<html>x</html>"}}

	p, svc := newTestPipeline(t, fetcher, 5e9)
	p = p.WithAnalyzer(analyzer).WithGenerator(generator)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.State != StateCompleted {
		t.Fatalf("state = %q, want completed", got.State)
	}
	if got.Warning == "" {
		t.Error("warning should be recorded when analysis fails")
	}
	if got.ErrorCode != "" {
		t.Errorf("error_code = %q, want empty (non-fatal)", got.ErrorCode)
	}
	// 洞察失败 → 报告引擎被告知洞察不可用，避免渲染 0/0/0 误导
	if generator.gotReq.InsightAvailable {
		t.Error("InsightAvailable should be false when insight failed")
	}
}

// 未配置 analyzer：任务完成并提示未配置（本地开发/测试环境）。
func TestPipeline_nilAnalyzerRecordsConfigureWarning(t *testing.T) {
	fetcher := &fakeFetcher{docs: sampleDocs(1)}

	p, svc := newTestPipeline(t, fetcher, 5e9)
	ctx := context.Background()

	created, _ := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err := p.Handle(ctx, TaskMessage{AnalysisID: created.ID, TenantID: "t1"}); err != nil {
		t.Fatal(err)
	}

	got, _ := svc.Get(ctx, "t1", created.ID)
	if got.State != StateCompleted {
		t.Fatalf("state = %q, want completed", got.State)
	}
	if got.Warning == "" {
		t.Error("warning should mention engine not configured")
	}
}

// ── 五维度分析（BettaFish 内核移植）──────────────────────────
//
// 现状：分析只有「情感+话题+摘要」三个聚合字段，单轮单视角 → 结论干瘪。
// 目标：分析引擎产出五个维度的独立研判（背景/热度/情感观点/群体差异/深层原因），
// 每维度带骨架字段，并由管线透传到报告引擎与 /result。

func sampleDimensions() []Dimension {
	return []Dimension{
		{
			ID: "background", Name: "背景与事件概述",
			Findings:   "核心发现：争议由一条实测视频引爆。",
			DataPoints: []string{"4 篇文档中 3 篇发布于 9 月上旬"},
			Quotes:     []Quote{{Text: "腿都伸不直", Source: "微博"}},
			DeepRead:   "深入解读：产品定位与用户预期错位。",
			Trend:      "趋势：官方回应后热度回落。",
		},
		{
			ID: "heat", Name: "热度与传播路径",
			Findings:   "核心发现：微博是主扩散场。",
			DataPoints: []string{"微博来源占 25%"},
			Quotes:     []Quote{{Text: "转发了", Source: "微博"}},
			DeepRead:   "深入解读：二次转载放大了争议。",
			Trend:      "趋势：仍在扩散。",
		},
	}
}

func TestService_setInsightStoresDimensions(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	ctx := context.Background()

	created, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: "t1", Name: "测试"})
	if err != nil {
		t.Fatal(err)
	}

	dims := sampleDimensions()
	if err := svc.SetInsight(ctx, "t1", created.ID, InsightResult{
		Summary: "总体可控", Dimensions: dims,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(ctx, "t1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dimensions) != len(dims) {
		t.Fatalf("dimensions = %d, want %d", len(got.Dimensions), len(dims))
	}
	first := got.Dimensions[0]
	if first.ID != "background" || first.Name != "背景与事件概述" {
		t.Errorf("dim[0] identity = %q/%q", first.ID, first.Name)
	}
	if first.Findings == "" || first.DeepRead == "" || first.Trend == "" {
		t.Errorf("dim[0] 骨架字段缺失: %+v", first)
	}
	if len(first.DataPoints) != 1 || len(first.Quotes) != 1 {
		t.Errorf("dim[0] 数据点/原声缺失: %+v", first)
	}
	if first.Quotes[0].Text != "腿都伸不直" || first.Quotes[0].Source != "微博" {
		t.Errorf("dim[0] 原声未保真: %+v", first.Quotes[0])
	}
}
