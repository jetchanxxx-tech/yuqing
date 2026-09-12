package analysis

import (
	"context"
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

// documentStore 按租户+分析分桶保存采集结果（内存实现，重启即失）。
type documentStore struct {
	mu   sync.RWMutex
	data map[string]map[string][]Document // tenantID → analysisID → docs
}

func newDocumentStore() *documentStore {
	return &documentStore{data: make(map[string]map[string][]Document)}
}

func (d *documentStore) add(_ context.Context, tenantID, analysisID string, docs []Document) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.data[tenantID] == nil {
		d.data[tenantID] = make(map[string][]Document)
	}
	d.data[tenantID][analysisID] = append(d.data[tenantID][analysisID], docs...)
}

func (d *documentStore) list(_ context.Context, tenantID, analysisID string) []Document {
	d.mu.RLock()
	defer d.mu.RUnlock()
	src := d.data[tenantID][analysisID]
	out := make([]Document, len(src))
	copy(out, src) // 返回副本，避免调用方改动内部状态
	return out
}

func (d *documentStore) count(_ context.Context, tenantID, analysisID string) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.data[tenantID][analysisID])
}

// ── Service 上的公开方法 ─────────────────────────────────

// AddDocuments 由管线写入采集结果。
func (s *Service) AddDocuments(ctx context.Context, tenantID, analysisID string, docs []Document) {
	s.docs.add(ctx, tenantID, analysisID, docs)
}

// Documents 返回某次分析采集到的全部文档（无结果时返回空切片而非 nil，
// 保证 JSON 序列化为 [] 而不是 null）。
func (s *Service) Documents(ctx context.Context, tenantID, analysisID string) []Document {
	docs := s.docs.list(ctx, tenantID, analysisID)
	if docs == nil {
		return []Document{}
	}
	return docs
}

// DocumentCount 返回采集文档数（供 /result 汇总）。
func (s *Service) DocumentCount(ctx context.Context, tenantID, analysisID string) int {
	return s.docs.count(ctx, tenantID, analysisID)
}
