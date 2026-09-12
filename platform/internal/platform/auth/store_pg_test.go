package auth

import (
	"context"
	"testing"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/id"
	"github.com/yuging/platform/internal/pkg/pgtest"
)

// authStoreContract 是 auth.Store 的行为契约，参数化到任意实现上执行。
//
// 断言取自内存版既有测试（service_test.go 的
// TestServiceRegister_createsUserTenantMemberQuotaAndTokens）：
// 注册链路对 store 的全部依赖 —— 用户/租户/成员写入、按 email 找回、
// 成员关系解析角色、以及未找到/重复时的 sentinel 错误。
//
// 同一个契约同时跑在 MemoryStore 与 PGStore 上：内存版证明契约本身有效
// （本地无 PG 也能跑），PG 版证明 pgx 实现与内存版行为一致。
func authStoreContract(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("CreateUser then GetUserByEmail 往返一致", func(t *testing.T) {
		st := newStore(t)
		want := User{
			ID:           id.New(),
			Email:        "alice@example.com",
			PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$hash",
			Name:         "Alice",
		}
		if err := st.CreateUser(ctx, want); err != nil {
			t.Fatalf("CreateUser failed: %v", err)
		}

		got, err := st.GetUserByEmail(ctx, want.Email)
		if err != nil {
			t.Fatalf("GetUserByEmail failed: %v", err)
		}
		if *got != want {
			t.Errorf("user = %+v, want %+v", *got, want)
		}
	})

	t.Run("GetUserByEmail 未注册邮箱 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.GetUserByEmail(ctx, "nobody@example.com"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("CreateUser 重复邮箱 → ErrConflict", func(t *testing.T) {
		st := newStore(t)
		u := User{ID: id.New(), Email: "dup@example.com", PasswordHash: "h", Name: "Dup"}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatalf("first CreateUser failed: %v", err)
		}
		other := User{ID: id.New(), Email: "dup@example.com", PasswordHash: "h2", Name: "Other"}
		if err := st.CreateUser(ctx, other); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})

	// 读操作必须返回副本：调用方改返回值不能影响已存的行。
	t.Run("GetUserByEmail 返回副本", func(t *testing.T) {
		st := newStore(t)
		u := User{ID: id.New(), Email: "copy@example.com", PasswordHash: "h", Name: "Original"}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}

		got, err := st.GetUserByEmail(ctx, u.Email)
		if err != nil {
			t.Fatal(err)
		}
		got.Name = "Mutated"

		again, err := st.GetUserByEmail(ctx, u.Email)
		if err != nil {
			t.Fatal(err)
		}
		if again.Name != "Original" {
			t.Errorf("stored name = %q, want Original（返回值必须是副本）", again.Name)
		}
	})

	t.Run("CreateMember then GetUserTenant 返回该租户", func(t *testing.T) {
		st := newStore(t)
		userID, tenantID := id.New(), id.New()
		if err := st.CreateUser(ctx, User{ID: userID, Email: "owner@example.com", PasswordHash: "h", Name: "Owner"}); err != nil {
			t.Fatal(err)
		}
		want := Tenant{
			ID:       tenantID,
			Name:     "Owner的团队",
			Slug:     "t-" + tenantID,
			DBName:   "yuging_t_" + tenantID,
			Status:   "active",
			PlanCode: "free",
		}
		if err := st.CreateTenant(ctx, want); err != nil {
			t.Fatalf("CreateTenant failed: %v", err)
		}
		if err := st.CreateMember(ctx, Member{TenantID: tenantID, UserID: userID, Role: "tenant_admin"}); err != nil {
			t.Fatalf("CreateMember failed: %v", err)
		}

		got, err := st.GetUserTenant(ctx, userID)
		if err != nil {
			t.Fatalf("GetUserTenant failed: %v", err)
		}
		if *got != want {
			t.Errorf("tenant = %+v, want %+v", *got, want)
		}

		role, err := st.GetUserRole(ctx, tenantID, userID)
		if err != nil {
			t.Fatalf("GetUserRole failed: %v", err)
		}
		if role != "tenant_admin" {
			t.Errorf("role = %q, want tenant_admin", role)
		}
	})

	t.Run("GetUserTenant 无成员关系 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.GetUserTenant(ctx, id.New()); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("GetUserRole 无成员关系 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.GetUserRole(ctx, id.New(), id.New()); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("CreateTenant 重复 ID → ErrConflict", func(t *testing.T) {
		st := newStore(t)
		tn := Tenant{ID: id.New(), Name: "n", Slug: "t-dup", DBName: "yuging_t_dup", Status: "active", PlanCode: "free"}
		if err := st.CreateTenant(ctx, tn); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateTenant(ctx, tn); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("CreateMember 重复成员关系 → ErrConflict", func(t *testing.T) {
		st := newStore(t)
		userID, tenantID := id.New(), id.New()
		if err := st.CreateUser(ctx, User{ID: userID, Email: "m@example.com", PasswordHash: "h", Name: "M"}); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateTenant(ctx, Tenant{ID: tenantID, Name: "n", Slug: "t-m", DBName: "yuging_t_m", Status: "active", PlanCode: "free"}); err != nil {
			t.Fatal(err)
		}
		m := Member{TenantID: tenantID, UserID: userID, Role: "tenant_admin"}
		if err := st.CreateMember(ctx, m); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateMember(ctx, m); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
}

// 本地（无 YUGING_TEST_PG_URL）也有信号：契约本身必须被内存版满足。
func TestAuthStore_Memory_satisfiesContract(t *testing.T) {
	authStoreContract(t, func(t *testing.T) Store { return NewMemoryStore() })
}

func TestAuthStore_PG_satisfiesContract(t *testing.T) {
	pool := pgtest.Pool(t, "auth")
	authStoreContract(t, func(t *testing.T) Store { return NewPGStore(pool) })
}

// ── PG 专有：内存版没有对应行为，单独断言并记录差异 ──────────────

// 持久化才是本轮改造的目的：换一个 store 实例（模拟进程重启）仍能读回数据。
func TestAuthStore_PG_survivesNewInstance(t *testing.T) {
	pool := pgtest.Pool(t, "auth")
	ctx := context.Background()

	userID, tenantID := id.New(), id.New()
	first := NewPGStore(pool)
	if err := first.CreateUser(ctx, User{ID: userID, Email: "persist@example.com", PasswordHash: "h", Name: "P"}); err != nil {
		t.Fatal(err)
	}
	if err := first.CreateTenant(ctx, Tenant{ID: tenantID, Name: "n", Slug: "t-p", DBName: "yuging_t_p", Status: "active", PlanCode: "free"}); err != nil {
		t.Fatal(err)
	}
	if err := first.CreateMember(ctx, Member{TenantID: tenantID, UserID: userID, Role: "tenant_admin"}); err != nil {
		t.Fatal(err)
	}

	second := NewPGStore(pool) // 新实例 = 模拟重启后的新进程
	u, err := second.GetUserByEmail(ctx, "persist@example.com")
	if err != nil {
		t.Fatalf("重启后读用户失败: %v", err)
	}
	if u.ID != userID || u.Name != "P" {
		t.Errorf("user = %+v, want id %s name P", *u, userID)
	}
	tn, err := second.GetUserTenant(ctx, userID)
	if err != nil {
		t.Fatalf("重启后读租户失败: %v", err)
	}
	if tn.ID != tenantID || tn.PlanCode != "free" {
		t.Errorf("tenant = %+v, want id %s plan free", *tn, tenantID)
	}
}

// users.email 是 CITEXT：PG 版按大小写不敏感匹配；
// 内存版是精确匹配（大小写规范化在 service 层做）。
func TestAuthStore_PG_emailLookupIsCaseInsensitive(t *testing.T) {
	pool := pgtest.Pool(t, "auth")
	ctx := context.Background()

	st := NewPGStore(pool)
	if err := st.CreateUser(ctx, User{ID: id.New(), Email: "alice@example.com", PasswordHash: "h", Name: "Alice"}); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetUserByEmail(ctx, "ALICE@Example.COM")
	if err != nil {
		t.Fatalf("CITEXT 大小写不敏感查询失败: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("Name = %q, want Alice", got.Name)
	}
}

// 成员关系的两个外键都指向已存在的行：悬空引用映射为 ErrNotFound
// （内存版无外键，允许写入孤儿成员 —— 见实现注释）。
func TestAuthStore_PG_memberWithUnknownRefsIsNotFound(t *testing.T) {
	pool := pgtest.Pool(t, "auth")
	ctx := context.Background()

	st := NewPGStore(pool)
	userID, tenantID := id.New(), id.New()
	if err := st.CreateUser(ctx, User{ID: userID, Email: "fk@example.com", PasswordHash: "h", Name: "F"}); err != nil {
		t.Fatal(err)
	}

	t.Run("未知租户", func(t *testing.T) {
		err := st.CreateMember(ctx, Member{TenantID: id.New(), UserID: userID, Role: "analyst"})
		if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("未知用户", func(t *testing.T) {
		if err := st.CreateTenant(ctx, Tenant{ID: tenantID, Name: "n", Slug: "t-fk", DBName: "yuging_t_fk", Status: "active", PlanCode: "free"}); err != nil {
			t.Fatal(err)
		}
		err := st.CreateMember(ctx, Member{TenantID: tenantID, UserID: id.New(), Role: "analyst"})
		if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// tenants 表对 slug 与 db_name 也有唯一约束：冲突同样映射为 ErrConflict。
func TestAuthStore_PG_duplicateSlugOrDBNameIsConflict(t *testing.T) {
	pool := pgtest.Pool(t, "auth")
	ctx := context.Background()
	st := NewPGStore(pool)

	if err := st.CreateTenant(ctx, Tenant{ID: id.New(), Name: "a", Slug: "t-same", DBName: "yuging_t_one", Status: "active", PlanCode: "free"}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		tn   Tenant
	}{
		{"重复 slug", Tenant{ID: id.New(), Name: "b", Slug: "t-same", DBName: "yuging_t_two", Status: "active", PlanCode: "free"}},
		{"重复 db_name", Tenant{ID: id.New(), Name: "c", Slug: "t-other", DBName: "yuging_t_one", Status: "active", PlanCode: "free"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := st.CreateTenant(ctx, tt.tn); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
				t.Errorf("err = %v, want ErrConflict", err)
			}
		})
	}
}
