package tenant

import (
	"context"
	"testing"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/id"
	"github.com/yuging/platform/internal/pkg/pgtest"
)

// tenantStoreContract 是 tenant.Store 的行为契约，参数化到任意实现上执行。
//
// 断言取自内存版既有测试（service_test.go 的
// TestTenantService_Get_found / _List_returnsCreatedTenants /
// TestTenantMemoryStore_Create_conflictingID / _UpdateStatus_notFound）。
func tenantStoreContract(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	// seed 用 ULID 建 ID：PG 版按 (created_at, id) 排序，
	// 同一毫秒内建的行靠 id 兜底 —— 与内存版的插入顺序一致。
	seed := func(t *testing.T, st Store, name string) Tenant {
		t.Helper()
		tenantID := id.New()
		tn := Tenant{
			ID:       tenantID,
			Name:     name,
			Slug:     "t-" + tenantID,
			DBName:   DBName(tenantID),
			Status:   StatusActive,
			PlanCode: "free",
		}
		if err := st.Create(ctx, tn); err != nil {
			t.Fatalf("seed tenant %q failed: %v", name, err)
		}
		return tn
	}

	t.Run("Create then Get 往返一致", func(t *testing.T) {
		st := newStore(t)
		want := seed(t, st, "alpha")

		got, err := st.Get(ctx, want.ID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if *got != want {
			t.Errorf("tenant = %+v, want %+v", *got, want)
		}
		if got.Status != StatusActive {
			t.Errorf("Status = %q, want active", got.Status)
		}
		if got.DBName != DBName(want.ID) {
			t.Errorf("DBName = %q, want %q", got.DBName, DBName(want.ID))
		}
	})

	t.Run("Get 未找到 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.Get(ctx, id.New()); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	// 空列表必须是长度 0 的切片而不是 nil：JSON 序列化成 [] 而不是 null，
	// 前端 .map() 遇 null 会崩（见 CLAUDE.md）。
	t.Run("List 空 → 非 nil 空切片", func(t *testing.T) {
		st := newStore(t)
		got, err := st.List(ctx)
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
		st := newStore(t)
		for _, name := range []string{"one", "two", "three"} {
			seed(t, st, name)
		}

		got, err := st.List(ctx)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("len(List) = %d, want 3", len(got))
		}
		wantNames := []string{"one", "two", "three"}
		for i, tn := range got {
			if tn.Name != wantNames[i] {
				t.Errorf("List[%d].Name = %q, want %q", i, tn.Name, wantNames[i])
			}
		}
	})

	t.Run("List 返回副本", func(t *testing.T) {
		st := newStore(t)
		seed(t, st, "copy")

		first, err := st.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		first[0].Name = "Mutated"

		again, err := st.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if again[0].Name != "copy" {
			t.Errorf("stored name = %q, want copy（返回值必须是副本）", again[0].Name)
		}
	})

	t.Run("Create 重复 ID → ErrConflict", func(t *testing.T) {
		st := newStore(t)
		tn := seed(t, st, "dup")
		if err := st.Create(ctx, tn); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("UpdateStatus 生效", func(t *testing.T) {
		st := newStore(t)
		tn := seed(t, st, "suspendable")

		if err := st.UpdateStatus(ctx, tn.ID, StatusSuspended); err != nil {
			t.Fatalf("UpdateStatus failed: %v", err)
		}
		got, err := st.Get(ctx, tn.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusSuspended {
			t.Errorf("Status = %q, want suspended", got.Status)
		}

		if err := st.UpdateStatus(ctx, tn.ID, StatusActive); err != nil {
			t.Fatalf("UpdateStatus(resume) failed: %v", err)
		}
		got, err = st.Get(ctx, tn.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != StatusActive {
			t.Errorf("Status = %q, want active", got.Status)
		}
	})

	t.Run("UpdateStatus 未找到 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if err := st.UpdateStatus(ctx, id.New(), StatusActive); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// 本地（无 YUGING_TEST_PG_URL）也有信号：契约本身必须被内存版满足。
func TestTenantStore_Memory_satisfiesContract(t *testing.T) {
	tenantStoreContract(t, func(t *testing.T) Store { return NewMemoryStore() })
}

func TestTenantStore_PG_satisfiesContract(t *testing.T) {
	pool := pgtest.Pool(t, "tenant")
	tenantStoreContract(t, func(t *testing.T) Store { return NewPGStore(pool) })
}

// ── PG 专有 ────────────────────────────────────────────────────

// 持久化：换实例（模拟重启）后数据仍在，且顺序不变。
func TestTenantStore_PG_survivesNewInstance(t *testing.T) {
	pool := pgtest.Pool(t, "tenant")
	ctx := context.Background()

	first := NewPGStore(pool)
	want := Tenant{
		ID:       id.New(),
		Name:     "持久化租户",
		Slug:     "t-persist",
		DBName:   "yuging_t_persist",
		Status:   StatusActive,
		PlanCode: "pro",
	}
	if err := first.Create(ctx, want); err != nil {
		t.Fatal(err)
	}

	got, err := NewPGStore(pool).Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("重启后读租户失败: %v", err)
	}
	if *got != want {
		t.Errorf("tenant = %+v, want %+v", *got, want)
	}

	// suspend 的结果同样要落库。
	if err := first.UpdateStatus(ctx, want.ID, StatusSuspended); err != nil {
		t.Fatal(err)
	}
	after, err := NewPGStore(pool).Get(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != StatusSuspended {
		t.Errorf("重启后 Status = %q, want suspended", after.Status)
	}
}
