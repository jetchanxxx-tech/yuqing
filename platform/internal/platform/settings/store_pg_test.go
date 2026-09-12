package settings

import (
	"context"
	"testing"

	"github.com/yuging/platform/internal/pkg/pgtest"
)

// seedValues 是环境变量种子的替身（生产里由 container.go 传入
// {"bocha_api_key": os.Getenv("BOCHA_API_KEY")}）。
func seedValues() map[string]string {
	return map[string]string{
		"bocha_api_key": "bocha-test-key-from-env",
		"feature_flag":  "on",
	}
}

// settingsStoreContract 是 settings.Store 的行为契约，参数化到任意实现上执行。
//
// 该接口此前没有测试（settings 包只有实现文件）；断言按调用方
// （/admin/settings 的 GET/PUT 与 container.go 的 bochaKeyFunc）的实际用法写：
// 取值、写值、全量读，以及未设置的键返回空串而不报错。
//
// newStore 收到种子 map：内存版与 PG 版的构造签名因此保持同构，
// 「无种子」场景直接传 nil。
func settingsStoreContract(t *testing.T, newStore func(t *testing.T, seed map[string]string) Store) {
	t.Helper()
	ctx := context.Background()

	t.Run("种子值可读", func(t *testing.T) {
		st := newStore(t, seedValues())
		got, err := st.Get(ctx, "bocha_api_key")
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if got != "bocha-test-key-from-env" {
			t.Errorf("Get(bocha_api_key) = %q, want bocha-test-key-from-env", got)
		}
	})

	// 未设置的键返回空串 + nil 错误：bochaKeyFunc 用 `v, _ := Get(...)` 取值，
	// 判定「引擎是否有 key」靠的是空串而不是错误。
	t.Run("未设置的键返回空串且不报错", func(t *testing.T) {
		st := newStore(t, seedValues())
		got, err := st.Get(ctx, "never_set_key")
		if err != nil {
			t.Fatalf("Get(unknown) failed: %v", err)
		}
		if got != "" {
			t.Errorf("Get(unknown) = %q, want 空串", got)
		}
	})

	t.Run("Set 覆盖种子值", func(t *testing.T) {
		st := newStore(t, seedValues())
		if err := st.Set(ctx, "bocha_api_key", "bocha-test-key-admin"); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
		got, err := st.Get(ctx, "bocha_api_key")
		if err != nil {
			t.Fatal(err)
		}
		if got != "bocha-test-key-admin" {
			t.Errorf("Get = %q, want bocha-test-key-admin（管理员在线覆盖应生效）", got)
		}
	})

	t.Run("Set 新键后可读", func(t *testing.T) {
		st := newStore(t, seedValues())
		if err := st.Set(ctx, "new_key", "v1"); err != nil {
			t.Fatal(err)
		}
		got, err := st.Get(ctx, "new_key")
		if err != nil {
			t.Fatal(err)
		}
		if got != "v1" {
			t.Errorf("Get = %q, want v1", got)
		}
	})

	t.Run("All 返回全部键", func(t *testing.T) {
		st := newStore(t, seedValues())
		if err := st.Set(ctx, "extra_key", "extra-value"); err != nil {
			t.Fatal(err)
		}

		all, err := st.All(ctx)
		if err != nil {
			t.Fatalf("All failed: %v", err)
		}
		checks := map[string]string{
			"bocha_api_key": "bocha-test-key-from-env",
			"feature_flag":  "on",
			"extra_key":     "extra-value",
		}
		for k, want := range checks {
			if all[k] != want {
				t.Errorf("All()[%q] = %q, want %q", k, all[k], want)
			}
		}
	})

	// 返回的是副本：改返回值不能影响 store（admin GET 直接把 map 交给 JSON 序列化）。
	t.Run("All 返回副本", func(t *testing.T) {
		st := newStore(t, seedValues())
		all, err := st.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		all["bocha_api_key"] = "mutated"

		again, err := st.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if again["bocha_api_key"] != "bocha-test-key-from-env" {
			t.Errorf("All()[bocha_api_key] = %q, want bocha-test-key-from-env（返回值必须是副本）", again["bocha_api_key"])
		}
	})

	t.Run("All 空 store 返回空 map", func(t *testing.T) {
		st := newStore(t, nil)
		all, err := st.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if all == nil {
			t.Fatal("All() = nil, want 空 map（JSON 需为 {}）")
		}
		if len(all) != 0 {
			t.Errorf("len(All()) = %d, want 0", len(all))
		}
	})
}

// 本地（无 YUGING_TEST_PG_URL）也有信号：契约本身必须被内存版满足。
func TestSettingsStore_Memory_satisfiesContract(t *testing.T) {
	settingsStoreContract(t, func(t *testing.T, seed map[string]string) Store {
		return NewMemoryStore(seed)
	})
}

func TestSettingsStore_PG_satisfiesContract(t *testing.T) {
	pool := pgtest.Pool(t, "settings")
	settingsStoreContract(t, func(t *testing.T, seed map[string]string) Store {
		st, err := NewPGStore(pool, seed)
		if err != nil {
			t.Fatalf("NewPGStore failed: %v", err)
		}
		return st
	})
}

// ── PG 专有 ────────────────────────────────────────────────────

// 种子只在键不存在时写入：重建进程（例如 systemd 重启带上 BOCHA_API_KEY）
// 绝不能覆盖管理员在线改过的值 —— 否则每次重启都会把 key 回滚成环境变量里的旧值。
func TestSettingsStore_PG_seedDoesNotOverwriteExistingValue(t *testing.T) {
	pool := pgtest.Pool(t, "settings")
	ctx := context.Background()

	first, err := NewPGStore(pool, map[string]string{"bocha_api_key": "bocha-test-key-from-env"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Set(ctx, "bocha_api_key", "bocha-test-key-changed"); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：同一个种子再构造一次。
	second, err := NewPGStore(pool, map[string]string{"bocha_api_key": "bocha-test-key-from-env"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := second.Get(ctx, "bocha_api_key")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bocha-test-key-changed" {
		t.Errorf("Get = %q, want bocha-test-key-changed（种子不得覆盖库里已有的值）", got)
	}
}

// 持久化：换实例后管理员写入的设置仍在，且新键也能被后续种子补齐。
func TestSettingsStore_PG_survivesNewInstance(t *testing.T) {
	pool := pgtest.Pool(t, "settings")
	ctx := context.Background()

	first, err := NewPGStore(pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Set(ctx, "llm_base_url", "https://api.deepseek.com"); err != nil {
		t.Fatal(err)
	}

	// 重启：新种子只补新键，不动已有键。
	second, err := NewPGStore(pool, map[string]string{
		"llm_base_url": "https://wrong.example.com",
		"new_seed_key": "seeded",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := second.Get(ctx, "llm_base_url")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.deepseek.com" {
		t.Errorf("llm_base_url = %q, want 已落库的值", got)
	}
	seeded, err := second.Get(ctx, "new_seed_key")
	if err != nil {
		t.Fatal(err)
	}
	if seeded != "seeded" {
		t.Errorf("new_seed_key = %q, want seeded（新键应被种子补齐）", seeded)
	}
}

// 覆盖语义：Set 是 upsert，重复写同一键以最后一次为准。
func TestSettingsStore_PG_setIsUpsert(t *testing.T) {
	pool := pgtest.Pool(t, "settings")
	ctx := context.Background()
	st, err := NewPGStore(pool, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range []string{"v1", "v2", "v3"} {
		if err := st.Set(ctx, "rotating", v); err != nil {
			t.Fatalf("Set(%s) failed: %v", v, err)
		}
	}
	got, err := st.Get(ctx, "rotating")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v3" {
		t.Errorf("Get = %q, want v3", got)
	}
}
