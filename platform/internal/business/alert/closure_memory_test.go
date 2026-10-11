package alert

import (
	"context"
	"testing"
	"time"
)

func TestClosureMemoryAlertRetentionAndWriteFence(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	for _, a := range []Alert{{ID: "old", CreatedAt: now.Add(-40 * 24 * time.Hour)}, {ID: "new", CreatedAt: now}} {
		if err := s.Create(ctx, "closing", a); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Create(ctx, "other", Alert{ID: "other", CreatedAt: now.Add(-400 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	p, ok := any(s).(interface {
		CleanupClosedTenant(context.Context, string, time.Time, int) (bool, error)
	})
	if !ok {
		t.Fatal("memory alert retention and write fence unavailable")
	}
	done, err := p.CleanupClosedTenant(ctx, "closing", now.Add(-30*24*time.Hour), 1)
	if err != nil || done {
		t.Fatalf("retained alert lost: %v %v", done, err)
	}
	if _, err = s.Get(ctx, "closing", "old"); err == nil {
		t.Fatal("expired alert retained")
	}
	if _, err = s.Get(ctx, "other", "other"); err != nil {
		t.Fatal("other alert lost")
	}
	if err = s.Create(ctx, "closing", Alert{ID: "late", CreatedAt: now}); err == nil {
		t.Fatal("late alert created after fence")
	}
	done, err = p.CleanupClosedTenant(ctx, "closing", now.Add(24*time.Hour), 1)
	if err != nil || !done {
		t.Fatalf("alert retry: %v %v", done, err)
	}
}
