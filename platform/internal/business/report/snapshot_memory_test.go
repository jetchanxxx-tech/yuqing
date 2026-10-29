package report

import (
	"context"
	"testing"
)

func TestMemoryReportSnapshotUsesCompletedContentInsteadOfMutableAnalysis(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, nil)
	type completedSnapshotCreator interface {
		CreateFromAnalysisSnapshot(context.Context, string, string, string, string, string) (*Report, error)
	}
	creator, ok := any(svc).(completedSnapshotCreator)
	if !ok {
		t.Fatal("memory report creation cannot bind the completed run content")
	}
	ctx := context.Background()
	completed := "<p>run A</p>"
	first, err := creator.CreateFromAnalysisSnapshot(ctx, "tenant", "analysis", "html", "user", completed)
	if err != nil {
		t.Fatal(err)
	}
	completed = "<p>run B</p>"
	if _, err := creator.CreateFromAnalysisSnapshot(ctx, "tenant", "analysis", "html", "user", completed); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "tenant", first.ID)
	if err != nil || got.ContentHTML != "<p>run A</p>" || !got.SnapshotAvailable {
		t.Fatalf("wrong run snapshot: %+v %v", got, err)
	}
}
