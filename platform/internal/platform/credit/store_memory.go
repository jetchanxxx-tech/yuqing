package credit

import (
	"context"
	"errors"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"sync"
	"time"
)

// errAlreadyRefunded 是存储层幂等命中（重复购买入账/重复回补）的内部信号，
// Service 层据此返回 no-op 而非报错。
var errAlreadyRefunded = errors.New("credit: idempotent replay")

// MemoryStore 是进程内额度存储（测试/开发）。互斥锁保证 ApplyDelta 原子，
// 与 PG 实现的超卖语义一致。
type MemoryStore struct {
	mu       sync.Mutex
	balances map[string]int
	plans    map[string]string
	versions map[string]int64
	txs      []Transaction
}

// NewMemoryStore 装配内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		balances: map[string]int{},
		plans:    map[string]string{},
		versions: map[string]int64{},
	}
}
func (m *MemoryStore) AnonymizeClosure(uid string, tenants []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sole := map[string]bool{}
	for _, id := range tenants {
		sole[id] = true
	}
	for i := range m.txs {
		tx := &m.txs[i]
		if tx.ActorID == uid || sole[tx.TenantID] {
			tx.ReasonDetail = "已注销账号（原因脱敏）"
			if tx.IdempotencyKey != "" {
				tx.IdempotencyKey = "anonymized:" + tx.ID
			}
		}
	}
}

func (m *MemoryStore) Balance(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balances[tenantID], nil
}

func (m *MemoryStore) Snapshot(_ context.Context, tenantID string) (*Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bal, hasBalance := m.balances[tenantID]
	plan, hasPlan := m.plans[tenantID]
	if !hasBalance && !hasPlan {
		return nil, nil
	}
	// PostgreSQL's report_credits default is free for a balance-only pool.
	if !hasPlan {
		plan = "free"
	}
	return &Snapshot{Balance: bal, PlanCode: plan, Version: m.versions[tenantID]}, nil
}

func (m *MemoryStore) ApplyDelta(_ context.Context, tenantID string, delta int, tx Transaction) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 幂等键：购买按订单、回补按被退的消费流水
	for i := range m.txs {
		e := &m.txs[i]
		if tx.Reason == ReasonBuy && tx.OrderID != "" && e.Reason == ReasonBuy && e.OrderID == tx.OrderID {
			return m.balances[tenantID], errAlreadyRefunded
		}
		if tx.Reason == ReasonRefund && tx.ConsumeTxID != "" && e.Reason == ReasonRefund && e.ConsumeTxID == tx.ConsumeTxID {
			return m.balances[tenantID], errAlreadyRefunded
		}
	}

	bal := m.balances[tenantID]
	if bal+delta < 0 {
		return bal, ErrInsufficientCredits
	}
	bal += delta
	m.balances[tenantID] = bal

	m.versions[tenantID]++
	version := m.versions[tenantID]
	m.txs = append(m.txs, Transaction{
		ID: newTxID(), TenantID: tenantID, Delta: delta,
		Reason: tx.Reason, ReasonDetail: tx.ReasonDetail, ActorID: tx.ActorID, IdempotencyKey: tx.IdempotencyKey, AnalysisID: tx.AnalysisID,
		OrderID: tx.OrderID, ConsumeTxID: tx.ConsumeTxID,
		BalanceAfter: bal, Version: version, CreatedAt: time.Now().UTC(),
	})
	return bal, nil
}

func (m *MemoryStore) FindUnrefundedConsume(_ context.Context, tenantID, analysisID string) (*Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	refunded := map[string]bool{}
	var newest *Transaction
	for i := range m.txs {
		e := &m.txs[i]
		if e.TenantID != tenantID {
			continue
		}
		if e.Reason == ReasonRefund && e.ConsumeTxID != "" {
			refunded[e.ConsumeTxID] = true
		}
	}
	for i := range m.txs {
		e := &m.txs[i]
		if e.TenantID == tenantID && e.Reason == ReasonUse && e.AnalysisID == analysisID && !refunded[e.ID] {
			if newest == nil || e.CreatedAt.After(newest.CreatedAt) {
				cp := *e
				newest = &cp
			}
		}
	}
	return newest, nil
}

func (m *MemoryStore) SetPlanCode(_ context.Context, tenantID, planCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans[tenantID] = planCode
	m.versions[tenantID]++
	return nil
}

func (m *MemoryStore) PlanCode(_ context.Context, tenantID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.plans[tenantID], nil
}

func (m *MemoryStore) Transactions(_ context.Context, tenantID string, limit int) ([]Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// append 序即时间序；倒序遍历得到新→旧（时间戳同刻时顺序仍确定）
	var out []Transaction
	for i := len(m.txs) - 1; i >= 0; i-- {
		if e := m.txs[i]; e.TenantID == tenantID {
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *MemoryStore) Adjust(ctx context.Context, a Adjustment) (*Transaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	for _, t := range m.txs {
		if t.TenantID == a.TenantID && t.IdempotencyKey == a.IdempotencyKey {
			if t.Reason != ReasonAdjust || t.Delta != a.Delta || t.ReasonDetail != a.ReasonDetail || t.ActorID != a.ActorID || t.ExpectedVersion != a.ExpectedVersion {
				return nil, pkgerrors.ErrConflict
			}
			return &t, nil
		}
	}
	if m.versions[a.TenantID] != a.ExpectedVersion {
		return nil, pkgerrors.ErrConflict
	}
	bal := m.balances[a.TenantID]
	if int64(bal)+int64(a.Delta) < 0 {
		return nil, ErrInsufficientCredits
	}
	if int64(bal)+int64(a.Delta) > 2147483647 {
		return nil, pkgerrors.ErrConflict
	}
	m.versions[a.TenantID]++
	t := Transaction{ID: newTxID(), TenantID: a.TenantID, Delta: a.Delta, Reason: ReasonAdjust, ReasonDetail: a.ReasonDetail, ActorID: a.ActorID, IdempotencyKey: a.IdempotencyKey, BalanceAfter: bal + a.Delta, Version: m.versions[a.TenantID], ExpectedVersion: a.ExpectedVersion, CreatedAt: time.Now().UTC()}
	m.balances[a.TenantID] = t.BalanceAfter
	m.txs = append(m.txs, t)
	return &t, nil
}
