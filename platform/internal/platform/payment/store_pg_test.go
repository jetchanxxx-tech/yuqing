package payment

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

// PGStore 契约：内存实现覆盖了 service 层逻辑，这里验证 store 本身的
// SQL 语义 —— 尤其 ClaimPaid 的原子性与流水号唯一（防重复入账的基石）。
func TestPGStore_contract(t *testing.T) {
	pool := pgtest.Pool(t, "payment")
	ctx := context.Background()
	newStore := func(t *testing.T) *PGStore {
		t.Helper()
		if _, err := pool.Exec(ctx, `TRUNCATE orders`); err != nil {
			t.Fatalf("pre-clean: %v", err)
		}
		return NewPGStore(pool)
	}

	t.Run("创建与读取往返", func(t *testing.T) {
		st := newStore(t)
		now := time.Now().UTC()
		in := &Order{
			ID: "o1", TenantID: "t1", SKUCode: "lite", Kind: "plan",
			Credits: 4, AmountCents: 9900, Channel: "alipay",
			State: StatePending, QRCodeURL: "https://qr",
			ExpiresAt: now.Add(orderTTL), CreatedAt: now,
		}
		if err := st.Create(ctx, in); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := st.Get(ctx, "o1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.AmountCents != 9900 || got.Credits != 4 || got.State != StatePending || got.QRCodeURL != "https://qr" {
			t.Fatalf("roundtrip mismatch: %+v", got)
		}
		if _, err := st.Get(ctx, "missing"); err == nil {
			t.Fatal("缺失订单应返回错误")
		}
	})

	t.Run("ClaimPaid 幂等与流水号唯一", func(t *testing.T) {
		st := newStore(t)
		now := time.Now().UTC()
		for _, id := range []string{"oa", "ob"} {
			if err := st.Create(ctx, &Order{
				ID: id, TenantID: "t1", SKUCode: "lite", Kind: "plan",
				Credits: 4, AmountCents: 9900, Channel: "alipay",
				State: StatePending, ExpiresAt: now.Add(orderTTL), CreatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
		}

		// 首次核销成功
		if ok, err := st.ClaimPaid(ctx, "oa", "txn-1"); err != nil || !ok {
			t.Fatalf("first claim = (%t, %v)", ok, err)
		}
		// 重复回调：同订单同流水号 → false（已非 pending）
		if ok, _ := st.ClaimPaid(ctx, "oa", "txn-1"); ok {
			t.Fatal("重复 claim 必须失败")
		}
		// 刷单：同一流水号喂给订单2 → false
		if ok, _ := st.ClaimPaid(ctx, "ob", "txn-1"); ok {
			t.Fatal("流水号冲突的 claim 必须失败")
		}
		// 订单2用自己的流水号 → 正常
		if ok, _ := st.ClaimPaid(ctx, "ob", "txn-2"); !ok {
			t.Fatal("独立流水号的 claim 应成功")
		}

		got, _ := st.Get(ctx, "ob")
		if got.State != StatePaid || got.ProviderTxnID != "txn-2" || got.PaidAt == nil {
			t.Fatalf("ob = %+v", got)
		}
	})

	t.Run("状态标记与租户列表", func(t *testing.T) {
		st := newStore(t)
		now := time.Now().UTC()
		for i, id := range []string{"ox", "oy"} {
			if err := st.Create(ctx, &Order{
				ID: id, TenantID: "t1", SKUCode: "addon_report", Kind: "addon",
				Credits: 1, AmountCents: 6900, Channel: "wechat",
				State: StatePending, ExpiresAt: now.Add(orderTTL),
				CreatedAt: now.Add(time.Duration(i) * time.Minute),
			}); err != nil {
				t.Fatal(err)
			}
		}
		_ = st.MarkClosed(ctx, "ox")
		_ = st.MarkRefundNeeded(ctx, "oy")
		_ = st.MarkGranted(ctx, "oy")
		_ = st.SaveChannelMeta(ctx, "oy", "txn-oy", "20260915120000")

		ox, _ := st.Get(ctx, "ox")
		if ox.State != StateClosed {
			t.Fatalf("ox = %s, want closed", ox.State)
		}
		oy, _ := st.Get(ctx, "oy")
		if oy.State != StateRefundNeeded || !oy.Granted || oy.ProviderTxnID != "txn-oy" {
			t.Fatalf("oy = %+v", oy)
		}

		list, err := st.List(ctx, "t1", 10)
		if err != nil || len(list) != 2 {
			t.Fatalf("List = (%v, %v), want 2 单", list, err)
		}
		// 新→旧：oy（晚创建）在前
		if list[0].ID != "oy" {
			t.Fatalf("list[0] = %s, want oy", list[0].ID)
		}
	})
}
