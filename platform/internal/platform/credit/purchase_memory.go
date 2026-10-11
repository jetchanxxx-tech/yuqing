package credit

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"time"
)

// Payment holds its paid-order lock through this memory commit. No fallible
// operation follows the pool write before payment sets its granted marker.
func (s *Service) GrantPurchasePlanMemory(ctx context.Context, tid, oid string, count int, plan string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m, ok := s.store.(*MemoryStore)
	if !ok {
		return pkgerrors.ErrServiceUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tx := range m.txs {
		if tx.Reason == ReasonBuy && tx.OrderID == oid {
			if tx.TenantID != tid || tx.Delta != count {
				return pkgerrors.ErrConflict
			}
			if plan != "" && tx.Version > 0 && m.versions[tid] == tx.Version {
				m.plans[tid], m.planSources[tid] = plan, "purchase"
				m.versions[tid]++
			}
			return nil
		}
	}
	if count <= 0 || int64(m.balances[tid])+int64(count) > 2147483647 {
		return pkgerrors.ErrConflict
	}
	m.balances[tid] += count
	m.versions[tid]++
	m.txs = append(m.txs, Transaction{ID: newTxID(), TenantID: tid, OrderID: oid, Delta: count, Reason: ReasonBuy, BalanceAfter: m.balances[tid], Version: m.versions[tid], CreatedAt: time.Now().UTC()})
	if plan != "" {
		m.plans[tid], m.planSources[tid] = plan, "purchase"
		m.versions[tid]++
	}
	return nil
}
