package analysis

import (
	"context"
	"log/slog"
	"sync"
)

// Document 是采集到的原始内容（由 query 引擎返回）。
type Document struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Content     string `json:"content"`
	Author      string `json:"author,omitempty"`
	SourceType  string `json:"source_type"`
	SourceName  string `json:"source_name"`
	PublishedAt string `json:"published_at,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

// documentStore 是采集文档的持久化契约：内存实现 memoryDocumentStore，
// PostgreSQL 实现 pgDocumentStore（documents_pg.go）。
//
// 方法带 error 是为了让 pg 实现上报数据库故障 —— 管线调用的是 Service 上
// 签名不变的公开方法（AddDocuments/Documents/DocumentCount），失败在那里
// 记日志并降级为空结果，调用方（pipeline/API）无需改动。
type documentStore interface {
	add(ctx context.Context, tenantID, analysisID string, docs []Document) error
	list(ctx context.Context, tenantID, analysisID string) ([]Document, error)
	count(ctx context.Context, tenantID, analysisID string) (int, error)
}

// memoryDocumentStore 按租户+分析分桶保存采集结果（内存实现，重启即失）。
type memoryDocumentStore struct {
	mu   sync.RWMutex
	data map[string]map[string][]Document // tenantID → analysisID → docs
}

var _ documentStore = (*memoryDocumentStore)(nil)

func newMemoryDocumentStore() *memoryDocumentStore {
	return &memoryDocumentStore{data: make(map[string]map[string][]Document)}
}

func (d *memoryDocumentStore) add(_ context.Context, tenantID, analysisID string, docs []Document) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.data[tenantID] == nil {
		d.data[tenantID] = make(map[string][]Document)
	}
	d.data[tenantID][analysisID] = append(d.data[tenantID][analysisID], docs...)
	return nil
}

func (d *memoryDocumentStore) list(_ context.Context, tenantID, analysisID string) ([]Document, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	src := d.data[tenantID][analysisID]
	out := make([]Document, len(src))
	copy(out, src) // 返回副本，避免调用方改动内部状态
	return out, nil
}

func (d *memoryDocumentStore) count(_ context.Context, tenantID, analysisID string) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.data[tenantID][analysisID]), nil
}

// ── Service 上的公开方法 ─────────────────────────────────

// AddDocuments 由管线写入采集结果。
// 写入失败只记日志：任务此时已推进到下一步，且采集结果丢失属于可观测的
// 降级（/result 少文档），不应让整条管线失败。
func (s *Service) AddDocuments(ctx context.Context, tenantID, analysisID string, docs []Document) {
	if err := s.docs.add(ctx, tenantID, analysisID, docs); err != nil {
		slog.Default().Warn("analysis: 采集文档写入失败",
			slog.String("tenant_id", tenantID),
			slog.String("analysis_id", analysisID),
			slog.String("err", err.Error()))
	}
}

// Documents 返回某次分析采集到的全部文档（无结果时返回空切片而非 nil，
// 保证 JSON 序列化为 [] 而不是 null）。
func (s *Service) Documents(ctx context.Context, tenantID, analysisID string) []Document {
	docs, err := s.docs.list(ctx, tenantID, analysisID)
	if err != nil {
		slog.Default().Warn("analysis: 采集文档读取失败",
			slog.String("tenant_id", tenantID),
			slog.String("analysis_id", analysisID),
			slog.String("err", err.Error()))
		return []Document{}
	}
	if docs == nil {
		return []Document{}
	}
	return docs
}

// DocumentCount 返回采集文档数（供 /result 汇总）。
func (s *Service) DocumentCount(ctx context.Context, tenantID, analysisID string) int {
	n, err := s.docs.count(ctx, tenantID, analysisID)
	if err != nil {
		slog.Default().Warn("analysis: 采集文档计数失败",
			slog.String("tenant_id", tenantID),
			slog.String("analysis_id", analysisID),
			slog.String("err", err.Error()))
		return 0
	}
	return n
}
