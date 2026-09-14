package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/engine"
	"github.com/yuqing/platform/internal/pkg/queue"
)

// pipelineBudget 计算整条管线的总预算：采集 + 分析 + 报告各阶段的超时之和。
//
// 不能只取 engines.query.timeout —— 那是「单个采集请求」的预算。管线依次执行
// 采集（Bocha 搜索 + Scrapling 串行抓取）、洞察（2 次 LLM 调用）、报告（1 次 LLM 调用），
// 三个阶段共用同一份 deadline；沿用单阶段预算会让慢分析在报告前耗尽 deadline，
// 使 Pipeline.step 拿到 ctx.Err() 并把任务判为 failed(timeout)，
// 与「采集结果不能因分析失败丢弃」的设计相悖。
//
// 未接线（URL 为空）的引擎不计入；返回 0 表示无有效配置，
// 由 NewPipeline 兜底为默认 3 分钟。
func pipelineBudget(cfg *config.Config) time.Duration {
	total := time.Duration(0)
	add := func(raw string, wired bool) {
		if !wired {
			return
		}
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			total += d
		}
	}
	add(cfg.Engines.Query.Timeout, cfg.Engines.Query.URL != "")
	add(cfg.Engines.Insight.Timeout, cfg.Engines.Insight.URL != "")
	add(cfg.Engines.Report.Timeout, cfg.Engines.Report.URL != "")
	return total
}

// engineFetcher 把引擎客户端适配为 analysis.Fetcher，
// 并完成 engine.Document → analysis.Document 的类型转换。
type engineFetcher struct {
	crawler *engine.RealCrawlerEngine
}

func (f *engineFetcher) Fetch(ctx context.Context, req analysis.FetchRequest) ([]analysis.Document, error) {
	docs, err := f.crawler.Search(ctx, &engine.CrawlReq{
		Keywords:   req.Keywords,
		Sources:    req.Sources,
		AnalysisID: req.AnalysisID,
		MaxDepth:   req.MaxResults,
	})
	if err != nil {
		return nil, err
	}
	out := make([]analysis.Document, 0, len(docs))
	for _, d := range docs {
		out = append(out, analysis.Document{
			ID:          d.ID,
			Title:       d.Title,
			URL:         d.URL,
			Content:     d.Content,
			Author:      d.Author,
			SourceType:  d.SourceType,
			SourceName:  d.SourceName,
			PublishedAt: d.PublishedAt,
			ContentHash: d.ContentHash,
		})
	}
	return out, nil
}

// engineInsightAdapter 把引擎客户端适配为 analysis.InsightAnalyzer。
type engineInsightAdapter struct {
	ins *engine.RealInsightEngine
}

func (a *engineInsightAdapter) Analyze(ctx context.Context, req analysis.InsightRequest) (analysis.InsightResult, error) {
	docs := toEngineDocuments(req.Documents)
	resp, err := a.ins.Analyze(ctx, &engine.InsightAnalyzeReq{
		Documents:    docs,
		AnalysisID:   req.AnalysisID,
		AnalysisType: req.AnalysisType,
		Title:        req.Title,
	})
	if err != nil {
		return analysis.InsightResult{}, err
	}
	out := analysis.InsightResult{
		Summary:    resp.Summary,
		Warning:    resp.Warning,
		Sentiments: make([]analysis.Sentiment, 0, len(resp.Sentiments)),
		Topics:     make([]analysis.Topic, 0, len(resp.Topics)),
		Dimensions: make([]analysis.Dimension, 0, len(resp.Dimensions)),
	}
	for _, s := range resp.Sentiments {
		out.Sentiments = append(out.Sentiments, analysis.Sentiment{
			DocumentID: s.DocumentID, Sentiment: s.Sentiment,
			Level: s.Level, Confidence: s.Confidence, Score: s.Score,
		})
	}
	for _, t := range resp.Topics {
		out.Topics = append(out.Topics, analysis.Topic{
			ID: t.ID, Name: t.Name, Keywords: t.Keywords, DocCount: t.DocCount, Trend: t.Trend,
		})
	}
	for _, d := range resp.Dimensions {
		out.Dimensions = append(out.Dimensions, toAnalysisDimension(d))
	}
	return out, nil
}

// toAnalysisDimension 转换维度结果；原声切片始终非 nil（前端 .map() 遇 null 会崩）。
func toAnalysisDimension(d engine.DimensionResult) analysis.Dimension {
	quotes := make([]analysis.Quote, 0, len(d.Quotes))
	for _, q := range d.Quotes {
		quotes = append(quotes, analysis.Quote{Text: q.Text, Source: q.Source})
	}
	return analysis.Dimension{
		ID:         d.ID,
		Name:       d.Name,
		Findings:   d.Findings,
		DataPoints: d.DataPoints,
		Quotes:     quotes,
		DeepRead:   d.DeepRead,
		Trend:      d.Trend,
	}
}

// toEngineDimensions 把维度结论回传给报告引擎。
func toEngineDimensions(dims []analysis.Dimension) []engine.DimensionResult {
	out := make([]engine.DimensionResult, 0, len(dims))
	for _, d := range dims {
		quotes := make([]engine.QuoteResult, 0, len(d.Quotes))
		for _, q := range d.Quotes {
			quotes = append(quotes, engine.QuoteResult{Text: q.Text, Source: q.Source})
		}
		out = append(out, engine.DimensionResult{
			ID: d.ID, Name: d.Name, Findings: d.Findings,
			DataPoints: d.DataPoints, Quotes: quotes,
			DeepRead: d.DeepRead, Trend: d.Trend,
		})
	}
	return out
}

// engineReportAdapter 把引擎客户端适配为 analysis.ReportGenerator。
type engineReportAdapter struct {
	rep *engine.RealReportEngine
}

func (a *engineReportAdapter) Generate(ctx context.Context, req analysis.ReportRequest) (analysis.ReportResult, error) {
	resp, err := a.rep.Generate(ctx, &engine.ReportGenerateReq{
		Title:            req.Title,
		Format:           "html",
		Documents:        toEngineDocuments(req.Documents),
		Sentiments:       toEngineSentiments(req.Sentiments),
		Topics:           toEngineTopics(req.Topics),
		Dimensions:       toEngineDimensions(req.Dimensions),
		AnalysisID:       req.AnalysisID,
		InsightAvailable: req.InsightAvailable,
	})
	if err != nil {
		return analysis.ReportResult{}, err
	}
	return analysis.ReportResult{ReportID: resp.ReportID, Content: resp.Content}, nil
}

func toEngineDocuments(docs []analysis.Document) []engine.Document {
	out := make([]engine.Document, 0, len(docs))
	for _, d := range docs {
		out = append(out, engine.Document{
			ID: d.ID, Title: d.Title, URL: d.URL, Content: d.Content,
			Author: d.Author, SourceType: d.SourceType, SourceName: d.SourceName,
			PublishedAt: d.PublishedAt, ContentHash: d.ContentHash,
		})
	}
	return out
}

func toEngineSentiments(items []analysis.Sentiment) []engine.SentimentResult {
	out := make([]engine.SentimentResult, 0, len(items))
	for _, s := range items {
		out = append(out, engine.SentimentResult{
			DocumentID: s.DocumentID, Sentiment: s.Sentiment,
			Level: s.Level, Confidence: s.Confidence, Score: s.Score,
		})
	}
	return out
}

func toEngineTopics(items []analysis.Topic) []engine.TopicResult {
	out := make([]engine.TopicResult, 0, len(items))
	for _, t := range items {
		out = append(out, engine.TopicResult{ID: t.ID, Name: t.Name, Keywords: t.Keywords, DocCount: t.DocCount, Trend: t.Trend})
	}
	return out
}

// startPipeline 在 server 进程内消费 analysis.tasks 并推进任务状态机。
//
// 为什么必须在同一进程：内存 store 与内存 queue 都是进程私有的。
// 独立 worker 进程既收不到 server 发布的消息，也看不到 server 创建的任务
// （实测：worker 运行数小时，收到任务数为 0，任务永久停在 queued）。
//
// 接入 PostgreSQL store + 远程队列（Redis/RabbitMQ）后，本管线可搬回
// 独立 worker 进程，届时无需改动管线自身。
//
// insight/report 传 nil 时对应步骤跳过并记录 warning（本地开发未配引擎）。
func startPipeline(
	ctx context.Context,
	q queue.Queue,
	svc *analysis.Service,
	crawler *engine.RealCrawlerEngine,
	insight *engine.RealInsightEngine,
	report *engine.RealReportEngine,
	timeout time.Duration,
	log *slog.Logger,
) *analysis.Pipeline {
	p := analysis.NewPipeline(svc, &engineFetcher{crawler: crawler}, timeout, log)
	if insight != nil {
		p = p.WithAnalyzer(&engineInsightAdapter{ins: insight})
	}
	if report != nil {
		p = p.WithGenerator(&engineReportAdapter{rep: report})
	}

	err := q.Subscribe(ctx, analysis.TopicAnalysisTasks, func(ctx context.Context, msg queue.Message) error {
		task, err := analysis.DecodeTaskMessage(msg.Body)
		if err != nil {
			log.Warn("pipeline: undecodable task message",
				slog.String("err", err.Error()), slog.String("body", string(msg.Body)))
			return nil // 丢弃坏消息，避免无限重试
		}
		if task.TenantID == "" {
			// 历史格式（裸 ID）无法定位租户，明确记录而非静默失败
			log.Warn("pipeline: task message missing tenant_id, skipped",
				slog.String("analysis_id", task.AnalysisID))
			return nil
		}
		if err := p.Handle(ctx, task); err != nil {
			log.Warn("pipeline: task did not complete",
				slog.String("analysis_id", task.AnalysisID), slog.String("err", err.Error()))
		}
		return nil
	})
	if err != nil {
		log.Error("pipeline: failed to subscribe analysis.tasks", slog.String("err", err.Error()))
		return p
	}
	log.Info("pipeline: subscribed", slog.String("topic", analysis.TopicAnalysisTasks))
	return p
}
