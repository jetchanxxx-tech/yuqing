package report

import (
	"context"
	"testing"
	"time"
)

func TestClosureMemoryReportRetentionPreservesOtherTenantAndRejectsLateCreate(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	old, ancient := now.Add(-40*24*time.Hour), now.Add(-400*24*time.Hour)
	for _, r := range []Report{{ID: "old", CreatedAt: &old, Format: "html", FileKey: "reports/old.html"}, {ID: "new", CreatedAt: &now, Format: "html", FileKey: "reports/new.html"}} {
		if err := s.Create(ctx, "closing", "owner", r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Create(ctx, "other", "other", Report{ID: "other", Format: "html", FileKey: "reports/other.html", CreatedAt: &ancient}); err != nil {
		t.Fatal(err)
	}
	p, ok := any(s).(interface {
		CleanupClosedTenant(context.Context, string, time.Time, int) (bool, error)
	})
	if !ok {
		t.Fatal("memory report retention and write fence unavailable")
	}
	done, err := p.CleanupClosedTenant(ctx, "closing", now.Add(-30*24*time.Hour), 1)
	if err != nil || done {
		t.Fatalf("retained report lost: %v %v", done, err)
	}
	if _, err = s.Get(ctx, "closing", "old"); err == nil {
		t.Fatal("expired report retained")
	}
	if _, err = s.Get(ctx, "other", "other"); err != nil {
		t.Fatal("other tenant report lost")
	}
	if err = s.Create(ctx, "closing", "owner", Report{ID: "late", CreatedAt: &now, Format: "html"}); err == nil {
		t.Fatal("late report created after fence")
	}
	done, err = p.CleanupClosedTenant(ctx, "closing", now.Add(24*time.Hour), 1)
	if err != nil || !done {
		t.Fatalf("report retry: %v %v", done, err)
	}
}
