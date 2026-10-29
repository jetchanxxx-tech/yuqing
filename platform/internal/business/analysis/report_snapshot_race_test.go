package analysis

import (
	"context"
	"testing"
	"time"
)

type rerunAtCompletionStore struct {
	analysisStore
	after func()
	fired bool
}

func (s *rerunAtCompletionStore) mutate(ctx context.Context, tenantID, analysisID string, fn func(*AnalysisResult) error) error {
	completed := false
	err := s.analysisStore.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		previous := a.State
		err := fn(a)
		completed = err == nil && previous != StateCompleted && a.State == StateCompleted
		return err
	})
	if err == nil && completed && !s.fired {
		s.fired = true
		s.after()
	}
	return err
}

type completedSnapshotRecorder struct{ contents []string }

func (s *completedSnapshotRecorder) CreateFromAnalysis(context.Context, string, string, string, string) (string, error) {
	return "legacy", nil
}

func (s *completedSnapshotRecorder) CreateFromAnalysisSnapshot(_ context.Context, _, _, _, _, content string) (string, error) {
	s.contents = append(s.contents, content)
	return "snapshot", nil
}

func TestMemoryPipelineSnapshotSurvivesImmediateRerunAfterCompletion(t *testing.T) {
	ctx := context.Background()
	p, svc := newTestPipeline(t, &fakeFetcher{docs: []Document{{ID: "doc", Content: "original"}}}, time.Minute)
	a, err := svc.Create(ctx, CreateAnalysisRequest{TenantID: "tenant", Name: "snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &rerunAtCompletionStore{analysisStore: svc.store}
	wrapped.after = func() {
		if err := svc.Rerun(ctx, "tenant", a.ID); err != nil {
			t.Fatal(err)
		}
	}
	svc.store = wrapped
	recorder := &completedSnapshotRecorder{}
	p.WithGenerator(&fakeGenerator{res: ReportResult{ReportID: "engine-a", Content: "<p>run A</p>"}}).WithReportSvc(recorder)
	if err := p.Handle(ctx, TaskMessage{TenantID: "tenant", AnalysisID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if !wrapped.fired || len(recorder.contents) != 1 || recorder.contents[0] != "<p>run A</p>" {
		t.Fatalf("snapshot lost or captured a rerun: %+v", recorder.contents)
	}
	current, err := svc.Get(ctx, "tenant", a.ID)
	if err != nil || current.State != StateQueued || current.ReportContent != "" {
		t.Fatalf("rerun fixture did not clear the live analysis: %+v %v", current, err)
	}
}
