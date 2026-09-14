package credit

import (
	"context"
	"errors"
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
	txs      []Transaction
}

// NewMemoryStore 装配内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		balances: map[string]int{},
		plans:    map[string]string{},
	}
}

func (m *MemoryStore) Balance(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balances[tenantID], nil
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

	m.txs = append(m.txs, Transaction{
		ID: newTxID(), TenantID: tenantID, Delta: delta,
		Reason: tx.Reason, AnalysisID: tx.AnalysisID,
		OrderID: tx.OrderID, ConsumeTxID: tx.ConsumeTxID,
		BalanceAfter: bal, CreatedAt: time.Now().UTC(),
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
