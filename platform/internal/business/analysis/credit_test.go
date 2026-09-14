package analysis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/queue"
)

// fakeCredits 是 CreditReserver 的测试替身：内存余额 + 消费/回补计数。
type fakeCredits struct {
	mu       sync.Mutex
	balance  map[string]int
	consumes map[string]int
	refunds  map[string]int
}

func newFakeCredits() *fakeCredits {
	return &fakeCredits{
		balance:  map[string]int{},
		consumes: map[string]int{},
		refunds:  map[string]int{},
	}
}

func (f *fakeCredits) grant(tenantID string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.balance[tenantID] += n
}

func (f *fakeCredits) TryConsume(_ context.Context, tenantID, analysisID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.balance[tenantID] < 1 {
		return pkgerrors.Wrap(pkgerrors.ErrNoCredits, "报告额度不足")
	}
	f.balance[tenantID]--
	f.consumes[analysisID]++
	return nil
}

func (f *fakeCredits) RefundByAnalysis(_ context.Context, tenantID, analysisID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consumes[analysisID] <= f.refunds[analysisID] {
		return false, nil
	}
	f.refunds[analysisID]++
	f.balance[tenantID]++
	return true, nil
}

func (f *fakeCredits) balanceOf(tenantID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balance[tenantID]
}

// failingQueue 在 Publish 时报错（验证扣减后的失败回补）。
type failingQueue struct{ queue.Queue }

func (failingQueue) Publish(_ context.Context, _ string, _ []byte) error {
	return errors.New("queue down")
}

func (failingQueue) Close() error { return nil }

func newGatedService(t *testing.T, q queue.Queue, credits *fakeCredits) *Service {
	t.Helper()
	svc := NewService(q, 4)
	svc.SetCreditReserver(credits)
	if c, ok := q.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = c.Close() })
	}
	return svc
}

func TestCreate_creditGating(t *testing.T) {
	credits := newFakeCredits()
	svc := newGatedService(t, queue.NewMemory(), credits)
	ctx := context.Background()
	req := CreateAnalysisRequest{
		TenantID: "t1", Name: "雅阁后排", AnalysisType: "brand",
		Keywords: []string{"雅阁"}, Sources: []string{"weibo"},
	}

	// 零余额：402 语义，任务不落库
	if _, err := svc.Create(ctx, req); !pkgerrors.Is(err, pkgerrors.ErrNoCredits) {
		t.Fatalf("zero-balance Create err = %v, want ErrNoCredits", err)
	}
	if got, _ := svc.List(ctx, "t1"); len(got) != 0 {
		t.Fatalf("refused create must not persist, got %d analyses", len(got))
	}

	// 有余额：扣 1 成功
	credits.grant("t1", 1)
	a, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create with credit: %v", err)
	}
	if credits.balanceOf("t1") != 0 {
		t.Fatalf("balance = %d, want 0 after consume", credits.balanceOf("t1"))
	}

	// 该任务的失败回补可用（analysis_id 关联）
	if refunded, _ := credits.RefundByAnalysis(ctx, "t1", a.ID); !refunded {
		t.Fatal("expected refund to find the consume by analysis id")
	}
}

func TestCreate_refundsWhenEnqueueFails(t *testing.T) {
	credits := newFakeCredits()
	credits.grant("t1", 2)
	svc := newGatedService(t, failingQueue{}, credits)

	if _, err := svc.Create(context.Background(), CreateAnalysisRequest{
		TenantID: "t1", Name: "x", AnalysisType: "brand",
	}); err == nil {
		t.Fatal("expected enqueue failure")
	}
	if got := credits.balanceOf("t1"); got != 2 {
		t.Fatalf("balance after enqueue failure = %d, want 2（扣减必须回补）", got)
	}
}

func TestMarkFailed_refundsCredit(t *testing.T) {
	credits := newFakeCredits()
	svc := newGatedService(t, queue.NewMemory(), credits)
	credits.grant("t1", 1)
	a := createAnalysis(t, svc, "t1")

	if err := svc.markFailed(context.Background(), "t1", a.ID, "fetch_error"); err != nil {
		t.Fatalf("markFailed: %v", err)
	}
	if got := credits.balanceOf("t1"); got != 1 {
		t.Fatalf("balance after failed pipeline = %d, want 1（失败必须回补）", got)
	}

	// 已终态任务重复 markFailed 不再退（幂等由 mutate no-op 保证）
	_ = svc.markFailed(context.Background(), "t1", a.ID, "fetch_error")
	if got := credits.balanceOf("t1"); got != 1 {
		t.Fatalf("balance = %d, want 1 (terminal no-op must not refund)", got)
	}
}

func TestCancel_refundsCredit(t *testing.T) {
	credits := newFakeCredits()
	svc := newGatedService(t, queue.NewMemory(), credits)
	credits.grant("t1", 1)
	a := createAnalysis(t, svc, "t1")

	if err := svc.Cancel(context.Background(), "t1", a.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := credits.balanceOf("t1"); got != 1 {
		t.Fatalf("balance after cancel = %d, want 1", got)
	}
}

func TestRerun_consumesCreditAgain(t *testing.T) {
	credits := newFakeCredits()
	svc := newGatedService(t, queue.NewMemory(), credits)
	credits.grant("t1", 1)
	a := createAnalysis(t, svc, "t1") // 消费 1，余额 0
	_ = svc.markFailed(context.Background(), "t1", a.ID, "fetch_error")
	// 失败回补后余额 1

	// 余额不足的场景：先把回补退掉的钱花在别处
	credits.grant("t2", 0)
	_ = credits.TryConsume(context.Background(), "t2", "dummy")
	_ = credits.TryConsume(context.Background(), "t2", "dummy2")
	if err := svc.Rerun(context.Background(), "t2", a.ID); !pkgerrors.Is(err, pkgerrors.ErrNoCredits) {
		t.Fatalf("rerun without credits err = %v, want ErrNoCredits", err)
	}

	// 有余额：重跑再扣一次
	if err := svc.Rerun(context.Background(), "t1", a.ID); err != nil {
		t.Fatalf("Rerun: %v", err)
	}
	if got := credits.balanceOf("t1"); got != 0 {
		t.Fatalf("balance after rerun = %d, want 0", got)
	}
	if credits.consumes[a.ID] != 2 {
		t.Fatalf("consumes = %d, want 2（两轮各扣一次）", credits.consumes[a.ID])
	}
	_ = fmt.Sprint()
}
