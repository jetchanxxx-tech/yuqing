package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

const contractTenant = "tenant-1"

// alertStoreContract 是 alert.Store 的行为契约，参数化到任意实现上执行。
//
// 断言取自内存版既有测试（service_test.go 的
// TestServiceList_emptyOrderedAndIsolated 与 TestMemoryStore_Get_notFoundAndTrigger）。
//
// newStore 返回「服务于 contractTenant 的 store」：内存版是多租户容器（忽略
// 绑定参数），PG 版绑定到某个租户库 —— 这正是 database-per-tenant 的形状。
// 两者都必须满足的公共约束是：拿别家的 tenantID 问不到数据。
func alertStoreContract(t *testing.T, newStore func(t *testing.T, tenantID string) Store) {
	t.Helper()
	ctx := context.Background()

	newAlert := func(tenantID, name string) Alert {
		return Alert{
			ID:             id.New(),
			TenantID:       tenantID,
			Name:           name,
			RuleJSON:       `{"threshold": 0.3, "email": "ops@example.com"}`,
			Threshold:      0.3,
			RecipientEmail: "ops@example.com",
			CreatedAt:      time.Now().UTC(),
		}
	}

	t.Run("Create then Get 往返一致", func(t *testing.T) {
		st := newStore(t, contractTenant)
		want := newAlert(contractTenant, "负面占比过高")
		if err := st.Create(ctx, contractTenant, want); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := st.Get(ctx, contractTenant, want.ID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		// CreatedAt 逐字段比（PG 版见 TestAlertStore_PG_createdAtIsNotPersisted）。
		// RuleJSON 单独比语义：JSONB 落库会重排键序，字符串比较会误报。
		gotCopy, wantCopy := *got, want
		gotCopy.CreatedAt, wantCopy.CreatedAt = time.Time{}, time.Time{}
		gotCopy.RuleJSON, wantCopy.RuleJSON = canonicalJSON(got.RuleJSON), canonicalJSON(want.RuleJSON)
		if gotCopy != wantCopy {
			t.Errorf("alert = %+v, want %+v", gotCopy, wantCopy)
		}
		if got.Threshold != 0.3 {
			t.Errorf("Threshold = %v, want 0.3（从 rule_json 还原）", got.Threshold)
		}
		if got.RecipientEmail != "ops@example.com" {
			t.Errorf("RecipientEmail = %q, want ops@example.com（从 rule_json 还原）", got.RecipientEmail)
		}
	})

	t.Run("Get 未找到 → ErrNotFound", func(t *testing.T) {
		st := newStore(t, contractTenant)
		if _, err := st.Get(ctx, contractTenant, id.New()); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	// 拿别的租户 ID 访问：拿不到数据，也不泄漏「它存在」。
	t.Run("跨租户访问 → ErrNotFound", func(t *testing.T) {
		st := newStore(t, contractTenant)
		a := newAlert(contractTenant, "本租户告警")
		if err := st.Create(ctx, contractTenant, a); err != nil {
			t.Fatal(err)
		}

		if _, err := st.Get(ctx, "tenant-2", a.ID); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("Get(tenant-2): err = %v, want ErrNotFound", err)
		}
		if err := st.UpdateTrigger(ctx, "tenant-2", a.ID, time.Now()); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("UpdateTrigger(tenant-2): err = %v, want ErrNotFound", err)
		}
	})

	t.Run("List 空 → 非 nil 空切片", func(t *testing.T) {
		st := newStore(t, contractTenant)
		got, err := st.List(ctx, contractTenant)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if got == nil {
			t.Fatal("List() = nil, want empty non-nil slice")
		}
		if len(got) != 0 {
			t.Errorf("len(List) = %d, want 0", len(got))
		}
	})

	t.Run("List 按创建顺序返回", func(t *testing.T) {
		st := newStore(t, contractTenant)
		first := newAlert(contractTenant, "first")
		if err := st.Create(ctx, contractTenant, first); err != nil {
			t.Fatal(err)
		}
		// 间隔 2ms：ULID 时间戳是毫秒精度，同一毫秒内的顺序无保证
		// （PG 版按 ULID 排序，见 store_pg.go 注释）。
		time.Sleep(2 * time.Millisecond)
		second := newAlert(contractTenant, "second")
		if err := st.Create(ctx, contractTenant, second); err != nil {
			t.Fatal(err)
		}

		got, err := st.List(ctx, contractTenant)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("len(List) = %d, want 2", len(got))
		}
		if got[0].ID != first.ID || got[1].ID != second.ID {
			t.Errorf("order = [%s %s], want creation order", got[0].ID, got[1].ID)
		}
	})

	t.Run("UpdateTrigger 记录触发时间", func(t *testing.T) {
		st := newStore(t, contractTenant)
		a := newAlert(contractTenant, "trigger")
		if err := st.Create(ctx, contractTenant, a); err != nil {
			t.Fatal(err)
		}

		at := time.Now().UTC().Truncate(time.Millisecond)
		if err := st.UpdateTrigger(ctx, contractTenant, a.ID, at); err != nil {
			t.Fatalf("UpdateTrigger failed: %v", err)
		}
		got, err := st.Get(ctx, contractTenant, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.LastTriggeredAt.Equal(at) {
			t.Errorf("LastTriggeredAt = %s, want %s", got.LastTriggeredAt, at)
		}

		if err := st.UpdateTrigger(ctx, contractTenant, id.New(), at); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Create 重复 ID → ErrConflict", func(t *testing.T) {
		st := newStore(t, contractTenant)
		a := newAlert(contractTenant, "dup")
		if err := st.Create(ctx, contractTenant, a); err != nil {
			t.Fatal(err)
		}
		if err := st.Create(ctx, contractTenant, a); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
}

// 本地（无 YUQING_TEST_PG_URL）也有信号：契约本身必须被内存版满足。
func TestAlertStore_Memory_satisfiesContract(t *testing.T) {
	alertStoreContract(t, func(t *testing.T, tenantID string) Store { return NewMemoryStore() })
}

func TestAlertStore_PG_satisfiesContract(t *testing.T) {
	pool := pgtest.Pool(t, "alert", pgtest.PlatformMigrations)
	alertStoreContract(t, func(t *testing.T, tenantID string) Store {
		// 子测试共享同一 schema：构造前清掉上一子测试的残留（内存版天然隔离）
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM alerts WHERE tenant_id IN ('tenant-1', 'tenant-2')`); err != nil {
			t.Fatalf("pre-clean alerts: %v", err)
		}
		return NewPGStore(pool, tenantID)
	})
}

// ── PG 专有 ────────────────────────────────────────────────────

// List 也受租户绑定约束：拿别家的 tenantID 列不出数据，
// 而不是静默返回本库的行（否则一次租户 ID 传错就变成跨租户泄漏）。
func TestAlertStore_PG_listRejectsForeignTenant(t *testing.T) {
	pool := pgtest.Pool(t, "alert", pgtest.PlatformMigrations)
	ctx := context.Background()

	st := NewPGStore(pool, "tenant-1")
	if err := st.Create(ctx, "tenant-1", Alert{ID: id.New(), TenantID: "tenant-1", Name: "n", RuleJSON: `{"threshold":0.3,"email":"a@b.com"}`}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.List(ctx, "tenant-2"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("List(tenant-2): err = %v, want ErrNotFound", err)
	}
}

// alerts 表没有 created_at 列（迁移文件仅 id/name/rule_json/last_triggered_at/enabled），
// 因此 PG 版读回的 CreatedAt 是零值 —— 内存版保留 Service 传入的真实时间。
// 显式固定这个降级行为，它需要一条 ALTER TABLE（见交付说明）才能消除。
func TestAlertStore_PG_createdAtIsNotPersisted(t *testing.T) {
	pool := pgtest.Pool(t, "alert", pgtest.PlatformMigrations)
	ctx := context.Background()
	st := NewPGStore(pool, "tenant-1")

	a := Alert{
		ID:        id.New(),
		TenantID:  "tenant-1",
		Name:      "created-at",
		RuleJSON:  `{"threshold":0.3,"email":"a@b.com"}`,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.Create(ctx, "tenant-1", a); err != nil {
		t.Fatal(err)
	}

	got, err := st.Get(ctx, "tenant-1", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %s, want 零值（表无该列）", got.CreatedAt)
	}
}

// 持久化：换实例（模拟重启）后告警规则与触发时间仍在。
func TestAlertStore_PG_survivesNewInstance(t *testing.T) {
	pool := pgtest.Pool(t, "alert", pgtest.PlatformMigrations)
	ctx := context.Background()

	a := Alert{
		ID:             id.New(),
		TenantID:       "tenant-1",
		Name:           "负面占比过高",
		RuleJSON:       `{"threshold": 0.4, "email": "ops@example.com"}`,
		Threshold:      0.4,
		RecipientEmail: "ops@example.com",
		CreatedAt:      time.Now().UTC(),
	}
	if err := NewPGStore(pool, "tenant-1").Create(ctx, "tenant-1", a); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := NewPGStore(pool, "tenant-1").UpdateTrigger(ctx, "tenant-1", a.ID, at); err != nil {
		t.Fatal(err)
	}

	got, err := NewPGStore(pool, "tenant-1").Get(ctx, "tenant-1", a.ID)
	if err != nil {
		t.Fatalf("重启后读告警失败: %v", err)
	}
	if got.Name != a.Name || !jsonEqual(got.RuleJSON, a.RuleJSON) || got.Threshold != 0.4 {
		t.Errorf("alert = %+v, want 与写入一致", got)
	}
	if !got.LastTriggeredAt.Equal(at) {
		t.Errorf("LastTriggeredAt = %s, want %s", got.LastTriggeredAt, at)
	}
}

// Service 直连 PG store 的端到端：Create（校验 rule JSON）→ List → Check
// （超阈值触发 + 写回 last_triggered_at）。证明 store 满足 Service 的真实用法，
// 而不只是满足契约测试里手写的调用序列。
func TestServiceOverPGStore_checkFiresAndPersistsTrigger(t *testing.T) {
	pool := pgtest.Pool(t, "alert", pgtest.PlatformMigrations)
	ctx := context.Background()

	store := NewPGStore(pool, contractTenant)
	sender := &mockEmailSender{}
	svc := NewService(store, sender)

	a, err := svc.Create(ctx, contractTenant, "敏感", fmt.Sprintf(`{"threshold": %v, "email": %q}`, 0.2, "ops@example.com"))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := svc.Create(ctx, contractTenant, "迟钝", fmt.Sprintf(`{"threshold": %v, "email": %q}`, 0.9, "ops@example.com")); err != nil {
		t.Fatal(err)
	}

	triggered, err := svc.Check(ctx, contractTenant, 0.5)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(triggered) != 1 || triggered[0].ID != a.ID {
		t.Fatalf("triggered = %+v, want 只有 0.2 阈值的那条", triggered)
	}
	if calls := sender.snapshot(); len(calls) != 1 {
		t.Errorf("email calls = %d, want 1", len(calls))
	}

	// 触发时间必须落库，而不是只活在返回值里。
	got, err := store.Get(ctx, contractTenant, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastTriggeredAt.IsZero() {
		t.Error("last_triggered_at 未持久化")
	}
}


// canonicalJSON 把 JSON 串规范化（解析后重序列化），消除 JSONB 落库的键序差异。
func canonicalJSON(s string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return s
	}
	b, err := json.Marshal(m)
	if err != nil {
		return s
	}
	return string(b)
}

// jsonEqual 比较两个 JSON 串的语义是否相等。
func jsonEqual(a, b string) bool {
	return canonicalJSON(a) == canonicalJSON(b)
}
