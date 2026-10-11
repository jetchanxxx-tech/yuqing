package analysis

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"time"
)

func (s *Service) CleanupClosedTenant(ctx context.Context, tid string, cutoff time.Time, limit int) (bool, error) {
	if tid == "" || limit < 1 || limit > 1000 {
		return false, pkgerrors.ErrBadRequest
	}
	m, ok := s.store.(*memoryStore)
	if !ok {
		return false, pkgerrors.ErrServiceUnavailable
	}
	docs, ok := s.docs.(*memoryDocumentStore)
	if !ok {
		return false, pkgerrors.ErrServiceUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	docs.mu.Lock()
	defer docs.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	m.sealed[tid] = true
	docs.sealed[tid] = true
	if m.purged[tid] == nil {
		m.purged[tid] = map[string]bool{}
	}
	removed := 0
	for id, a := range m.byTenant[tid] {
		if m.purged[tid][id] || a.CreatedAt.IsZero() || a.CreatedAt.After(cutoff) || !IsTerminal(string(a.State)) || removed >= limit {
			continue
		}
		delete(docs.data[tid], id)
		*a = AnalysisResult{ID: a.ID, CurrentRunID: a.CurrentRunID, Name: "已回收分析", State: a.State, Progress: a.Progress, CreatedAt: a.CreatedAt, CreatedBy: a.CreatedBy, StartedAt: a.StartedAt, FinishedAt: a.FinishedAt, DocCount: a.DocCount}
		m.purged[tid][id] = true
		removed++
	}
	for id, a := range m.byTenant[tid] {
		if !m.purged[tid][id] {
			if a.CreatedAt.IsZero() {
				return false, pkgerrors.ErrConflict
			}
			return false, nil
		}
	}
	if len(docs.data[tid]) > 0 {
		return false, pkgerrors.ErrConflict
	}
	return true, nil
}
