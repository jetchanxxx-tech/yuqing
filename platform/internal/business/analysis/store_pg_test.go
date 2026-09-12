package analysis

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
)

// pgTestEnv 指向已应用 0002 迁移的测试库。未设置时全部 pg 用例跳过 ——
// 本地开发无 PG，真实 GREEN 在服务器上跑：
//
//	YUQING_TEST_PG_URL='postgres://<用户>:<口令>@localhost:5432/yuqing_test?sslmode=disable' go test ./...
//
// 凭据只从环境变量读，勿写进代码或 CI 配置。
const pgTestEnv = "YUQING_TEST_PG_URL"

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
	// 先 Ping：DSN 写错时立刻报错，而不是让断言以空结果/超时的形式失败
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("连接测试库失败（%s）: %v", pgTestEnv, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newTestTenant 返回本次用例独占的租户 ID（ULID 前缀），
// 保证断言「list 只含本租户数据」不受库中历史数据影响。
func newTestTenant() string { return "t-" + id.New() }

// cleanupAnalyses 删除某租户的全部分析记录（pg 用例收尾用）。
func cleanupAnalyses(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM analyses WHERE tenant_id = $1`, tenantID); err != nil {
		t.Errorf("清理 analyses 失败: %v", err)
	}
}

// analysisStoreImpl 是一个待测 store 实现：build 返回全新实例与清理函数。
type analysisStoreImpl struct {
	name  string
	build func(t *testing.T) (analysisStore, func(tenantID string))
}

// analysisStoreImpls 返回当前环境可测的实现：内存版恒可用，pg 版需 YUQING_TEST_PG_URL。
func analysisStoreImpls(t *testing.T) []analysisStoreImpl {
	t.Helper()
	impls := []analysisStoreImpl{{
		name:  "memory",
		build: func(*testing.T) (analysisStore, func(string)) { return newMemoryStore(), func(string) {} },
	}}
	if os.Getenv(pgTestEnv) == "" {
		t.Logf("%s 未设置：只跑内存实现", pgTestEnv)
		return impls
	}
	impls = append(impls, analysisStoreImpl{
		name: "postgres",
		build: func(t *testing.T) (analysisStore, func(string)) {
			pool := pgTestPool(t)
			return newPGStore(pool), func(tenantID string) { cleanupAnalyses(t, pool, tenantID) }
		},
	})
	return impls
}

// sampleAnalysis 构造一条字段齐全的记录。
// 时间截断到微秒：TIMESTAMPTZ 精度只到微秒，纳秒会被库截断。
func sampleAnalysis() *AnalysisResult {
	created := time.Now().UTC().Truncate(time.Microsecond)
	return &AnalysisResult{
		ID:           id.New(),
		Name:         "雅阁后排舆情",
		AnalysisType: "brand",
		State:        StateFetching,
		Progress:     25,
		StartedAt:    created.Add(-time.Minute),
		CreatedAt:    created,
		Keywords:     []string{"雅阁后排", "后备箱"},
		Sources:      []string{"weibo", "news"},
		DocCount:     19,
		Summary:      "后排空间争议升温",
		Warning:      "insight engine not configured",
		Sentiments: []Sentiment{{
			DocumentID: "doc-1", Sentiment: "negative", Level: "负面",
			Confidence: 0.82, Score: -0.7,
		}},
		Topics: []Topic{{
			ID: "topic-1", Name: "后排空间", Keywords: []string{"空间", "后备箱"},
			DocCount: 12, Trend: "rising",
		}},
		ReportID:      "report-1",
		ReportContent: "<html><body>" + strings.Repeat("x", 4096) + "</body></html>",
	}
}

// assertAnalysisEqual 逐字段比对，时间用 Equal（跨驱动的时间表示不保证 DeepEqual）。
func assertAnalysisEqual(t *testing.T, got, want *AnalysisResult) {
	t.Helper()
	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.AnalysisType != want.AnalysisType {
		t.Errorf("AnalysisType = %q, want %q", got.AnalysisType, want.AnalysisType)
	}
	if got.State != want.State {
		t.Errorf("State = %q, want %q", got.State, want.State)
	}
	if got.Progress != want.Progress {
		t.Errorf("Progress = %d, want %d", got.Progress, want.Progress)
	}
	if got.ErrorCode != want.ErrorCode {
		t.Errorf("ErrorCode = %q, want %q", got.ErrorCode, want.ErrorCode)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if !got.FinishedAt.Equal(want.FinishedAt) {
		t.Errorf("FinishedAt = %v, want %v", got.FinishedAt, want.FinishedAt)
	}
	if got.DocCount != want.DocCount {
		t.Errorf("DocCount = %d, want %d", got.DocCount, want.DocCount)
	}
	if got.Summary != want.Summary || got.Warning != want.Warning || got.ReportID != want.ReportID {
		t.Errorf("Summary/Warning/ReportID = %q/%q/%q, want %q/%q/%q",
			got.Summary, got.Warning, got.ReportID, want.Summary, want.Warning, want.ReportID)
	}
	if !reflect.DeepEqual(got.Keywords, want.Keywords) {
		t.Errorf("Keywords = %v, want %v", got.Keywords, want.Keywords)
	}
	if !reflect.DeepEqual(got.Sources, want.Sources) {
		t.Errorf("Sources = %v, want %v", got.Sources, want.Sources)
	}
	if !reflect.DeepEqual(got.Sentiments, want.Sentiments) {
		t.Errorf("Sentiments = %+v, want %+v", got.Sentiments, want.Sentiments)
	}
	if !reflect.DeepEqual(got.Topics, want.Topics) {
		t.Errorf("Topics = %+v, want %+v", got.Topics, want.Topics)
	}
	if got.ReportContent != want.ReportContent {
		t.Errorf("ReportContent = %d 字节, want %d 字节", len(got.ReportContent), len(want.ReportContent))
	}
}

func TestAnalysisStore_putGetRoundTrip(t *testing.T) {
	for _, impl := range analysisStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			want := sampleAnalysis()
			if err := st.put(ctx, tenant, want); err != nil {
				t.Fatalf("put failed: %v", err)
			}
			got, err := st.get(ctx, tenant, want.ID)
			if err != nil {
				t.Fatalf("get failed: %v", err)
			}
			assertAnalysisEqual(t, got, want)

			// 返回副本：改动读出的对象不得影响已存数据。
			got.Name = "改名"
			got.Progress = 999
			got.Topics = append(got.Topics, Topic{ID: "topic-2"})
			again, err := st.get(ctx, tenant, want.ID)
			if err != nil {
				t.Fatal(err)
			}
			if again.Name != want.Name || again.Progress != want.Progress || len(again.Topics) != len(want.Topics) {
				t.Errorf("get 未返回副本: %+v", again)
			}
		})
	}
}

func TestAnalysisStore_putPreservesNilAndEmptySlices(t *testing.T) {
	for _, impl := range analysisStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			// nil 与原样空切片都要能往返：JSONB 存 "null" 与 "[]" 互不混淆。
			nilSlices := sampleAnalysis()
			nilSlices.Keywords, nilSlices.Sentiments = nil, nil
			emptySlices := sampleAnalysis()
			emptySlices.Keywords, emptySlices.Sentiments = []string{}, []Sentiment{}

			for _, want := range []*AnalysisResult{nilSlices, emptySlices} {
				if err := st.put(ctx, tenant, want); err != nil {
					t.Fatalf("put failed: %v", err)
				}
				got, err := st.get(ctx, tenant, want.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got.Keywords, want.Keywords) {
					t.Errorf("Keywords = %#v, want %#v", got.Keywords, want.Keywords)
				}
				if !reflect.DeepEqual(got.Sentiments, want.Sentiments) {
					t.Errorf("Sentiments = %#v, want %#v", got.Sentiments, want.Sentiments)
				}
			}
		})
	}
}

func TestAnalysisStore_putDuplicateConflicts(t *testing.T) {
	for _, impl := range analysisStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			a := sampleAnalysis()
			if err := st.put(ctx, tenant, a); err != nil {
				t.Fatalf("首次 put failed: %v", err)
			}
			if err := st.put(ctx, tenant, a); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
				t.Errorf("重复 ID 的 put error = %v, want ErrConflict", err)
			}
			// 跨租户同 ID 的行为两版不同（内存版按租户分桶、互不冲突；
			// pg 版 id 是全局主键、必然冲突），见 TestPGStore_idIsGlobalPrimaryKey。
		})
	}
}

// TestPGStore_idIsGlobalPrimaryKey 锁定 0002 表结构带来的跨租户语义：
// analyses.id 是全局主键（内存版是 tenant → id 两级字典，同 ID 可存在于
// 不同租户）。ID 是 ULID，实际不会碰撞，故对业务无影响。
func TestPGStore_idIsGlobalPrimaryKey(t *testing.T) {
	pool := pgTestPool(t)
	st := newPGStore(pool)
	tenant, other := newTestTenant(), newTestTenant()
	defer cleanupAnalyses(t, pool, tenant)
	defer cleanupAnalyses(t, pool, other)
	ctx := context.Background()

	a := sampleAnalysis()
	if err := st.put(ctx, tenant, a); err != nil {
		t.Fatalf("put failed: %v", err)
	}
	if err := st.put(ctx, other, a); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("跨租户同 ID 的 put error = %v, want ErrConflict", err)
	}
	// 他租户读不到该行（租户隔离优先于主键可见性）
	if _, err := st.get(ctx, other, a.ID); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("跨租户 get error = %v, want ErrNotFound", err)
	}
}

func TestAnalysisStore_getNotFoundAndTenantIsolation(t *testing.T) {
	for _, impl := range analysisStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant, other := newTestTenant(), newTestTenant()
			defer cleanup(tenant)
			defer cleanup(other)

			if _, err := st.get(ctx, tenant, "01MISSINGNOTFOUND000000000"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
				t.Errorf("get missing error = %v, want ErrNotFound", err)
			}

			a := sampleAnalysis()
			if err := st.put(ctx, tenant, a); err != nil {
				t.Fatalf("put failed: %v", err)
			}
			if _, err := st.get(ctx, other, a.ID); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
				t.Errorf("跨租户 get error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestAnalysisStore_listSortsAndScopesByTenant(t *testing.T) {
	for _, impl := range analysisStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant, other := newTestTenant(), newTestTenant()
			defer cleanup(tenant)
			defer cleanup(other)

			// 空租户必须返回空切片而不是 nil（前端 .map() 遇 null 会崩）。
			empty, err := st.list(ctx, tenant)
			if err != nil {
				t.Fatalf("list failed: %v", err)
			}
			if empty == nil || len(empty) != 0 {
				t.Errorf("空租户 list = %#v, want 非 nil 空切片", empty)
			}

			base := time.Now().UTC().Truncate(time.Microsecond)
			// 同一 CreatedAt 的两条：按 ID 升序打破平局，保证顺序稳定。
			first := sampleAnalysis()
			first.ID, first.CreatedAt = "01AAA0000000000000000000AA", base
			second := sampleAnalysis()
			second.ID, second.CreatedAt = "01BBB0000000000000000000BB", base
			third := sampleAnalysis()
			third.ID, third.CreatedAt = "01CCC0000000000000000000CC", base.Add(time.Minute)
			foreign := sampleAnalysis()
			foreign.ID, foreign.CreatedAt = "01DDD0000000000000000000DD", base

			for _, a := range []*AnalysisResult{third, first, foreign, second} {
				id := tenant
				if a == foreign {
					id = other
				}
				if err := st.put(ctx, id, a); err != nil {
					t.Fatalf("put(%s) failed: %v", a.ID, err)
				}
			}

			got, err := st.list(ctx, tenant)
			if err != nil {
				t.Fatalf("list failed: %v", err)
			}
			if len(got) != 3 {
				t.Fatalf("list len = %d, want 3（不含其他租户）", len(got))
			}
			wantOrder := []string{first.ID, second.ID, third.ID}
			for i, want := range wantOrder {
				if got[i].ID != want {
					t.Errorf("list[%d].ID = %q, want %q（created_at, id 升序）", i, got[i].ID, want)
				}
			}
			others, err := st.list(ctx, other)
			if err != nil {
				t.Fatal(err)
			}
			if len(others) != 1 || others[0].ID != foreign.ID {
				t.Errorf("other 租户 list = %+v, want 仅 %s", others, foreign.ID)
			}
		})
	}
}

func TestAnalysisStore_mutateAppliesAndPersists(t *testing.T) {
	for _, impl := range analysisStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant, other := newTestTenant(), newTestTenant()
			defer cleanup(tenant)
			defer cleanup(other)

			a := sampleAnalysis()
			if err := st.put(ctx, tenant, a); err != nil {
				t.Fatalf("put failed: %v", err)
			}

			finished := time.Now().UTC().Truncate(time.Microsecond)
			err := st.mutate(ctx, tenant, a.ID, func(x *AnalysisResult) error {
				x.State = StateCompleted
				x.Progress = 100
				x.FinishedAt = finished
				x.Sentiments = append(x.Sentiments, Sentiment{DocumentID: "doc-2", Sentiment: "positive"})
				return nil
			})
			if err != nil {
				t.Fatalf("mutate failed: %v", err)
			}
			got, err := st.get(ctx, tenant, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StateCompleted || got.Progress != 100 {
				t.Errorf("State/Progress = %q/%d, want completed/100", got.State, got.Progress)
			}
			if !got.FinishedAt.Equal(finished) {
				t.Errorf("FinishedAt = %v, want %v", got.FinishedAt, finished)
			}
			if len(got.Sentiments) != 2 {
				t.Errorf("Sentiments len = %d, want 2（mutate 的改动必须落库）", len(got.Sentiments))
			}

			// 不存在的 ID / 跨租户 → ErrNotFound（与 memoryStore 一致）
			for _, tc := range []struct {
				name     string
				tenantID string
				id       string
			}{
				{"missing", tenant, "01MISSINGNOTFOUND000000000"},
				{"other tenant", other, a.ID},
			} {
				err := st.mutate(ctx, tc.tenantID, tc.id, func(*AnalysisResult) error { return nil })
				if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
					t.Errorf("%s: mutate error = %v, want ErrNotFound", tc.name, err)
				}
			}

			// 回调返回的错误原样透出（service 依赖它把状态机冲突报成 ErrConflict），
			// 且此时不写入任何改动。
			conflict := pkgerrors.Wrap(pkgerrors.ErrConflict, "analysis cannot transition from completed to fetching")
			err = st.mutate(ctx, tenant, a.ID, func(x *AnalysisResult) error {
				if x.State == StateCompleted {
					return conflict
				}
				x.Name = "不该写入"
				return nil
			})
			if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
				t.Fatalf("mutate 回调错误 = %v, want ErrConflict", err)
			}
			got, err = st.get(ctx, tenant, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != a.Name {
				t.Errorf("回调报错后 Name = %q, want %q（不应写入）", got.Name, a.Name)
			}
		})
	}
}

// TestAnalysisStore_mutateSerializesConcurrentUpdates 证明 mutate 的读-改-写
// 互斥：并发自增不丢更新（pg 版靠 SELECT … FOR UPDATE，内存版靠写锁）。
func TestAnalysisStore_mutateSerializesConcurrentUpdates(t *testing.T) {
	for _, impl := range analysisStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			a := sampleAnalysis()
			a.Progress = 0
			if err := st.put(ctx, tenant, a); err != nil {
				t.Fatalf("put failed: %v", err)
			}

			const goroutines = 16
			var wg sync.WaitGroup
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := st.mutate(ctx, tenant, a.ID, func(x *AnalysisResult) error {
						x.Progress++
						return nil
					}); err != nil {
						t.Errorf("并发 mutate failed: %v", err)
					}
				}()
			}
			wg.Wait()

			got, err := st.get(ctx, tenant, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Progress != goroutines {
				t.Errorf("Progress = %d, want %d（存在丢失更新）", got.Progress, goroutines)
			}
		})
	}
}

// TestPGStore_listOmitsReportContent 锁定 pg 版 list 的刻意差异：
// 列表不取 KB 级正文（详情页按秒轮询 /analyses/:id），get 必须取全。
func TestPGStore_listOmitsReportContent(t *testing.T) {
	pool := pgTestPool(t)
	st := newPGStore(pool)
	tenant := newTestTenant()
	defer cleanupAnalyses(t, pool, tenant)
	ctx := context.Background()

	a := sampleAnalysis()
	a.ReportContent = "<html>" + strings.Repeat("y", 8192) + "</html>"
	if err := st.put(ctx, tenant, a); err != nil {
		t.Fatalf("put failed: %v", err)
	}

	// /analyses/:id/result 经 get 取正文，必须完整。
	got, err := st.get(ctx, tenant, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReportContent != a.ReportContent {
		t.Errorf("get ReportContent = %d 字节, want %d 字节", len(got.ReportContent), len(a.ReportContent))
	}

	list, err := st.list(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].ReportContent != "" {
		t.Errorf("list 不应返回 report_content，got %d 字节", len(list[0].ReportContent))
	}
	// 其余字段照常返回，否则列表页会缺数据。
	if list[0].Name != a.Name || list[0].DocCount != a.DocCount || !reflect.DeepEqual(list[0].Keywords, a.Keywords) {
		t.Errorf("list 行数据不完整: %+v", list[0])
	}
}
