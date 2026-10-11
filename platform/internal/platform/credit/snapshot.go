package credit

import (
	"context"
	"fmt"
)

// Snapshot reads the actual pool without creating a row or assuming a plan.
// A nil snapshot is different from an existing pool with a zero balance.
type Snapshot struct {
	Balance  int    `json:"balance"`
	PlanCode string `json:"plan_code"`
	Version  int64  `json:"version"`
}
type snapshotStore interface {
	Snapshot(context.Context, string) (*Snapshot, error)
}

func (s *Service) Snapshot(ctx context.Context, tenantID string) (*Snapshot, error) {
	store, ok := s.store.(snapshotStore)
	if !ok {
		return nil, fmt.Errorf("credit pool snapshot unavailable")
	}
	return store.Snapshot(ctx, tenantID)
}
