package apikey

import (
	"context"
	"reflect"
	"testing"
	"time"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/id"
	"github.com/yuging/platform/internal/pkg/pgtest"
)

// apiKeyStoreContract 是 apikey.Store 的行为契约，参数化到任意实现上执行。
//
// 断言取自内存版既有测试（service_test.go 的
// TestAPIKey_ListKeys_scopedAndSafe / _includesRevokedWithTimestamp /
// TestAPIKey_RevokeKey / TestAPIKey_StoreGetByHash_notFound）。
func apiKeyStoreContract(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()
	ctx := context.Background()

	// newKey 模拟 Service.CreateKey 的产物：ULID 主键 + 同一时刻的 CreatedAt。
	// PG 版没有 created_at 列，按 ULID 时间戳还原创建时刻（毫秒精度）。
	newKey := func(tenantID, name string) *APIKey {
		return &APIKey{
			ID:         id.New(),
			TenantID:   tenantID,
			Name:       name,
			Scopes:     []string{"analyses:create"},
			Prefix:     "pangu_01ARZ3",
			CreatedAt:  time.Now().UTC().Truncate(time.Millisecond),
			keyHash:    "hash-" + id.New(),
			RevokedAt:  nil,
			LastUsedAt: nil,
		}
	}

	// compareExceptCreatedAt：PG 版的 CreatedAt 由主键时间戳还原（毫秒精度），
	// 与调用方传入值可能差 1ms，因此单独用容差断言，其余字段必须逐字段相等。
	compareExceptCreatedAt := func(t *testing.T, got, want *APIKey) {
		t.Helper()
		g, w := *got, *want
		g.CreatedAt, w.CreatedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("key = %+v, want %+v", g, w)
		}
	}

	t.Run("Create then GetByHash 往返一致", func(t *testing.T) {
		st := newStore(t)
		want := newKey("t1", "ci-bot")
		if err := st.Create(ctx, want); err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		got, err := st.GetByHash(ctx, want.keyHash)
		if err != nil {
			t.Fatalf("GetByHash failed: %v", err)
		}
		compareExceptCreatedAt(t, got, want)
		if got.CreatedAt.IsZero() {
			t.Error("CreatedAt 未还原（PG 版按 ULID 主键时间戳还原）")
		}
		if delta := time.Since(got.CreatedAt); delta < 0 || delta > time.Minute {
			t.Errorf("CreatedAt = %s, want 接近当前时间", got.CreatedAt)
		}
		if got.IsRevoked() {
			t.Error("新建的 key 不应是已撤销状态")
		}
	})

	t.Run("GetByHash 未知哈希 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.GetByHash(ctx, "deadbeef"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	// 返回副本：调用方改返回值不能影响已存的行。
	t.Run("GetByHash 返回副本", func(t *testing.T) {
		st := newStore(t)
		k := newKey("t1", "original")
		if err := st.Create(ctx, k); err != nil {
			t.Fatal(err)
		}

		got, err := st.GetByHash(ctx, k.keyHash)
		if err != nil {
			t.Fatal(err)
		}
		got.Name = "Mutated"

		again, err := st.GetByHash(ctx, k.keyHash)
		if err != nil {
			t.Fatal(err)
		}
		if again.Name != "original" {
			t.Errorf("Name = %q, want original（返回值必须是副本）", again.Name)
		}
	})

	// 顺序断言必须跨毫秒：ULID 的时间戳段是毫秒精度，同一毫秒内的随机尾段
	// 之间没有先后关系（id.New 用 crypto/rand 填尾段，不单调）。内存版按
	// CreatedAt 排序、PG 版按 ULID 排序，两者都只在「跨毫秒」时才等于插入顺序。
	// 这也是一个真实限制：同一毫秒内建的多把密钥，顺序不保证（表里没有
	// created_at 列可依赖）—— 建密钥是低频管理动作，实践中不会撞上。
	t.Run("List 按租户隔离并按创建顺序返回", func(t *testing.T) {
		st := newStore(t)
		first := newKey("t1", "first")
		if err := st.Create(ctx, first); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
		second := newKey("t1", "second")
		if err := st.Create(ctx, second); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
		third := newKey("t2", "other-tenant")
		if err := st.Create(ctx, third); err != nil {
			t.Fatal(err)
		}

		got, err := st.List(ctx, "t1")
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(List) = %d, want 2（跨租户泄漏会得到 3）", len(got))
		}
		if got[0].ID != first.ID || got[1].ID != second.ID {
			t.Errorf("order = [%s %s], want creation order", got[0].ID, got[1].ID)
		}

		empty, err := st.List(ctx, "t-unknown")
		if err != nil {
			t.Fatalf("List(unknown) failed: %v", err)
		}
		if empty == nil {
			t.Error("List() = nil, want empty non-nil slice（JSON 需为 []）")
		}
		if len(empty) != 0 {
			t.Errorf("len(List(unknown)) = %d, want 0", len(empty))
		}
	})

	t.Run("Revoke 只影响本租户且幂等", func(t *testing.T) {
		st := newStore(t)
		mine := newKey("t1", "mine")
		theirs := newKey("t2", "theirs")
		for _, k := range []*APIKey{mine, theirs} {
			if err := st.Create(ctx, k); err != nil {
				t.Fatal(err)
			}
		}

		if err := st.Revoke(ctx, "t1", id.New()); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("未知 key: err = %v, want ErrNotFound", err)
		}
		if err := st.Revoke(ctx, "t1", theirs.ID); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("跨租户 key: err = %v, want ErrNotFound（不得泄漏存在性）", err)
		}
		if err := st.Revoke(ctx, "t1", mine.ID); err != nil {
			t.Fatalf("Revoke failed: %v", err)
		}

		// 撤销后仍在列表中可见，且带 revoked_at。
		keys, err := st.List(ctx, "t1")
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0].RevokedAt == nil {
			t.Fatalf("revoked key 未在列表中带上 revoked_at: %+v", keys)
		}
		firstRevoked := *keys[0].RevokedAt

		// 重复撤销是幂等的，且保留首次撤销时间。
		if err := st.Revoke(ctx, "t1", mine.ID); err != nil {
			t.Fatalf("second Revoke failed: %v", err)
		}
		again, err := st.GetByHash(ctx, mine.keyHash)
		if err != nil {
			t.Fatal(err)
		}
		if again.RevokedAt == nil || !again.RevokedAt.Equal(firstRevoked) {
			t.Errorf("RevokedAt = %v, want %v（幂等：保留首次撤销时间）", again.RevokedAt, firstRevoked)
		}
		// 跨租户的 key 不受影响。
		untouched, err := st.GetByHash(ctx, theirs.keyHash)
		if err != nil {
			t.Fatal(err)
		}
		if untouched.IsRevoked() {
			t.Error("别的租户的 key 被误撤销了")
		}
	})

	t.Run("Touch 记录最后使用时间", func(t *testing.T) {
		st := newStore(t)
		k := newKey("t1", "touched")
		if err := st.Create(ctx, k); err != nil {
			t.Fatal(err)
		}

		at := time.Now().UTC().Truncate(time.Millisecond)
		if err := st.Touch(ctx, k.ID, at); err != nil {
			t.Fatalf("Touch failed: %v", err)
		}
		got, err := st.GetByHash(ctx, k.keyHash)
		if err != nil {
			t.Fatal(err)
		}
		if got.LastUsedAt == nil {
			t.Fatal("LastUsedAt 未写入")
		}
		if !got.LastUsedAt.Equal(at) {
			t.Errorf("LastUsedAt = %v, want %v", *got.LastUsedAt, at)
		}

		if err := st.Touch(ctx, id.New(), at); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("未知 key: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("Create 重复 ID → ErrConflict", func(t *testing.T) {
		st := newStore(t)
		k := newKey("t1", "dup")
		if err := st.Create(ctx, k); err != nil {
			t.Fatal(err)
		}
		dup := newKey("t1", "dup-again")
		dup.ID = k.ID
		if err := st.Create(ctx, dup); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})
}

// 本地（无 YUGING_TEST_PG_URL）也有信号：契约本身必须被内存版满足。
func TestAPIKeyStore_Memory_satisfiesContract(t *testing.T) {
	apiKeyStoreContract(t, func(t *testing.T) Store { return NewMemoryStore() })
}

func TestAPIKeyStore_PG_satisfiesContract(t *testing.T) {
	pool := pgtest.Pool(t, "apikey")
	apiKeyStoreContract(t, func(t *testing.T) Store { return NewPGStore(pool) })
}

// ── PG 专有 ────────────────────────────────────────────────────

// key_hash 上没有唯一约束（迁移文件未建），PG 版在代码层判重：
// 两个不同的 key 行不允许共用同一个哈希 —— 否则 GetByHash 会二义，
// 等于给认证链路埋一个「同一把钥匙开两扇门」的隐患。
//
// 内存版没有这个检查：byHash 是 map，后插入者静默覆盖前者。
func TestAPIKeyStore_PG_rejectsDuplicateKeyHash(t *testing.T) {
	pool := pgtest.Pool(t, "apikey")
	ctx := context.Background()
	st := NewPGStore(pool)

	first := &APIKey{ID: id.New(), TenantID: "t1", Name: "first", CreatedAt: time.Now().UTC(), keyHash: "shared-hash"}
	if err := st.Create(ctx, first); err != nil {
		t.Fatal(err)
	}

	second := &APIKey{ID: id.New(), TenantID: "t2", Name: "second", CreatedAt: time.Now().UTC(), keyHash: "shared-hash"}
	if err := st.Create(ctx, second); !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

// 持久化：换实例（模拟重启）后 key 与撤销状态仍在。
func TestAPIKeyStore_PG_survivesNewInstance(t *testing.T) {
	pool := pgtest.Pool(t, "apikey")
	ctx := context.Background()

	k := &APIKey{
		ID:        id.New(),
		TenantID:  "t1",
		Name:      "ci-bot",
		Scopes:    []string{"analyses:create", "documents:read"},
		Prefix:    "pangu_01ARZ3",
		CreatedAt: time.Now().UTC(),
		keyHash:   "persist-hash",
	}
	if err := NewPGStore(pool).Create(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := NewPGStore(pool).Revoke(ctx, "t1", k.ID); err != nil {
		t.Fatal(err)
	}

	got, err := NewPGStore(pool).GetByHash(ctx, "persist-hash")
	if err != nil {
		t.Fatalf("重启后按哈希取 key 失败: %v", err)
	}
	if got.ID != k.ID || got.Name != "ci-bot" || got.Prefix != "pangu_01ARZ3" {
		t.Errorf("key = %+v, want id/name/prefix 与写入一致", got)
	}
	if !reflect.DeepEqual(got.Scopes, []string{"analyses:create", "documents:read"}) {
		t.Errorf("Scopes = %v, want 原样读回", got.Scopes)
	}
	if !got.IsRevoked() {
		t.Error("撤销状态未持久化")
	}
}

// api_keys 表没有 created_at 列：CreatedAt 由 ULID 主键的时间戳还原。
// 非 ULID 主键（历史/外部写入的行）还原不出时间，降级为零值 —— 显式固定
// 这个行为，避免以后有人误以为它总是有值。
func TestAPIKeyStore_PG_createdAtDegradesForNonULID(t *testing.T) {
	pool := pgtest.Pool(t, "apikey")
	ctx := context.Background()
	st := NewPGStore(pool)

	k := &APIKey{ID: "legacy-key-id", TenantID: "t1", Name: "legacy", CreatedAt: time.Now().UTC(), keyHash: "legacy-hash"}
	if err := st.Create(ctx, k); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetByHash(ctx, "legacy-hash")
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt = %s, want 零值（主键不是 ULID 时无法还原）", got.CreatedAt)
	}
}
