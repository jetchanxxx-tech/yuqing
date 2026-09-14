package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// pgStore 是 analysisStore 的 PostgreSQL 实现，落在 platform 库的 analyses 表
// （platform/migrations/platform/0002_tenant_data.sql），用 tenant_id 列做逻辑
// 隔离（database-per-tenant 物理隔离见 README 路线图）。
//
// 与 memoryStore 对齐的行为：get/list 返回副本、put 重复 ID 报 ErrConflict、
// mutate 在一次事务内读-改-写（互斥由 SELECT … FOR UPDATE 的行锁保证）。
// 刻意差异见 list 与 mutate 的注释。
type pgStore struct {
	pool *pgxpool.Pool
}

var _ analysisStore = (*pgStore)(nil)

// newPGStore 创建 PostgreSQL 分析存储。
func newPGStore(pool *pgxpool.Pool) *pgStore { return &pgStore{pool: pool} }

// analysisColumns 是 SELECT 列清单，顺序必须与 scanAnalysis 的扫描目标、
// analysisArgs 的参数顺序一致。
const analysisColumns = `id, tenant_id, name, analysis_type, state, progress, error_code,
	started_at, finished_at, created_at, keywords, sources, doc_count,
	summary, warning, sentiments, topics, dimensions, report_id, report_content`

// analysisColumnsList 供 list 使用：不取 KB 级 report_content ——
// 详情页按秒轮询 /analyses/:id，内联整份报告会让每次轮询都传输正文。
// 列表端点与详情端点都已把该字段标为 json:"-"（正文只经
// GET /analyses/:id/result 返回，那条路径走 get）。
const analysisColumnsList = `id, tenant_id, name, analysis_type, state, progress, error_code,
	started_at, finished_at, created_at, keywords, sources, doc_count,
	summary, warning, sentiments, topics, dimensions, report_id, '' AS report_content`

const insertAnalysisSQL = `INSERT INTO analyses (
	id, tenant_id, name, analysis_type, state, progress, error_code,
	started_at, finished_at, created_at, keywords, sources, doc_count,
	summary, warning, sentiments, topics, dimensions, report_id, report_content
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`

// updateAnalysisSQL 用 $1/$2 定位行（id + tenant_id），其余列整体写回：
// mutate 的读-改-写语义在内存版是「改指针指向的对象」，在 pg 版是「整行 UPDATE」。
const updateAnalysisSQL = `UPDATE analyses SET
	name = $3, analysis_type = $4, state = $5, progress = $6, error_code = $7,
	started_at = $8, finished_at = $9, created_at = $10, keywords = $11, sources = $12,
	doc_count = $13, summary = $14, warning = $15, sentiments = $16, topics = $17,
	dimensions = $18, report_id = $19, report_content = $20
WHERE id = $1 AND tenant_id = $2`

// put 写入一条新分析；ID 已存在（含他租户）报 ErrConflict。
func (s *pgStore) put(ctx context.Context, tenantID string, a *AnalysisResult) error {
	if _, err := s.pool.Exec(ctx, insertAnalysisSQL, analysisArgs(tenantID, a)...); err != nil {
		if isUniqueViolation(err) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "analysis already exists")
		}
		return pgInternal(err)
	}
	return nil
}

// get 读取一条分析（含 report_content，/analyses/:id/result 需要正文）。
func (s *pgStore) get(ctx context.Context, tenantID, analysisID string) (*AnalysisResult, error) {
	a, err := scanAnalysis(s.pool.QueryRow(ctx,
		`SELECT `+analysisColumns+` FROM analyses WHERE id = $1 AND tenant_id = $2`,
		analysisID, tenantID))
	if err != nil {
		return nil, notFoundOrInternal(err, "analysis not found")
	}
	return a, nil
}

// list 返回某租户的全部分析，按 created_at, id 升序（对齐内存版排序：
// 时间相同则按 ID 打破平局，保证分页与顺序稳定）。
func (s *pgStore) list(ctx context.Context, tenantID string) ([]AnalysisResult, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+analysisColumnsList+` FROM analyses WHERE tenant_id = $1 ORDER BY created_at, id`,
		tenantID)
	if err != nil {
		return nil, pgInternal(err)
	}
	defer rows.Close()

	out := make([]AnalysisResult, 0)
	for rows.Next() {
		a, err := scanAnalysis(rows)
		if err != nil {
			return nil, pgInternal(err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, pgInternal(err)
	}
	return out, nil
}

// mutate 在事务内对一条分析做读-改-写：SELECT … FOR UPDATE 取行锁，
// 回调返回 nil 才写回并提交，否则回滚。
//
// 与内存版的差异：内存版在写锁内直接改存储对象，回调返回错误时它已做的
// 改动会**保留**；pg 版回滚，改动丢弃。本包所有回调（transition/advance/
// markFailed/Rerun/SetInsight/SetReport/SetWarning）都先校验后写入，
// 因此实际行为一致；新增回调请沿用「先校验后写入」。
func (s *pgStore) mutate(ctx context.Context, tenantID, analysisID string, fn func(*AnalysisResult) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pgInternal(err)
	}
	// Commit 之后的 Rollback 返回 ErrTxClosed（忽略）；未提交路径借此释放
	// 连接与行锁 —— 回调报错时不能漏掉回滚，否则行锁要等连接归还才释放。
	defer func() { _ = tx.Rollback(ctx) }()

	a, err := scanAnalysis(tx.QueryRow(ctx,
		`SELECT `+analysisColumns+` FROM analyses WHERE id = $1 AND tenant_id = $2 FOR UPDATE`,
		analysisID, tenantID))
	if err != nil {
		return notFoundOrInternal(err, "analysis not found")
	}
	if err := fn(a); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, updateAnalysisSQL, analysisArgs(tenantID, a)...); err != nil {
		return pgInternal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return pgInternal(err)
	}
	return nil
}

// analysisArgs 组装写入参数，顺序与 analysisColumns / insertAnalysisSQL 一致
// （$1 = id，$2 = tenant_id，供 UPDATE 的 WHERE 直接复用）。
func analysisArgs(tenantID string, a *AnalysisResult) []any {
	return []any{
		a.ID, tenantID, a.Name, a.AnalysisType, string(a.State), a.Progress, a.ErrorCode,
		nullableTime(a.StartedAt), nullableTime(a.FinishedAt), createdAtOrNow(a.CreatedAt),
		marshalJSON(a.Keywords), marshalJSON(a.Sources), a.DocCount,
		a.Summary, a.Warning, marshalJSON(a.Sentiments), marshalJSON(a.Topics),
		marshalJSON(a.Dimensions), a.ReportID, a.ReportContent,
	}
}

// scanAnalysis 把一行 analyses 扫描为 *AnalysisResult。tenant_id 是分桶键
// （调用方已按它过滤），AnalysisResult 里没有对应字段，故扫描后丢弃。
func scanAnalysis(row pgx.Row) (*AnalysisResult, error) {
	var (
		a          AnalysisResult
		tenantID   string
		state      string
		startedAt  *time.Time
		finishedAt *time.Time
		keywords   []byte
		sources    []byte
		sentiments []byte
		topics     []byte
		dimensions []byte
	)
	if err := row.Scan(
		&a.ID, &tenantID, &a.Name, &a.AnalysisType, &state, &a.Progress, &a.ErrorCode,
		&startedAt, &finishedAt, &a.CreatedAt, &keywords, &sources, &a.DocCount,
		&a.Summary, &a.Warning, &sentiments, &topics, &dimensions, &a.ReportID, &a.ReportContent,
	); err != nil {
		return nil, err
	}
	a.State = State(state)
	a.StartedAt = timeOrZero(startedAt)
	a.FinishedAt = timeOrZero(finishedAt)
	a.CreatedAt = a.CreatedAt.UTC()
	if err := unmarshalJSON(keywords, &a.Keywords); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(sources, &a.Sources); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(sentiments, &a.Sentiments); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(topics, &a.Topics); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(dimensions, &a.Dimensions); err != nil {
		return nil, err
	}
	return &a, nil
}

// ── 类型与错误映射的公共小工具 ───────────────────────────

// marshalJSON 序列化 JSONB 列。nil 切片写成 JSON null、空切片写成 []，
// 读回时一一对应（内存版保留 nil / 空切片的区分，这里同样保留）。
// 字段都是 JSON 原生类型，序列化不会失败；保底写 null 而不是 panic。
func marshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// unmarshalJSON 解析 JSONB 列；JSON null 解成 nil 切片，与写入侧对称。
func unmarshalJSON(raw []byte, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// nullableTime 把 Go 零值时间写成 SQL NULL（TIMESTAMPTZ 可空列），
// 读回时零值 ↔ NULL 一一对应，避免存成 0001-01-01。
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// timeOrZero 把可空时间列还原成 Go 时间，NULL → 零值（对齐内存版未开始/
// 未结束的表示）。统一转 UTC：TIMESTAMPTZ 是绝对时刻，表示层不应随会话时区变。
func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// createdAtOrNow 兜底 created_at（列 NOT NULL DEFAULT now()）：
// 调用方漏设时由应用补当前时间，避免整条写入失败。
func createdAtOrNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

// isUniqueViolation 判定唯一键冲突（SQLSTATE 23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// notFoundOrInternal 把 pgx.ErrNoRows 映射成 ErrNotFound，其他错误归 ErrInternal。
func notFoundOrInternal(err error, msg string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, msg)
	}
	return pgInternal(err)
}

// pgInternal 把驱动/编解码错误包成 ErrInternal。错误信封对外可见：
// 驱动消息（含表名/SQLSTATE）用于定位缺迁移一类的故障，且不含连接串与数据值。
func pgInternal(err error) error {
	if err == nil {
		return nil
	}
	return pkgerrors.Wrap(pkgerrors.ErrInternal, "database error: "+err.Error())
}
