package analysis

import (
	"context"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/queue"
)

// cleanupDocuments 删除某租户的采集文档（pg 用例收尾用）。
func cleanupDocuments(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM raw_documents WHERE tenant_id = $1`, tenantID); err != nil {
		t.Errorf("清理 raw_documents 失败: %v", err)
	}
}

// documentStoreImpl 是一个待测文档 store 实现。
type documentStoreImpl struct {
	name  string
	build func(t *testing.T) (documentStore, func(tenantID string))
}

// documentStoreImpls 返回当前环境可测的实现（内存版恒可用，pg 版需 env）。
func documentStoreImpls(t *testing.T) []documentStoreImpl {
	t.Helper()
	impls := []documentStoreImpl{{
		name: "memory",
		build: func(*testing.T) (documentStore, func(string)) {
			return newMemoryDocumentStore(), func(string) {}
		},
	}}
	if os.Getenv(pgTestEnv) == "" {
		t.Logf("%s 未设置：只跑内存实现", pgTestEnv)
		return impls
	}
	impls = append(impls, documentStoreImpl{
		name: "postgres",
		build: func(t *testing.T) (documentStore, func(string)) {
			pool := pgTestPool(t)
			return newPGDocumentStore(pool), func(tenantID string) {
				cleanupDocuments(t, pool, tenantID)
			}
		},
	})
	return impls
}

// sampleDocuments 返回三条采集结果（含 RFC3339 时间与非标准时间）。
func sampleDocuments() []Document {
	return []Document{
		{
			ID: "doc-a", Title: "雅阁后排实测：空间够用吗", URL: "https://example.com/a",
			Content: "后排腿部空间…", Author: "张三", SourceType: "news", SourceName: "汽车之家",
			PublishedAt: "2026-08-01T10:00:00Z", ContentHash: "hash-a",
		},
		{
			ID: "doc-b", Title: "车主吐槽后备箱", URL: "https://example.com/b",
			Content: "后备箱开口偏小…", Author: "李四", SourceType: "weibo", SourceName: "微博",
			PublishedAt: "2026-08-02T12:30:00+08:00", ContentHash: "hash-b",
		},
		{
			ID: "doc-c", Title: "无时间来源", URL: "https://example.com/c",
			Content: "抓取正文失败，退回 Bocha snippet…", SourceType: "news", SourceName: "新浪",
			ContentHash: "hash-c",
		},
	}
}

func TestDocumentStore_addListCount(t *testing.T) {
	for _, impl := range documentStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			want := sampleDocuments()
			if err := st.add(ctx, tenant, "analysis-1", want); err != nil {
				t.Fatalf("add failed: %v", err)
			}

			got, err := st.list(ctx, tenant, "analysis-1")
			if err != nil {
				t.Fatalf("list failed: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("list len = %d, want %d", len(got), len(want))
			}
			// 存储顺序不保证，按 ID 归一后逐字段比对。
			// PublishedAt 单独比：TIMESTAMPTZ 读回统一为 UTC 表示
			// （+08:00 与 Z 是同一时刻，字符串形式不同）。
			sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
			for i := range want {
				g, w := got[i], want[i]
				if !samePublishedAt(g.PublishedAt, w.PublishedAt) {
					t.Errorf("doc[%d] PublishedAt = %q, want %q", i, g.PublishedAt, w.PublishedAt)
				}
				g.PublishedAt, w.PublishedAt = "", ""
				if !reflect.DeepEqual(g, w) {
					t.Errorf("doc[%d] = %+v, want %+v", i, g, w)
				}
			}

			n, err := st.count(ctx, tenant, "analysis-1")
			if err != nil {
				t.Fatalf("count failed: %v", err)
			}
			if n != len(want) {
				t.Errorf("count = %d, want %d", n, len(want))
			}
		})
	}
}

func TestDocumentStore_emptyReturnsEmptySliceAndZero(t *testing.T) {
	for _, impl := range documentStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			// Service.Documents 依赖「空结果 = 空切片而非 nil」（前端 .map() 会崩）
			got, err := st.list(ctx, tenant, "analysis-missing")
			if err != nil {
				t.Fatalf("list failed: %v", err)
			}
			if got == nil {
				t.Error("list 空结果返回 nil，期望空切片")
			}
			if len(got) != 0 {
				t.Errorf("list len = %d, want 0", len(got))
			}
			if n, err := st.count(ctx, tenant, "analysis-missing"); err != nil || n != 0 {
				t.Errorf("count = %d/%v, want 0/nil", n, err)
			}
			// 空批次写入是 no-op（引擎可能返回 0 条）。
			if err := st.add(ctx, tenant, "analysis-missing", nil); err != nil {
				t.Errorf("add(nil) failed: %v", err)
			}
		})
	}
}

func TestDocumentStore_isolatedByTenantAndAnalysis(t *testing.T) {
	for _, impl := range documentStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant, other := newTestTenant(), newTestTenant()
			defer cleanup(tenant)
			defer cleanup(other)

			docs := sampleDocuments()
			if err := st.add(ctx, tenant, "analysis-1", docs[:2]); err != nil {
				t.Fatal(err)
			}
			if err := st.add(ctx, tenant, "analysis-2", docs[2:]); err != nil {
				t.Fatal(err)
			}
			if err := st.add(ctx, other, "analysis-1", docs[:1]); err != nil {
				t.Fatal(err)
			}

			// 同租户不同分析互不串味
			if n, err := st.count(ctx, tenant, "analysis-1"); err != nil || n != 2 {
				t.Errorf("analysis-1 count = %d/%v, want 2", n, err)
			}
			if n, err := st.count(ctx, tenant, "analysis-2"); err != nil || n != 1 {
				t.Errorf("analysis-2 count = %d/%v, want 1", n, err)
			}
			// 跨租户读不到
			if n, err := st.count(ctx, other, "analysis-2"); err != nil || n != 0 {
				t.Errorf("跨租户 count = %d/%v, want 0", n, err)
			}
			got, err := st.list(ctx, other, "analysis-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].ID != "doc-a" {
				t.Errorf("跨租户 list = %+v, want 仅 doc-a", got)
			}
		})
	}
}

func TestDocumentStore_publishedAtRoundTrip(t *testing.T) {
	for _, impl := range documentStoreImpls(t) {
		t.Run(impl.name, func(t *testing.T) {
			ctx := context.Background()
			st, cleanup := impl.build(t)
			tenant := newTestTenant()
			defer cleanup(tenant)

			docs := sampleDocuments()
			if err := st.add(ctx, tenant, "analysis-1", docs); err != nil {
				t.Fatal(err)
			}
			got, err := st.list(ctx, tenant, "analysis-1")
			if err != nil {
				t.Fatal(err)
			}
			byID := make(map[string]Document, len(got))
			for _, d := range got {
				byID[d.ID] = d
			}

			// 带 Z 的 RFC3339 原样往返（两种实现都存得住）
			if ts := byID["doc-a"].PublishedAt; ts != "2026-08-01T10:00:00Z" {
				t.Errorf("doc-a PublishedAt = %q, want 2026-08-01T10:00:00Z", ts)
			}
			// 带 +08:00 的偏移：内存版原样返回字符串，pg 版（TIMESTAMPTZ）返回
			// 同一时刻的 UTC 表示 —— 契约只要求「时刻不变」，具体写法见
			// TestPGDocumentStore_publishedAtNormalizedToUTC。
			wantInstant, err := time.Parse(time.RFC3339, "2026-08-02T12:30:00+08:00")
			if err != nil {
				t.Fatalf("测试数据时间非法: %v", err)
			}
			gotInstant, err := time.Parse(time.RFC3339, byID["doc-b"].PublishedAt)
			if err != nil {
				t.Errorf("doc-b PublishedAt = %q, 无法按 RFC3339 解析: %v", byID["doc-b"].PublishedAt, err)
			} else if !gotInstant.Equal(wantInstant) {
				t.Errorf("doc-b PublishedAt = %q（%v）, want 时刻 %v", byID["doc-b"].PublishedAt, gotInstant, wantInstant)
			}
			// 空时间保持空字符串（不是零值时间串）
			if ts := byID["doc-c"].PublishedAt; ts != "" {
				t.Errorf("doc-c PublishedAt = %q, want 空串", ts)
			}
		})
	}
}

// TestPGDocumentStore_publishedAtNormalizedToUTC 锁定 pg 版的时间表示：
// TIMESTAMPTZ 存绝对时刻，带偏移的输入读回统一成 UTC 的 Z 形式
// （内存版则原样保留 "2026-08-02T12:30:00+08:00" 字符串）。
func TestPGDocumentStore_publishedAtNormalizedToUTC(t *testing.T) {
	pool := pgTestPool(t)
	st := newPGDocumentStore(pool)
	tenant := newTestTenant()
	defer cleanupDocuments(t, pool, tenant)
	ctx := context.Background()

	docs := []Document{{ID: "doc-offset", Title: "带偏移", PublishedAt: "2026-08-02T12:30:00+08:00"}}
	if err := st.add(ctx, tenant, "analysis-1", docs); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	got, err := st.list(ctx, tenant, "analysis-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("list len = %d, want 1", len(got))
	}
	if got[0].PublishedAt != "2026-08-02T04:30:00Z" {
		t.Errorf("PublishedAt = %q, want 2026-08-02T04:30:00Z", got[0].PublishedAt)
	}
}

// TestPGDocumentStore_publishedAtNonStandardDropped 锁定 pg 版对非标准时间串的
// 处理：TIMESTAMPTZ 无法无损容纳任意字符串，解析失败即存 NULL（读回空串）。
func TestPGDocumentStore_publishedAtNonStandardDropped(t *testing.T) {
	pool := pgTestPool(t)
	st := newPGDocumentStore(pool)
	tenant := newTestTenant()
	defer cleanupDocuments(t, pool, tenant)
	ctx := context.Background()

	docs := []Document{
		{ID: "doc-bad", Title: "时间格式不标准", PublishedAt: "2026年8月1日 10:00", ContentHash: "h1"},
		{ID: "doc-date-only", Title: "只有日期", PublishedAt: "2026-08-01", ContentHash: "h2"},
	}
	if err := st.add(ctx, tenant, "analysis-1", docs); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	got, err := st.list(ctx, tenant, "analysis-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("list len = %d, want 2（时间解析失败不得丢文档）", len(got))
	}
	for _, d := range got {
		if d.PublishedAt != "" {
			t.Errorf("%s PublishedAt = %q, want 空串（非 RFC3339 存 NULL）", d.ID, d.PublishedAt)
		}
		if d.Title == "" {
			t.Errorf("%s Title 丢失", d.ID)
		}
	}
}

// TestPGDocumentStore_duplicateIDStoredOnce 验证重复投递（Rerun 重跑同一批
// 采集结果）不会撑大文档数：raw_documents.id 是主键，冲突即跳过。
func TestPGDocumentStore_duplicateIDStoredOnce(t *testing.T) {
	pool := pgTestPool(t)
	st := newPGDocumentStore(pool)
	tenant := newTestTenant()
	defer cleanupDocuments(t, pool, tenant)
	ctx := context.Background()

	docs := sampleDocuments()
	if err := st.add(ctx, tenant, "analysis-1", docs); err != nil {
		t.Fatal(err)
	}
	if err := st.add(ctx, tenant, "analysis-1", docs); err != nil {
		t.Fatalf("重复 add failed: %v", err)
	}
	n, err := st.count(ctx, tenant, "analysis-1")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(docs) {
		t.Errorf("count = %d, want %d（重复 ID 只落一行）", n, len(docs))
	}
}

// TestPGDocumentStore_emptyIDGetsGenerated 锁定空 ID 兜底：引擎在正文抓取
// 失败时会给出空 ID（query_engine 用 content_hash 当 ID），而 id 是主键。
func TestPGDocumentStore_emptyIDGetsGenerated(t *testing.T) {
	pool := pgTestPool(t)
	st := newPGDocumentStore(pool)
	tenant := newTestTenant()
	defer cleanupDocuments(t, pool, tenant)
	ctx := context.Background()

	docs := []Document{
		{ID: "", Title: "空 ID 甲", ContentHash: ""},
		{ID: "", Title: "空 ID 乙", ContentHash: ""},
	}
	if err := st.add(ctx, tenant, "analysis-1", docs); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	got, err := st.list(ctx, tenant, "analysis-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("list len = %d, want 2（空 ID 不得互相覆盖）", len(got))
	}
	for _, d := range got {
		if d.ID == "" {
			t.Errorf("空 ID 未补: %+v", d)
		}
	}
	if got[0].ID == got[1].ID {
		t.Errorf("两条空 ID 文档拿到相同 ID %q", got[0].ID)
	}
}

// TestServiceWithPGStore_survivesServiceRebuild 端到端验证 pg store 的意义：
// 换一个 Service 实例（等价于进程重启）后任务与文档仍在。
func TestServiceWithPGStore_survivesServiceRebuild(t *testing.T) {
	pool := pgTestPool(t)
	tenant := newTestTenant()
	defer cleanupAnalyses(t, pool, tenant)
	defer cleanupDocuments(t, pool, tenant)
	ctx := context.Background()

	q := queue.NewMemory()
	defer func() { _ = q.Close() }()
	svc := NewServiceWithStore(q, 4, newPGStore(pool), newPGDocumentStore(pool))

	created, err := svc.Create(ctx, CreateAnalysisRequest{
		TenantID: tenant, UserID: "user-1", Name: "雅阁后排舆情",
		AnalysisType: "brand", Keywords: []string{"雅阁后排"}, Sources: []string{"news"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	docs := sampleDocuments()
	svc.AddDocuments(ctx, tenant, created.ID, docs)
	if err := svc.Transition(ctx, tenant, created.ID, "acquiring_budget"); err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	// 第二个实例：内存 store 在此会丢掉全部数据
	svc2 := NewServiceWithStore(queue.NewMemory(), 4, newPGStore(pool), newPGDocumentStore(pool))
	got, err := svc2.Get(ctx, tenant, created.ID)
	if err != nil {
		t.Fatalf("重建后 Get failed: %v", err)
	}
	if got.Name != created.Name || got.State != StateAcquiringBudget {
		t.Errorf("重建后 = %+v, want name=%q state=acquiring_budget", got, created.Name)
	}
	if len(got.Keywords) != 1 || got.Keywords[0] != "雅阁后排" {
		t.Errorf("重建后 Keywords = %v, want [雅阁后排]", got.Keywords)
	}

	storedDocs := svc2.Documents(ctx, tenant, created.ID)
	if len(storedDocs) != len(docs) {
		t.Errorf("重建后 Documents len = %d, want %d", len(storedDocs), len(docs))
	}
	if n := svc2.DocumentCount(ctx, tenant, created.ID); n != len(docs) {
		t.Errorf("重建后 DocumentCount = %d, want %d", n, len(docs))
	}

	list, err := svc2.List(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("重建后 List = %+v, want 仅本任务", list)
	}
}

// TestServiceWithStore_memoryStores 不依赖 PG：验证新构造函数与内存实现
// 装配后的行为与 NewService 一致。
func TestServiceWithStore_memoryStores(t *testing.T) {
	q := queue.NewMemory()
	defer func() { _ = q.Close() }()
	svc := NewServiceWithStore(q, 4, newMemoryStore(), newMemoryDocumentStore())
	ctx := context.Background()
	tenant := "t-" + id.New()

	created, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: tenant, Name: "雅阁后排舆情"})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := svc.Get(ctx, tenant, created.ID); err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	svc.AddDocuments(ctx, tenant, created.ID, sampleDocuments())
	if n := svc.DocumentCount(ctx, tenant, created.ID); n != 3 {
		t.Errorf("DocumentCount = %d, want 3", n)
	}
	if err := svc.Cancel(ctx, tenant, created.ID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	got, err := svc.Get(ctx, tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCanceled {
		t.Errorf("State = %q, want canceled", got.State)
	}
	if time.Since(got.FinishedAt) > time.Minute {
		t.Errorf("FinishedAt = %v, want 刚刚", got.FinishedAt)
	}
}

// samePublishedAt 比较两个 RFC3339 时间串是否代表同一时刻（容忍时区表示差异）。
func samePublishedAt(a, b string) bool {
	if a == b {
		return true
	}
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA != nil || errB != nil {
		return false
	}
	return ta.Equal(tb)
}
