package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/yuging/platform/internal/business/analysis"
	"github.com/yuging/platform/internal/engine"
	"github.com/yuging/platform/internal/pkg/queue"
)

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

// startPipeline 在 server 进程内消费 analysis.tasks 并推进任务状态机。
//
// 为什么必须在同一进程：内存 store 与内存 queue 都是进程私有的。
// 独立 worker 进程既收不到 server 发布的消息，也看不到 server 创建的任务
// （实测：worker 运行数小时，收到任务数为 0，任务永久停在 queued）。
//
// 接入 PostgreSQL store + 远程队列（Redis/RabbitMQ）后，本管线可搬回
// 独立 worker 进程，届时无需改动管线自身。
func startPipeline(
	ctx context.Context,
	q queue.Queue,
	svc *analysis.Service,
	crawler *engine.RealCrawlerEngine,
	timeout time.Duration,
	log *slog.Logger,
) *analysis.Pipeline {
	p := analysis.NewPipeline(svc, &engineFetcher{crawler: crawler}, timeout, log)

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
