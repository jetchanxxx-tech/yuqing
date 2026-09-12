package report

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
)

// pgTestEnv 指向已应用 0002 迁移的测试库；未设置时跳过全部 pg 用例。
const pgTestEnv = "YUGING_TEST_PG_URL"

// pgTestPool 新建测试连接池并在用例结束时关闭；环境变量缺失即跳过当前用例。
func pgTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(pgTestEnv)
	if dsn == "" {
		t.Skipf("%s 未设置，跳过 PostgreSQL 用例", pgTestEnv)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("连接测试库失败（%s）: %v", pgTestEnv, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newTestTenant 返回本次用例独占的租户 ID，隔离库中历史数据。
func newTestTenant() string { return "t-" + id.New() }

func cleanupReports(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM reports WHERE tenant_id = $1`, tenantID); err != nil {
		t.Errorf("清理 reports 失败: %v", err)
	}
}

// reportStoreImpl 是一个待测 Store 实现。
type reportStoreImpl struct {
	name  string
	build func(t *testing.T) (Store, func(tenantID string))
}

// reportStoreImpls 返回当前环境可测的实现：内存版恒可用，pg 版需 YUGING_TEST_PG_URL。
func reportStoreImpls(t *testing.T) []reportStoreImpl {
	t.Helper()
	impls := []reportStoreImpl{{
		name:  "memory",
		build: func(*testing.T) (Store, func(string)) { return NewMemoryStore(), func(string) {} },
	}}
	if os.Getenv(pgTestEnv) == "" {
		t.Logf("%s 未设置：只跑内存实现", pgTestEnv)
		return impls
	}
	impls = append(impls, reportStoreImpl{
		name: "postgres",
		build: func(t *testing.T) (Store, func(string)) {
			pool := pgTestPool(t)
			return NewPGStore(pool), func(tenantID string) { cleanupReports(t, pool, tenantID) }
		},
	})
	return impls
}

func sampleReport(analysisID, format string) Report {
	return Report{
		ID:         id.New(),
		AnalysisID: analysisID,
		Format:     format,
		Status:     "completed",
		FileKey:    "reports/" + id.New() + "." + format,
	}
}

func TestReportStore_createGetRoundTrip(t *testing.T) {
	for _, impl := range reportStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant, other := newTestTenant(), newTestTenant()
			defer cleanup(tenant)
			defer cleanup(other)

			want := sampleReport("analysis-1", "html")
			if err := st.Create(ctx, tenant, want); err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			got, err := st.Get(ctx, tenant, want.ID)
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}
			if got.ID != want.ID || got.AnalysisID != want.AnalysisID ||
				got.Format != want.Format || got.Status != want.Status || got.FileKey != want.FileKey {
				t.Errorf("got %+v, want %+v", got, want)
			}

			// 返回副本：改动读出的对象不得影响已存数据
			got.Status = "改坏了"
			again, err := st.Get(ctx, tenant, want.ID)
			if err != nil {
				t.Fatal(err)
			}
			if again.Status != want.Status {
				t.Errorf("Get 未返回副本，Status = %q", again.Status)
			}

			// 缺失与跨租户都答 ErrNotFound（不泄露他租户资源是否存在）
			if _, err := st.Get(ctx, tenant, "01MISSINGNOTFOUND000000000"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
				t.Errorf("Get missing error = %v, want ErrNotFound", err)
			}
			if _, err := st.Get(ctx, other, want.ID); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
				t.Errorf("跨租户 Get error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestReportStore_createDuplicateConflicts(t *testing.T) {
	for _, impl := range reportStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			r := sampleReport("analysis-1", "html")
			if err := st.Create(ctx, tenant, r); err != nil {
				t.Fatalf("首次 Create failed: %v", err)
			}
			if err := st.Create(ctx, tenant, r); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
				t.Errorf("重复 ID Create error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestReportStore_listFiltersAndPaginates(t *testing.T) {
	for _, impl := range reportStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant, other := newTestTenant(), newTestTenant()
			defer cleanup(tenant)
			defer cleanup(other)

			// 空租户：空切片而非 nil
			empty, err := st.List(ctx, tenant, Filter{})
			if err != nil {
				t.Fatalf("List failed: %v", err)
			}
			if empty == nil || len(empty) != 0 {
				t.Errorf("空租户 List = %#v, want 非 nil 空切片", empty)
			}

			// 三个报告：tenant 两个（不同 analysis）+ other 一个
			first := sampleReport("analysis-1", "html")
			second := sampleReport("analysis-2", "markdown")
			foreign := sampleReport("analysis-1", "html")
			for _, tc := range []struct {
				tenantID string
				r        Report
			}{{tenant, first}, {tenant, second}, {other, foreign}} {
				if err := st.Create(ctx, tc.tenantID, tc.r); err != nil {
					t.Fatalf("Create failed: %v", err)
				}
			}

			all, err := st.List(ctx, tenant, Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 2 {
				t.Fatalf("List len = %d, want 2（不含其他租户）", len(all))
			}
			if all[0].ID != first.ID || all[1].ID != second.ID {
				t.Errorf("List 顺序 = [%s %s], want [%s %s]（创建顺序）",
					all[0].ID, all[1].ID, first.ID, second.ID)
			}

			byAnalysis, err := st.List(ctx, tenant, Filter{AnalysisID: "analysis-2"})
			if err != nil {
				t.Fatal(err)
			}
			if len(byAnalysis) != 1 || byAnalysis[0].ID != second.ID {
				t.Errorf("按 analysis 过滤 = %+v, want 仅 %s", byAnalysis, second.ID)
			}

			byStatus, err := st.List(ctx, tenant, Filter{Status: "failed"})
			if err != nil {
				t.Fatal(err)
			}
			if byStatus == nil || len(byStatus) != 0 {
				t.Errorf("按 status 过滤 = %#v, want 非 nil 空切片", byStatus)
			}

			limited, err := st.List(ctx, tenant, Filter{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(limited) != 1 || limited[0].ID != first.ID {
				t.Errorf("Limit=1 = %+v, want 仅 %s", limited, first.ID)
			}

			offset, err := st.List(ctx, tenant, Filter{Offset: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(offset) != 1 || offset[0].ID != second.ID {
				t.Errorf("Offset=1 = %+v, want 仅 %s", offset, second.ID)
			}

			// Offset 越过末尾：空切片（内存版显式返回 []Report{}）
			beyond, err := st.List(ctx, tenant, Filter{Offset: 99})
			if err != nil {
				t.Fatal(err)
			}
			if beyond == nil || len(beyond) != 0 {
				t.Errorf("Offset=99 = %#v, want 非 nil 空切片", beyond)
			}

			// Offset + Limit 组合：先过滤后分页（内存版语义）
			page, err := st.List(ctx, tenant, Filter{Offset: 1, Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(page) != 1 || page[0].ID != second.ID {
				t.Errorf("Offset=1 Limit=1 = %+v, want 仅 %s", page, second.ID)
			}
		})
	}
}

func TestReportStore_updateStatus(t *testing.T) {
	for _, impl := range reportStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant, other := newTestTenant(), newTestTenant()
			defer cleanup(tenant)
			defer cleanup(other)

			r := sampleReport("analysis-1", "html")
			if err := st.Create(ctx, tenant, r); err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			if err := st.UpdateStatus(ctx, tenant, r.ID, "failed"); err != nil {
				t.Fatalf("UpdateStatus failed: %v", err)
			}
			got, err := st.Get(ctx, tenant, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "failed" {
				t.Errorf("Status = %q, want failed", got.Status)
			}
			if got.FileKey != r.FileKey || got.Format != r.Format {
				t.Errorf("UpdateStatus 改动了其他字段: %+v", got)
			}

			if err := st.UpdateStatus(ctx, tenant, "01MISSINGNOTFOUND000000000", "failed"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
				t.Errorf("UpdateStatus missing error = %v, want ErrNotFound", err)
			}
			// 跨租户不得改动他租户的报告
			if err := st.UpdateStatus(ctx, other, r.ID, "failed"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
				t.Errorf("跨租户 UpdateStatus error = %v, want ErrNotFound", err)
			}
			primary, err := st.Get(ctx, tenant, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if primary.Status != "failed" {
				t.Errorf("跨租户调用改动了本租户数据: Status = %q", primary.Status)
			}
		})
	}
}

// TestPGReportStore_titleAndSummaryNotPersisted 锁定已知缺口：0002 的 reports
// 表没有 title/summary_json 列（迁移不可改），这两个字段不落库。
// 当前 CreateFromAnalysis 不设置它们，故线上行为无差异；将来要用需补 0003 迁移。
func TestPGReportStore_titleAndSummaryNotPersisted(t *testing.T) {
	pool := pgTestPool(t)
	st := NewPGStore(pool)
	tenant := newTestTenant()
	defer cleanupReports(t, pool, tenant)
	ctx := context.Background()

	r := sampleReport("analysis-1", "html")
	r.Title = "雅阁后排舆情监测报告"
	r.SummaryJSON = `{"positive":3,"negative":7}`
	if err := st.Create(ctx, tenant, r); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	got, err := st.Get(ctx, tenant, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "" || got.SummaryJSON != "" {
		t.Errorf("Title/SummaryJSON = %q/%q, want 空（表中无对应列）", got.Title, got.SummaryJSON)
	}
	// 其余字段照常落库
	if got.ID != r.ID || got.Format != r.Format || got.Status != r.Status || got.FileKey != r.FileKey {
		t.Errorf("其余字段未落库: %+v", got)
	}
}
