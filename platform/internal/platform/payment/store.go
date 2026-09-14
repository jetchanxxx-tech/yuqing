package payment

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Store 是订单持久化契约（内存 / PostgreSQL 双实现）。
// ClaimPaid 的原子性是防重复入账的基石：并发/重复回调只有一个能赢。
type Store interface {
	Create(ctx context.Context, o *Order) error
	Get(ctx context.Context, orderID string) (*Order, error)
	List(ctx context.Context, tenantID string, limit int) ([]*Order, error)
	// ClaimPaid 原子跃迁 pending→paid 并回填渠道流水号。
	// 返回 false 表示订单已不在 pending（重复回调/竞态）。
	// 渠道流水号唯一（同一流水号只能属于一个订单）由实现保证。
	ClaimPaid(ctx context.Context, orderID, providerTxnID string) (bool, error)
	MarkClosed(ctx context.Context, orderID string) error
	// MarkRefundNeeded 标记异常订单（如关闭后仍收到成功回调）：钱已扣，
	// 不自动发放，等人工退款/放行。
	MarkRefundNeeded(ctx context.Context, orderID string) error
	MarkGranted(ctx context.Context, orderID string) error
	// SaveChannelMeta 绑定预下单号与渠道侧时间元数据（银联查单必需 txnTime）。
	SaveChannelMeta(ctx context.Context, orderID, providerTxnID, txnTime string) error
}

// MemoryStore 是进程内订单存储（测试/开发）。
type MemoryStore struct {
	mu      sync.Mutex
	orders  map[string]*Order
	txnIdx  map[string]string // provider_txn_id → order_id（唯一索引模拟）
}

// NewMemoryStore 装配内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{orders: map[string]*Order{}, txnIdx: map[string]string{}}
}

func (m *MemoryStore) Create(_ context.Context, o *Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *o
	m.orders[o.ID] = &cp
	return nil
}

func (m *MemoryStore) Get(_ context.Context, orderID string) (*Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *o
	return &cp, nil
}

func (m *MemoryStore) List(_ context.Context, tenantID string, limit int) ([]*Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Order
	for _, o := range m.orders {
		if o.TenantID == tenantID {
			cp := *o
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) ClaimPaid(_ context.Context, orderID, providerTxnID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok {
		return false, ErrNotFound
	}
	if o.State != StatePending {
		return false, nil
	}
	// 渠道流水号唯一：已被其他订单占用则本次跃迁失败（刷单防线）
	if owner, dup := m.txnIdx[providerTxnID]; dup && owner != orderID {
		return false, nil
	}
	now := time.Now().UTC()
	o.State = StatePaid
	o.ProviderTxnID = providerTxnID
	o.PaidAt = &now
	if providerTxnID != "" {
		m.txnIdx[providerTxnID] = orderID
	}
	return true, nil
}

func (m *MemoryStore) MarkClosed(_ context.Context, orderID string) error {
	return m.transition(orderID, StateClosed)
}

func (m *MemoryStore) MarkRefundNeeded(_ context.Context, orderID string) error {
	return m.transition(orderID, StateRefundNeeded)
}

func (m *MemoryStore) MarkGranted(_ context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	o.Granted = true
	return nil
}

func (m *MemoryStore) SaveChannelMeta(_ context.Context, orderID, providerTxnID, txnTime string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	o.ProviderTxnID = providerTxnID
	if txnTime != "" {
		o.TxnTime = txnTime
	}
	return nil
}

func (m *MemoryStore) transition(orderID, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	o.State = to
	return nil
}
