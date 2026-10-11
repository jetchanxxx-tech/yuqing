package alert

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"time"
)

func (m *MemoryStore) CleanupClosedTenant(ctx context.Context, tid string, cutoff time.Time, limit int) (bool, error) {
	if tid == "" || limit < 1 || limit > 1000 {
		return false, pkgerrors.ErrBadRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	m.sealed[tid] = true
	removed := 0
	for id, a := range m.alerts[tid] {
		if !a.CreatedAt.IsZero() && !a.CreatedAt.After(cutoff) && removed < limit {
			delete(m.alerts[tid], id)
			removed++
		}
	}
	order := m.order[tid][:0]
	for _, id := range m.order[tid] {
		if m.alerts[tid][id] != nil {
			order = append(order, id)
		}
	}
	m.order[tid] = order
	for _, a := range m.alerts[tid] {
		if a.CreatedAt.IsZero() {
			return false, pkgerrors.ErrConflict
		}
		return false, nil
	}
	return true, nil
}
