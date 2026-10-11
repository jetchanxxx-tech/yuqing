package analysis

import (
	"context"
	"github.com/yuqing/platform/internal/pkg/queue"
	"testing"
	"time"
)

func TestClosureMemoryAnalysisRetentionAndLateDocumentFence(t *testing.T) {
	ctx := context.Background()
	s := NewService(queue.NewMemory(), 1)
	m := s.store.(*memoryStore)
	now := time.Now().UTC()
	for _, a := range []AnalysisResult{{ID: "old", State: StateCompleted, Name: "Old private", CreatedAt: now.Add(-40 * 24 * time.Hour), ReportContent: "private"}, {ID: "new", State: StateCompleted, Name: "Retained", CreatedAt: now, ReportContent: "keep"}} {
		if err := m.put(ctx, "closing", &a); err != nil {
			t.Fatal(err)
		}
	}
	other := AnalysisResult{ID: "other", State: StateCompleted, Name: "Other", CreatedAt: now.Add(-400 * 24 * time.Hour), ReportContent: "other data"}
	if err := m.put(ctx, "other", &other); err != nil {
		t.Fatal(err)
	}
	if err := s.docs.add(ctx, "closing", "old", []Document{{ID: "old-doc", Content: "private doc"}}); err != nil {
		t.Fatal(err)
	}
	p, ok := any(s).(interface {
		CleanupClosedTenant(context.Context, string, time.Time, int) (bool, error)
	})
	if !ok {
		t.Fatal("memory analysis retention and write fence unavailable")
	}
	done, err := p.CleanupClosedTenant(ctx, "closing", now.Add(-30*24*time.Hour), 1)
	if err != nil || done {
		t.Fatalf("future data incorrectly cleared: %v %v", done, err)
	}
	old, err := s.Get(ctx, "closing", "old")
	if err != nil || old.ReportContent != "" || len(s.Documents(ctx, "closing", "old")) != 0 {
		t.Fatal("expired analysis was not scrubbed with its documents")
	}
	if err = s.SaveDocuments(ctx, "closing", "old", []Document{{ID: "late", Content: "late PII"}}); err == nil {
		t.Fatal("a previously admitted document writer survived the tenant fence")
	}
	kept, err := s.Get(ctx, "other", "other")
	if err != nil || kept.ReportContent != "other data" {
		t.Fatal("other tenant data changed")
	}
	done, err = p.CleanupClosedTenant(ctx, "closing", now.Add(24*time.Hour), 1)
	if err != nil || !done {
		t.Fatalf("retention retry: %v %v", done, err)
	}
}
