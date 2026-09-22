package auth

// 用户中心存储契约：UserStore + VerificationStore 双实现（内存/PG）参数化验证。
//
// 动机：v0.1.1 生产部署连续 4 个 PG bug（EXCLUDE 非法 / token_type 列名 /
// ON CONFLICT 无约束 / INSERT 缺 id）全部因 PG 路径零测试覆盖漏网 ——
// 本文件保证两条实现路径的行为契约从此锁定。
//
// 运行条件：YUQING_TEST_PG_URL 指向测试库（pgtest 自建独占 schema 并按序
// 执行 migrations/platform/*.sql，含 0007）；未设置时 PG 用例自动 skip，
// 内存版契约在本地依然全量执行。

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

// userCenterStoreContract 是 UserStore 的行为契约，参数化到任意实现。
func userCenterStoreContract(t *testing.T, newStore func(t *testing.T) UserStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("GetByID 往返一致（新字段零值）", func(t *testing.T) {
		st := newStore(t)
		userID := id.New()
		seedUCUserOnUserStore(t, st, userID, "uc-getbyid@example.com")
		u, err := st.GetByID(ctx, userID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if u.ID != userID || u.Email != "uc-getbyid@example.com" || u.Name != "契约用户" {
			t.Errorf("user = %+v, want id=%s email=uc-getbyid@example.com", u, userID)
		}
		if u.Phone != "" || u.AvatarURL != "" || u.EmailVerifiedAt != nil || u.TrialAnalysisUsed != 0 {
			t.Errorf("新字段应为零值: %+v", u)
		}
	})

	t.Run("GetByID 不存在 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if _, err := st.GetByID(ctx, id.New()); !errors.Is(err, errors.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdatePassword 更新哈希并记录时间", func(t *testing.T) {
		st := newStore(t)
		userID := id.New()
		seedUCUserOnUserStore(t, st, userID, "uc-pwd@example.com")

		if err := st.UpdatePassword(ctx, userID, "newhash"); err != nil {
			t.Fatalf("UpdatePassword: %v", err)
		}
		u, _ := st.GetByID(ctx, userID)
		if u.PasswordHash != "newhash" {
			t.Errorf("hash = %q, want newhash", u.PasswordHash)
		}
		if u.PasswordChangedAt == nil {
			t.Error("password_changed_at 未记录")
		}
	})

	t.Run("MarkEmailVerified 幂等生效", func(t *testing.T) {
		st := newStore(t)
		userID := id.New()
		seedUCUserOnUserStore(t, st, userID, "uc-verify@example.com")

		if err := st.MarkEmailVerified(ctx, userID); err != nil {
			t.Fatalf("MarkEmailVerified: %v", err)
		}
		// 二次调用不报错（幂等）
		if err := st.MarkEmailVerified(ctx, userID); err != nil {
			t.Fatalf("二次 MarkEmailVerified: %v", err)
		}
		u, _ := st.GetByID(ctx, userID)
		if u.EmailVerifiedAt == nil {
			t.Error("email_verified_at 未标记")
		}
	})

	t.Run("UpdateProfile 空串跳过 + 中文名", func(t *testing.T) {
		st := newStore(t)
		userID := id.New()
		seedUCUserOnUserStore(t, st, userID, "uc-profile@example.com")

		if err := st.UpdateProfile(ctx, userID, "七字中文名测试通过", "", "Asia/Tokyo"); err != nil {
			t.Fatalf("UpdateProfile: %v", err)
		}
		// 空串字段跳过
		if err := st.UpdateProfile(ctx, userID, "", "", ""); err != nil {
			t.Fatalf("UpdateProfile(空): %v", err)
		}
		u, _ := st.GetByID(ctx, userID)
		if u.Name != "七字中文名测试通过" || u.Timezone != "Asia/Tokyo" {
			t.Errorf("profile = %q/%q, want 中文名保留 + Asia/Tokyo", u.Name, u.Timezone)
		}
	})

	t.Run("SetPhone → GetByPhone 往返；ClearPhone 清空", func(t *testing.T) {
		st := newStore(t)
		userID := id.New()
		seedUCUserOnUserStore(t, st, userID, "uc-phone@example.com")

		if err := st.SetPhone(ctx, userID, "13800138000"); err != nil {
			t.Fatalf("SetPhone: %v", err)
		}
		u, _ := st.GetByID(ctx, userID)
		if u.Phone != "13800138000" || u.PhoneVerifiedAt == nil {
			t.Fatalf("phone = %q verified=%v, want 绑定+已验证", u.Phone, u.PhoneVerifiedAt != nil)
		}
		byPhone, err := st.GetByPhone(ctx, "13800138000")
		if err != nil || byPhone.ID != userID {
			t.Fatalf("GetByPhone = %v %v, want userID %s", byPhone, err, userID)
		}

		if err := st.ClearPhone(ctx, userID); err != nil {
			t.Fatalf("ClearPhone: %v", err)
		}
		if _, err := st.GetByPhone(ctx, "13800138000"); !errors.Is(err, errors.ErrNotFound) {
			t.Errorf("解绑后 GetByPhone = %v, want ErrNotFound", err)
		}
	})

	t.Run("SetPhone 手机号已被他人占用 → ErrConflict", func(t *testing.T) {
		st := newStore(t)
		u1, u2 := id.New(), id.New()
		seedUCUserOnUserStore(t, st, u1, "uc-p1@example.com")
		seedUCUserOnUserStore(t, st, u2, "uc-p2@example.com")

		if err := st.SetPhone(ctx, u1, "13900139000"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPhone(ctx, u2, "13900139000"); !errors.Is(err, errors.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("ConsumeTrialAnalysis 0→1 只允许一次", func(t *testing.T) {
		st := newStore(t)
		userID := id.New()
		seedUCUserOnUserStore(t, st, userID, "uc-trial@example.com")

		first, err := st.ConsumeTrialAnalysis(ctx, userID)
		if err != nil || !first {
			t.Fatalf("首次 = %v %v, want true nil", first, err)
		}
		second, err := st.ConsumeTrialAnalysis(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if second {
			t.Error("第二次消耗应失败（方案 B 只试 1 次）")
		}
	})
}

// seedUCUserOnUserStore 在 UserStore 上直接种用户。
// PG 实现同时是 Store 与 UserStore（类型断言后走 CreateUser）；
// 内存实现同理。契约统一走该 helper 保证初始化路径一致。
func seedUCUserOnUserStore(t *testing.T, us UserStore, userID, email string) {
	t.Helper()
	cs, ok := us.(interface {
		CreateUser(ctx context.Context, u User) error
	})
	if !ok {
		t.Fatalf("UserStore 实现不满足 CreateUser（无法种数据）")
	}
	if err := cs.CreateUser(context.Background(), User{
		ID: userID, Email: email, PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$h$h", Name: "契约用户",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// verificationStoreContract 是 VerificationStore 的行为契约。
// seedUser 返回一个"存在"的 user_id：PG 实现的 verification_tokens.user_id
// 有外键（引用 users 表），契约需要真实行；内存版可返回任意字符串。
func verificationStoreContract(t *testing.T, newStore func(t *testing.T) VerificationStore, seedUser func(t *testing.T) string) {
	t.Helper()
	ctx := context.Background()

	t.Run("邮箱 token：Save→Load→Consume→二次 Load 失效", func(t *testing.T) {
		st := newStore(t)
		uid := seedUser(t)
		if err := st.SaveEmailToken(ctx, "tok1", uid, time.Hour); err != nil {
			t.Fatalf("SaveEmailToken: %v", err)
		}
		got, err := st.LoadEmailToken(ctx, "tok1")
		if err != nil || got != uid {
			t.Fatalf("Load = %q %v, want %q nil", got, err, uid)
		}
		if err := st.ConsumeEmailToken(ctx, "tok1"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.LoadEmailToken(ctx, "tok1"); !errors.Is(err, errors.ErrNotFound) {
			t.Errorf("消费后 Load = %v, want ErrNotFound", err)
		}
	})

	t.Run("邮箱 token 负 TTL 视为过期", func(t *testing.T) {
		st := newStore(t)
		uid := seedUser(t)
		if err := st.SaveEmailToken(ctx, "tok2", uid, -time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := st.LoadEmailToken(ctx, "tok2"); !errors.Is(err, errors.ErrNotFound) {
			t.Errorf("过期 token Load = %v, want ErrNotFound", err)
		}
	})

	t.Run("短信码：Save→Load；同号覆盖旧码；Consume 后失效", func(t *testing.T) {
		st := newStore(t)
		if err := st.SaveSMSCode(ctx, "13800138000", "bind", "111111", 5*time.Minute); err != nil {
			t.Fatalf("SaveSMSCode: %v", err)
		}
		// 覆盖旧码
		if err := st.SaveSMSCode(ctx, "13800138000", "bind", "222222", 5*time.Minute); err != nil {
			t.Fatalf("SaveSMSCode(覆盖): %v", err)
		}
		got, err := st.LoadSMSCode(ctx, "13800138000", "bind")
		if err != nil || got != "222222" {
			t.Fatalf("Load = %q %v, want 222222 nil（覆盖语义）", got, err)
		}
		if err := st.ConsumeSMSCode(ctx, "13800138000", "bind"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.LoadSMSCode(ctx, "13800138000", "bind"); !errors.Is(err, errors.ErrNotFound) {
			t.Errorf("消费后 Load = %v, want ErrNotFound", err)
		}
	})

	t.Run("短信码过期 → ErrNotFound", func(t *testing.T) {
		st := newStore(t)
		if err := st.SaveSMSCode(ctx, "13900139000", "bind", "333333", -time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, err := st.LoadSMSCode(ctx, "13900139000", "bind"); !errors.Is(err, errors.ErrNotFound) {
			t.Errorf("过期码 Load = %v, want ErrNotFound", err)
		}
	})
}

// 本地（无 PG）也有信号：契约必须被内存版满足。
func TestUserCenter_Memory_satisfiesContract(t *testing.T) {
	userCenterStoreContract(t, func(t *testing.T) UserStore { return NewMemoryStore() })
	verificationStoreContract(t,
		func(t *testing.T) VerificationStore { return NewMemoryVerificationStore() },
		func(t *testing.T) string { return "user-" + id.New() },
	)
}

// PG 路径契约：需要 YUQING_TEST_PG_URL（部署服务器/CI 上真实执行）。
func TestUserCenter_PG_satisfiesContract(t *testing.T) {
	pool := pgtest.Pool(t, "auth")
	st := NewPGStore(pool)
	userCenterStoreContract(t, func(t *testing.T) UserStore { return st })
	verificationStoreContract(t,
		func(t *testing.T) VerificationStore { return NewPGVerificationStore(pool) },
		func(t *testing.T) string {
			// verification_tokens.user_id 有外键：种真实用户行
			uid := id.New()
			seedUCUserOnUserStore(t, st, uid, "uc-vk-"+uid+"@example.com")
			return uid
		},
	)
}

// PG 专有：持久化语义（换实例 = 模拟重启）。
func TestUserCenter_PG_survivesNewInstance(t *testing.T) {
	pool := pgtest.Pool(t, "auth")
	ctx := context.Background()

	st := NewPGStore(pool)
	uid := id.New()
	seedUCUserOnUserStore(t, st, uid, "uc-persist-"+uid+"@example.com")

	first := NewPGVerificationStore(pool)
	if err := first.SaveEmailToken(ctx, "persist-tok", uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	second := NewPGVerificationStore(pool) // 新实例
	if got, err := second.LoadEmailToken(ctx, "persist-tok"); err != nil || got != uid {
		t.Errorf("重启后 Load = %q %v, want %q nil", got, err, uid)
	}
}
