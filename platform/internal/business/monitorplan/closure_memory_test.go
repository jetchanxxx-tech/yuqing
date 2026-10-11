package monitorplan

import (
	"context"
	"testing"
	"time"
)

func TestClosureMemoryMonitorRetentionAndWriteFence(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	s.plans[memoryKey("closing", "old")] = Plan{ID: "old", TenantID: "closing", CreatedAt: now.Add(-40 * 24 * time.Hour)}
	s.plans[memoryKey("closing", "new")] = Plan{ID: "new", TenantID: "closing", CreatedAt: now}
	s.plans[memoryKey("other", "other")] = Plan{ID: "other", TenantID: "other", CreatedAt: now.Add(-400 * 24 * time.Hour)}
	p, ok := any(s).(interface {
		CleanupClosedTenant(context.Context, string, time.Time, int) (bool, error)
	})
	if !ok {
		t.Fatal("memory monitor retention and write fence unavailable")
	}
	done, err := p.CleanupClosedTenant(ctx, "closing", now.Add(-30*24*time.Hour), 1)
	if err != nil || done {
		t.Fatalf("retained monitor lost: %v %v", done, err)
	}
	if _, err = s.Get(ctx, "closing", "old"); err == nil {
		t.Fatal("expired monitor retained")
	}
	if _, err = s.Get(ctx, "other", "other"); err != nil {
		t.Fatal("other monitor lost")
	}
	done, err = p.CleanupClosedTenant(ctx, "closing", now.Add(24*time.Hour), 1)
	if err != nil || !done {
		t.Fatalf("monitor retry: %v %v", done, err)
	}
}
