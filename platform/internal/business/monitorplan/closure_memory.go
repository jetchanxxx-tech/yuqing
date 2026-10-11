package monitorplan

import (
	"context"
	"time"
)

func (s *MemoryStore) CleanupClosedTenant(ctx context.Context, tid string, cutoff time.Time, limit int) (bool, error) {
	if tid == "" || limit < 1 || limit > 1000 {
		return false, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	s.sealed[tid] = true
	removed := 0
	for key, p := range s.plans {
		if p.TenantID == tid && !p.CreatedAt.IsZero() && !p.CreatedAt.After(cutoff) && removed < limit {
			delete(s.plans, key)
			removed++
		}
	}
	for _, p := range s.plans {
		if p.TenantID == tid {
			if p.CreatedAt.IsZero() {
				return false, ErrConflict
			}
			return false, nil
		}
	}
	return true, nil
}
