package analysis

import (
	"context"
	"errors"
	"github.com/yuqing/platform/internal/business/report"
	"io"
	"log/slog"
	"testing"
	"time"
)

type canceledFetcher struct{ cancel context.CancelFunc }

func (f canceledFetcher) Fetch(ctx context.Context, _ FetchRequest) ([]Document, error) {
	f.cancel()
	return nil, ctx.Err()
}

type canceledWrappedFetcher struct{ cancel context.CancelFunc }

func (f canceledWrappedFetcher) Fetch(context.Context, FetchRequest) ([]Document, error) {
	f.cancel()
	return nil, errors.New("wrapped PostgreSQL error")
}

func TestPipelineShutdownWithWrappedStorageErrorRemainsRecoverable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, svc := newTestPipeline(t, canceledWrappedFetcher{cancel: cancel}, 5*time.Second)
	a := createAnalysis(t, svc, "wrapped-tenant")
	if err := p.Handle(ctx, TaskMessage{TenantID: "wrapped-tenant", AnalysisID: a.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown err=%v", err)
	}
	got, err := svc.Get(context.Background(), "wrapped-tenant", a.ID)
	if err != nil || got.State != StateFetching {
		t.Fatalf("state=%v err=%v", got, err)
	}
}

func TestPipelineShutdownDoesNotMakeInterruptedTaskTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, svc := newTestPipeline(t, canceledFetcher{cancel: cancel}, 5*time.Second)
	a := createAnalysis(t, svc, "shutdown-tenant")
	if err := p.Handle(ctx, TaskMessage{TenantID: "shutdown-tenant", AnalysisID: a.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown err=%v", err)
	}
	got, err := svc.Get(context.Background(), "shutdown-tenant", a.ID)
	if err != nil || got.State != StateFetching {
		t.Fatalf("state=%v err=%v; must remain recoverable", got, err)
	}
}

type checkingReportService struct {
	svc   *Service
	state State
	calls int
}

func (s *checkingReportService) CreateFromAnalysis(ctx context.Context, tenantID, analysisID, _, _ string) (string, error) {
	a, err := s.svc.Get(ctx, tenantID, analysisID)
	if err != nil {
		return "", err
	}
	s.state = a.State
	s.calls++
	return "record", nil
}

func TestPipelineReportRecordOnlyAfterCompletion(t *testing.T) {
	p, svc := newTestPipeline(t, &fakeFetcher{docs: sampleDocs(1)}, 5*time.Second)
	p.WithGenerator(&fakeGenerator{res: ReportResult{ReportID: "generated", Content: "report"}})
	record := &checkingReportService{svc: svc}
	p.WithReportSvc(record)
	a := createAnalysis(t, svc, "report-tenant")
	if err := p.Handle(context.Background(), TaskMessage{TenantID: "report-tenant", AnalysisID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if record.calls != 1 || record.state != StateCompleted {
		t.Fatalf("report record calls=%d created in state=%s", record.calls, record.state)
	}
}

func TestMemoryPipelineTerminalReplayKeepsPriorBehavior(t *testing.T) {
	legacy := newFakeReportSvc()
	p, svc := newTestPipelineWithReportSvc(t, &fakeFetcher{docs: sampleDocs(1)}, legacy)
	a := createAnalysis(t, svc, "memory-replay-tenant")
	msg := TaskMessage{TenantID: "memory-replay-tenant", AnalysisID: a.ID}
	if err := p.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := p.Handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(legacy.calls) != 1 {
		t.Fatalf("memory replay created %d report records", len(legacy.calls))
	}
}

type retryableReportService struct {
	svc      *report.Service
	failOnce bool
}

func (r *retryableReportService) CreateFromAnalysis(ctx context.Context, tenantID, analysisID, format, createdBy string) (string, error) {
	created, err := r.svc.CreateFromAnalysis(ctx, tenantID, analysisID, format, createdBy)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}
func (r *retryableReportService) CreateFromAnalysisOnce(ctx context.Context, tenantID, analysisID, format, createdBy, runKey string) (string, error) {
	if r.failOnce {
		r.failOnce = false
		return "", errors.New("temporary report store failure")
	}
	created, err := r.svc.CreateFromAnalysisOnce(ctx, tenantID, analysisID, format, createdBy, runKey)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func TestCompletedReportRecordFailureReconcilesOnTerminalRedelivery(t *testing.T) {
	fetcher := &fakeFetcher{docs: sampleDocs(1)}
	p, svc := newTestPipeline(t, fetcher, 5*time.Second)
	p.WithGenerator(&fakeGenerator{res: ReportResult{ReportID: "generated", Content: "report"}})
	reports := report.NewService(report.NewMemoryStore(), nil, nil)
	p.WithReportSvc(&retryableReportService{svc: reports, failOnce: true})
	a := createAnalysis(t, svc, "report-retry-tenant")
	msg := TaskMessage{TenantID: "report-retry-tenant", AnalysisID: a.ID}
	if err := p.Handle(context.Background(), msg); err == nil {
		t.Fatal("report store failure was ACK-able")
	}
	state, err := svc.Get(context.Background(), msg.TenantID, msg.AnalysisID)
	if err != nil || state.State != StateCompleted {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	for range 2 {
		if err := p.Handle(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := reports.List(context.Background(), msg.TenantID)
	if err != nil || len(stored) != 1 || fetcher.calls != 1 {
		t.Fatalf("reports=%+v fetches=%d err=%v", stored, fetcher.calls, err)
	}
}

func TestCanceledTaskRejectsLateResults(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	a := createAnalysis(t, svc, "cancel-tenant")
	ctx := context.Background()
	if err := svc.Cancel(ctx, "cancel-tenant", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetInsight(ctx, "cancel-tenant", a.ID, InsightResult{Summary: "late"}); err == nil {
		t.Fatal("late insight accepted")
	}
	if err := svc.SetReport(ctx, "cancel-tenant", a.ID, "late", "late"); err == nil {
		t.Fatal("late report accepted")
	}
	if err := svc.SetWarning(ctx, "cancel-tenant", a.ID, "late warning"); err == nil {
		t.Fatal("late warning accepted")
	}
	svc.AddDocuments(ctx, "cancel-tenant", a.ID, sampleDocs(1))
	if docs := svc.Documents(ctx, "cancel-tenant", a.ID); len(docs) != 0 {
		t.Fatalf("late documents persisted: %+v", docs)
	}
	p := NewPipeline(svc, &fakeFetcher{}, 5*time.Second, nil)
	p.setDocCount(ctx, TaskMessage{TenantID: "cancel-tenant", AnalysisID: a.ID}, 10)
	got, err := svc.Get(ctx, "cancel-tenant", a.ID)
	if err != nil || got.DocCount != 0 || got.Warning != "" {
		t.Fatalf("canceled task mutated: %+v err=%v", got, err)
	}
}

type failingDocumentStore struct{ documentStore }

func (f failingDocumentStore) add(context.Context, string, string, []Document) error {
	return errors.New("storage unavailable")
}

func TestPipelineDocumentWriteFailureDoesNotComplete(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	svc.docs = failingDocumentStore{documentStore: svc.docs}
	p := NewPipeline(svc, &fakeFetcher{docs: sampleDocs(1)}, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a := createAnalysis(t, svc, "document-tenant")
	if err := p.Handle(context.Background(), TaskMessage{TenantID: "document-tenant", AnalysisID: a.ID}); err == nil {
		t.Fatal("document write error swallowed")
	}
	got, err := svc.Get(context.Background(), "document-tenant", a.ID)
	if err != nil || got.State == StateCompleted {
		t.Fatalf("false success state=%v err=%v", got, err)
	}
}
