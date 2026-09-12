package report

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// PGStore 是 Store 的 PostgreSQL 实现（reports 表，见
// platform/migrations/platform/0002_tenant_data.sql），用 tenant_id 列做逻辑隔离。
// 行为对齐 MemoryStore：Get 跨租户/不存在都报 ErrNotFound、List 先过滤后分页、
// Create 重复 ID 报 ErrConflict、UpdateStatus 命中 0 行报 ErrNotFound。
//
// 已知差异：0002 的 reports 表没有 title/summary_json 列（迁移不可改），
// Report.Title / Report.SummaryJSON 不落库（Create 忽略、Get/List 恒返回空值）。
// 当前 CreateFromAnalysis 不设置这两个字段，故线上行为无差异；
// 将来若需要，先补一条 0003 迁移加列，再在两处 SQL 中带上它们。
type PGStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*PGStore)(nil)

// NewPGStore 创建 PostgreSQL 报告存储。
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// reportColumns 是读路径的列清单，顺序与扫描目标一致。
// created_at 由列默认值 now() 填充，Report 结构体里没有对应字段。
const reportColumns = `id, analysis_id, format, status, file_key`

// Create 插入一条报告记录；ID 已存在报 ErrConflict。
func (s *PGStore) Create(ctx context.Context, tenantID string, r Report) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO reports (id, tenant_id, analysis_id, format, status, file_key)
VALUES ($1, $2, $3, $4, $5, $6)`, r.ID, tenantID, r.AnalysisID, r.Format, r.Status, r.FileKey)
	if err != nil {
		if isUniqueViolation(err) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "report already exists")
		}
		return pgInternal(err)
	}
	return nil
}

// Get 读取一条报告（跨租户与不存在都答 ErrNotFound，不泄露他租户资源存在性）。
func (s *PGStore) Get(ctx context.Context, tenantID, reportID string) (*Report, error) {
	var r Report
	err := s.pool.QueryRow(ctx,
		`SELECT `+reportColumns+` FROM reports WHERE id = $1 AND tenant_id = $2`,
		reportID, tenantID).Scan(&r.ID, &r.AnalysisID, &r.Format, &r.Status, &r.FileKey)
	if err != nil {
		return nil, notFoundOrInternal(err, "report not found")
	}
	return &r, nil
}

// List 返回某租户的报告，按 created_at, id 升序（创建顺序：ID 是 ULID，
// 同一毫秒内也单调递增，故时间相同时按 ID 打破平局仍是创建序）。
// 过滤条件按需拼进 WHERE（占位符序号随条件增长），分页语义与内存版一致：
// Limit <= 0 表示不限，Offset 越过末尾返回空切片。
func (s *PGStore) List(ctx context.Context, tenantID string, f Filter) ([]Report, error) {
	q := `SELECT ` + reportColumns + ` FROM reports WHERE tenant_id = $1`
	args := []any{tenantID}
	if f.AnalysisID != "" {
		args = append(args, f.AnalysisID)
		q += fmt.Sprintf(" AND analysis_id = $%d", len(args))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	q += " ORDER BY created_at, id"
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if f.Offset > 0 {
		args = append(args, f.Offset)
		q += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, pgInternal(err)
	}
	defer rows.Close()

	out := make([]Report, 0)
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.AnalysisID, &r.Format, &r.Status, &r.FileKey); err != nil {
			return nil, pgInternal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, pgInternal(err)
	}
	return out, nil
}

// UpdateStatus 更新报告状态；行不存在（含他租户）报 ErrNotFound。
func (s *PGStore) UpdateStatus(ctx context.Context, tenantID, reportID, status string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE reports SET status = $3 WHERE id = $1 AND tenant_id = $2`,
		reportID, tenantID, status)
	if err != nil {
		return pgInternal(err)
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "report not found")
	}
	return nil
}

// ── 错误映射（与 analysis 包同构的小工具） ────────────────

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

// pgInternal 把驱动错误包成 ErrInternal，消息带驱动原文（含表名/SQLSTATE），
// 不含连接串与数据值。
func pgInternal(err error) error {
	if err == nil {
		return nil
	}
	return pkgerrors.Wrap(pkgerrors.ErrInternal, "database error: "+err.Error())
}
