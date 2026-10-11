package report

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
	for id, r := range m.byTenant[tid] {
		if r.CreatedAt == nil || r.CreatedAt.IsZero() {
			return false, pkgerrors.ErrConflict
		}
		if r.Format != "html" || (r.FileKey != "" && r.FileKey != "reports/"+r.ID+".html") {
			return false, pkgerrors.ErrConflict
		}
		if !r.CreatedAt.After(cutoff) && removed < limit {
			delete(m.byTenant[tid], id)
			removed++
		}
	}
	order := m.order[tid][:0]
	for _, id := range m.order[tid] {
		if m.byTenant[tid][id] != nil {
			order = append(order, id)
		}
	}
	m.order[tid] = order
	return len(m.byTenant[tid]) == 0, nil
}
