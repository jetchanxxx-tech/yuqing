package monitorplan

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/yuqing/platform/internal/pkg/id"
)

type MemoryStore struct {
	mu    sync.Mutex
	plans map[string]Plan
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{plans: make(map[string]Plan)} }

func memoryKey(tenant, id string) string { return tenant + "/" + id }

func sortPlans(plans []Plan) {
	sort.Slice(plans, func(i, j int) bool {
		if plans[i].CreatedAt.Equal(plans[j].CreatedAt) {
			return plans[i].ID > plans[j].ID
		}
		return plans[i].CreatedAt.After(plans[j].CreatedAt)
	})
}

func (s *MemoryStore) Create(_ context.Context, draft Plan) (*Plan, error) {
	if err := validateDraft(draft); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if draft.ID == "" {
		draft.ID = id.New()
	}
	if _, exists := s.plans[memoryKey(draft.TenantID, draft.ID)]; exists {
		return nil, ErrConflict
	}
	draft.State, draft.Revision = "draft", 1
	draft.CreatedAt, draft.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	s.plans[memoryKey(draft.TenantID, draft.ID)] = ClonePlan(draft)
	return &draft, nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, planID string) (*Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[memoryKey(tenantID, planID)]
	if !ok || tenantID == "" {
		return nil, ErrNotFound
	}
	copy := ClonePlan(plan)
	return &copy, nil
}

func (s *MemoryStore) Update(_ context.Context, tenantID string, draft Plan) (*Plan, error) {
	if err := validateDraft(draft); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.plans[memoryKey(tenantID, draft.ID)]
	if !ok || tenantID == "" || tenantID != draft.TenantID || old.OwnerID != draft.OwnerID || old.TemplateID != draft.TemplateID || old.TemplateVersion != draft.TemplateVersion {
		return nil, ErrNotFound
	}
	if old.Revision != draft.Revision {
		return nil, ErrConflict
	}
	draft.State, draft.CreatedAt = old.State, old.CreatedAt
	draft.Revision, draft.UpdatedAt = old.Revision+1, time.Now().UTC()
	s.plans[memoryKey(tenantID, draft.ID)] = ClonePlan(draft)
	return &draft, nil
}

func (s *MemoryStore) List(_ context.Context, tenantID string, limit, offset int) ([]Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Plan{}
	if tenantID == "" || limit < 1 || limit > 100 || offset < 0 {
		return result, nil
	}
	for _, plan := range s.plans {
		if plan.TenantID == tenantID {
			result = append(result, ClonePlan(plan))
		}
	}
	// Stable order for pagination, even when drafts share the same clock tick.
	sortPlans(result)
	if offset >= len(result) {
		return []Plan{}, nil
	}
	result = result[offset:]
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *MemoryStore) Delete(_ context.Context, tenantID, ownerID, planID string, revision int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[memoryKey(tenantID, planID)]
	if !ok || tenantID == "" || plan.OwnerID != ownerID {
		return ErrNotFound
	}
	if plan.Revision != revision {
		return ErrConflict
	}
	delete(s.plans, memoryKey(tenantID, planID))
	return nil
}
