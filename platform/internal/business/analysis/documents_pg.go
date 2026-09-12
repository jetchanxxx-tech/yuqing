package analysis

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuging/platform/internal/pkg/id"
)

// pgDocumentStore 是 documentStore 的 PostgreSQL 实现，落在 raw_documents 表
// （platform/migrations/platform/0002_tenant_data.sql）。
//
// 与 memoryDocumentStore 的差异：
//   - 重复 ID（同一批采集结果重跑，如 Rerun）不重复入库 —— raw_documents.id
//     是主键，冲突即跳过（内存版会 append 出重复行）；
//   - 空 ID 由应用补 ULID：引擎在正文抓取失败时会给出空 ID
//     （query_engine 用 content_hash 当 ID），空串会互相覆盖；
//   - 顺序：表里没有序号/时间列，list 按 id 升序返回（稳定但非插入序）；
//   - published_at 列是 TIMESTAMPTZ，非 RFC3339 的时间串存 NULL（见下）。
type pgDocumentStore struct {
	pool *pgxpool.Pool
}

var _ documentStore = (*pgDocumentStore)(nil)

// newPGDocumentStore 创建 PostgreSQL 采集文档存储。
func newPGDocumentStore(pool *pgxpool.Pool) *pgDocumentStore {
	return &pgDocumentStore{pool: pool}
}

const (
	// ON CONFLICT DO NOTHING 让重复投递幂等：管线重跑（Rerun）会重新写入
	// 同一批采集结果，不该把文档数翻倍、也不该让整批写入失败。
	//
	// 注意：raw_documents.id 是**全局**主键（不含 tenant_id），因此两个租户
	// 抓到同一篇内容（ID = content_hash）时，后写入的一行会被跳过。这是
	// 0002 迁移的表结构约束，需改主键为 (tenant_id, analysis_id, id) 才能根治。
	insertDocumentSQL = `INSERT INTO raw_documents (
	id, tenant_id, analysis_id, title, url, content, author,
	source_type, source_name, published_at, content_hash
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (id) DO NOTHING`

	selectDocumentsSQL = `SELECT id, title, url, content, author, source_type, source_name,
	published_at, content_hash
FROM raw_documents WHERE tenant_id = $1 AND analysis_id = $2
ORDER BY id`

	countDocumentsSQL = `SELECT count(*) FROM raw_documents WHERE tenant_id = $1 AND analysis_id = $2`
)

// add 批量写入采集结果（pgx.Batch：一次往返，避免逐条 INSERT 的延迟）。
func (d *pgDocumentStore) add(ctx context.Context, tenantID, analysisID string, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, doc := range docs {
		batch.Queue(insertDocumentSQL, documentArgs(tenantID, analysisID, doc)...)
	}
	results := d.pool.SendBatch(ctx, batch)
	defer func() { _ = results.Close() }()

	// 逐条取结果：某条失败（如约束冲突以外的错误）立即返回，
	// 同批已生效的行保留 —— 重跑时靠 ON CONFLICT 幂等补齐。
	for range docs {
		if _, err := results.Exec(); err != nil {
			return pgInternal(err)
		}
	}
	return nil
}

// list 返回某次分析采集到的文档，无结果时返回空切片（非 nil）。
func (d *pgDocumentStore) list(ctx context.Context, tenantID, analysisID string) ([]Document, error) {
	rows, err := d.pool.Query(ctx, selectDocumentsSQL, tenantID, analysisID)
	if err != nil {
		return nil, pgInternal(err)
	}
	defer rows.Close()

	out := make([]Document, 0)
	for rows.Next() {
		var (
			doc         Document
			publishedAt *time.Time
		)
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.URL, &doc.Content, &doc.Author,
			&doc.SourceType, &doc.SourceName, &publishedAt, &doc.ContentHash); err != nil {
			return nil, pgInternal(err)
		}
		// TIMESTAMPTZ → RFC3339（不带小数秒）。写入时按 RFC3339 解析、
		// 读回统一成 UTC 表示，因此带 +08:00 的时间会显示为同一时刻的 Z 形式。
		if publishedAt != nil {
			doc.PublishedAt = publishedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, pgInternal(err)
	}
	return out, nil
}

// count 返回某次分析采集到的文档数。
func (d *pgDocumentStore) count(ctx context.Context, tenantID, analysisID string) (int, error) {
	var n int
	if err := d.pool.QueryRow(ctx, countDocumentsSQL, tenantID, analysisID).Scan(&n); err != nil {
		return 0, pgInternal(err)
	}
	return n, nil
}

// documentArgs 组装一行 raw_documents 的参数。
func documentArgs(tenantID, analysisID string, doc Document) []any {
	docID := doc.ID
	if docID == "" {
		// 主键是 id：空串会让多条空 ID 文档互相顶掉，这里补 ULID。
		docID = id.New()
	}
	return []any{
		docID, tenantID, analysisID, doc.Title, doc.URL, doc.Content, doc.Author,
		doc.SourceType, doc.SourceName, nullablePublishedAt(doc.PublishedAt), doc.ContentHash,
	}
}

// nullablePublishedAt 把 Go 侧的字符串时间写进 TIMESTAMPTZ 列。
//
// 引擎产出的是 RFC3339（Bocha/Scrapling），能解析就存；解析失败或为空则存
// NULL，读回为空串 —— TIMESTAMPTZ 无法无损容纳任意时间串，宁可标空也不写错值
// （Document.PublishedAt 只用于展示，不是排序或去重键）。
func nullablePublishedAt(s string) any {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return t.UTC()
}
