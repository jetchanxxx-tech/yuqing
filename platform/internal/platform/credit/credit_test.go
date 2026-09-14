package credit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

// creditServiceContract 是额度服务的行为契约，参数化到 memory / PG 双实现。
// 资金安全断言（并发不超卖、入账幂等、一扣至多一退）在两个实现上都必须成立。
func creditServiceContract(t *testing.T, newService func(t *testing.T) *Service) {
	t.Helper()
	ctx := context.Background()

	t.Run("试用入账与消费", func(t *testing.T) {
		svc := newService(t)
		if err := svc.TryConsume(ctx, "t1", "a1"); !pkgerrors.Is(err, pkgerrors.ErrNoCredits) {
			t.Fatalf("zero-balance consume: err=%v, want ErrNoCredits", err)
		}
		if err := svc.GrantTrial(ctx, "t1", 1); err != nil {
			t.Fatalf("GrantTrial: %v", err)
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 1 {
			t.Fatalf("balance after trial = %d, want 1", bal)
		}
		if err := svc.TryConsume(ctx, "t1", "a1"); err != nil {
			t.Fatalf("consume after trial: %v", err)
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 0 {
			t.Fatalf("balance after consume = %d, want 0", bal)
		}
		txs, _ := svc.Transactions(ctx, "t1", 10)
		if len(txs) != 2 || txs[0].Reason != ReasonUse || txs[1].Reason != ReasonTrial {
			t.Fatalf("txs = %+v, want [consume, trial] newest first", txs)
		}
	})

	// 资金安全核心：并发扣减绝不超卖。余额 5，20 个 goroutine 抢扣 —— 恰好 5 个成功。
	t.Run("并发扣减不超卖", func(t *testing.T) {
		svc := newService(t)
		if err := svc.GrantPurchase(ctx, "t1", "order-seed", 5); err != nil {
			t.Fatalf("seed grant: %v", err)
		}

		const workers = 20
		var okCount atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := svc.TryConsume(ctx, "t1", "analysis-race"); err == nil {
					okCount.Add(1)
				}
			}()
		}
		wg.Wait()

		if okCount.Load() != 5 {
			t.Fatalf("ok=%d, want exactly 5（超卖或多扣都是资金事故）", okCount.Load())
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 0 {
			t.Fatalf("balance = %d, want 0", bal)
		}
	})

	t.Run("购买入账按订单幂等", func(t *testing.T) {
		svc := newService(t)
		// 回调重放 / 查单补偿并发触发同一订单入账：只入一次
		for i := 0; i < 3; i++ {
			if err := svc.GrantPurchase(ctx, "t1", "order-9", 4); err != nil {
				t.Fatalf("grant #%d: %v", i, err)
			}
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 4 {
			t.Fatalf("balance = %d, want 4 (idempotent)", bal)
		}
	})

	t.Run("回补幂等且支持 Rerun 再消费", func(t *testing.T) {
		svc := newService(t)
		_ = svc.GrantTrial(ctx, "t1", 1)
		_ = svc.TryConsume(ctx, "t1", "a1")

		refunded, err := svc.RefundByAnalysis(ctx, "t1", "a1")
		if err != nil || !refunded {
			t.Fatalf("first refund = (%t, %v), want (true, nil)", refunded, err)
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 1 {
			t.Fatalf("balance after refund = %d, want 1", bal)
		}

		// 重复回补（管线重试/重复 markFailed）：no-op
		refunded, err = svc.RefundByAnalysis(ctx, "t1", "a1")
		if err != nil || refunded {
			t.Fatalf("second refund = (%t, %v), want (false, nil)", refunded, err)
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 1 {
			t.Fatalf("balance after double refund = %d, want 1", bal)
		}

		// Rerun：新一轮消费 → 新一轮回补（一扣至多一退，按 consume_tx_id 配对）
		_ = svc.TryConsume(ctx, "t1", "a1")
		refunded, err = svc.RefundByAnalysis(ctx, "t1", "a1")
		if err != nil || !refunded {
			t.Fatalf("refund after rerun consume = (%t, %v), want (true, nil)", refunded, err)
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 1 {
			t.Fatalf("balance = %d, want 1", bal)
		}

		// 没有消费记录时回补：no-op 不报错
		refunded, err = svc.RefundByAnalysis(ctx, "t1", "never-consumed")
		if err != nil || refunded {
			t.Fatalf("refund without consume = (%t, %v), want (false, nil)", refunded, err)
		}
	})

	t.Run("套餐标记", func(t *testing.T) {
		svc := newService(t)
		if err := svc.SetPlanCode(ctx, "t1", "lite"); err != nil {
			t.Fatalf("SetPlanCode: %v", err)
		}
		if code, _ := svc.PlanCode(ctx, "t1"); code != "lite" {
			t.Fatalf("plan code = %q, want lite", code)
		}
		if code, _ := svc.PlanCode(ctx, "unknown"); code != "" {
			t.Fatalf("unknown tenant plan code = %q, want empty", code)
		}
	})

	t.Run("运营调整", func(t *testing.T) {
		svc := newService(t)
		if err := svc.AdminAdjust(ctx, "t1", 10, "客服补偿"); err != nil {
			t.Fatalf("AdminAdjust: %v", err)
		}
		if bal, _ := svc.Balance(ctx, "t1"); bal != 10 {
			t.Fatalf("balance = %d, want 10", bal)
		}
		txs, _ := svc.Transactions(ctx, "t1", 10)
		if len(txs) != 1 || txs[0].Reason != ReasonGrant {
			t.Fatalf("admin adjust reason = %v", txs)
		}
	})
}

// 本地（无 PG）也有信号：契约必须被内存实现满足。
func TestService_Memory_satisfiesContract(t *testing.T) {
	creditServiceContract(t, func(t *testing.T) *Service {
		return NewService(NewMemoryStore())
	})
}

// 真实 PG 行为（原子 UPDATE + 唯一索引幂等）在配置了测试库时验证。
func TestService_PG_satisfiesContract(t *testing.T) {
	pool := pgtest.Pool(t, "credit")
	creditServiceContract(t, func(t *testing.T) *Service {
		// 子测试共享同一 schema：构造前清掉上一子测试的残留
		if _, err := pool.Exec(context.Background(),
			`TRUNCATE report_credits, credit_transactions`); err != nil {
			t.Fatalf("pre-clean: %v", err)
		}
		return NewService(NewPGStore(pool))
	})
}

func TestErrorCodeMapping(t *testing.T) {
	err := pkgerrors.Wrap(pkgerrors.ErrNoCredits, "报告额度不足，请购买套餐")
	code, status := pkgerrors.CodeFor(err)
	if code != "NO_CREDITS" || status != 402 {
		t.Fatalf("code=%s status=%d, want NO_CREDITS/402", code, status)
	}
}
