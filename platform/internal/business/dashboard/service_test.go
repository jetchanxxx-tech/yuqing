package dashboard

import (
	"context"
	"math"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/pkg/queue"
)

func newTestDashboard(t *testing.T) (*Service, *analysis.Service) {
	t.Helper()
	q := queue.NewMemory()
	t.Cleanup(func() { _ = q.Close() })
	analysisSvc := analysis.NewService(q, 4)
	svc := NewService(analysisSvc, nil)
	return svc, analysisSvc
}

func createQueued(t *testing.T, svc *analysis.Service, tenantID string) *analysis.AnalysisResult {
	t.Helper()
	a, err := svc.Create(context.Background(), analysis.CreateAnalysisRequest{
		TenantID: tenantID, UserID: "user-1", Name: "雅阁后排舆情", AnalysisType: "brand",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	return a
}

// complete walks an analysis through the pipeline to the completed state.
func complete(t *testing.T, svc *analysis.Service, tenantID, id string) {
	t.Helper()
	for _, to := range []string{"acquiring_budget", "fetching", "analyzing", "generating_report", "completed"} {
		if err := svc.Transition(context.Background(), tenantID, id, to); err != nil {
			t.Fatalf("Transition to %q failed: %v", to, err)
		}
	}
}

func cancel(t *testing.T, svc *analysis.Service, tenantID, id string) {
	t.Helper()
	if err := svc.Cancel(context.Background(), tenantID, id); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.001
}

func TestOverview_emptyTenant(t *testing.T) {
	svc, _ := newTestDashboard(t)
	got, err := svc.Overview(context.Background(), "tenant-empty")
	if err != nil {
		t.Fatalf("Overview failed: %v", err)
	}
	if got.TotalAnalyses != 0 || got.ActiveTasks != 0 {
		t.Errorf("empty overview = %+v, want zero totals", got)
	}
	if got.SuccessRate != 0 || math.IsNaN(got.SuccessRate) {
		t.Errorf("SuccessRate = %v, want 0 (no NaN on empty)", got.SuccessRate)
	}
}

func TestOverview_countsStatesAndSuccessRate(t *testing.T) {
	svc, aSvc := newTestDashboard(t)
	createQueued(t, aSvc, "tenant-1")
	second := createQueued(t, aSvc, "tenant-1")
	complete(t, aSvc, "tenant-1", second.ID)
	third := createQueued(t, aSvc, "tenant-1")
	cancel(t, aSvc, "tenant-1", third.ID)

	got, err := svc.Overview(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Overview failed: %v", err)
	}
	if got.TotalAnalyses != 3 {
		t.Errorf("TotalAnalyses = %d, want 3", got.TotalAnalyses)
	}
	if got.ActiveTasks != 1 {
		t.Errorf("ActiveTasks = %d, want 1 (one queued, one completed, one canceled)", got.ActiveTasks)
	}
	if !almostEqual(got.SuccessRate, 100.0/3.0) {
		t.Errorf("SuccessRate = %v, want %v", got.SuccessRate, 100.0/3.0)
	}
}

func TestOverview_tenantIsolation(t *testing.T) {
	svc, aSvc := newTestDashboard(t)
	createQueued(t, aSvc, "tenant-a")

	got, err := svc.Overview(context.Background(), "tenant-b")
	if err != nil {
		t.Fatalf("Overview failed: %v", err)
	}
	if got.TotalAnalyses != 0 {
		t.Errorf("tenant-b TotalAnalyses = %d, want 0 (analyses are tenant-scoped)", got.TotalAnalyses)
	}
}

func TestTrend_groupsByCreatedDate(t *testing.T) {
	svc, aSvc := newTestDashboard(t)
	first := createQueued(t, aSvc, "tenant-1")
	createQueued(t, aSvc, "tenant-1")
	second := createQueued(t, aSvc, "tenant-2")

	got, err := svc.Trend(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Trend failed: %v", err)
	}
	if len(got.Dates) != 1 || len(got.Counts) != 1 {
		t.Fatalf("Trend = %d dates/%d counts, want 1/1 for same-day creations", len(got.Dates), len(got.Counts))
	}
	wantDate := first.CreatedAt.Format("2006-01-02")
	if got.Dates[0] != wantDate {
		t.Errorf("Dates[0] = %q, want %q", got.Dates[0], wantDate)
	}
	if got.Counts[0] != 2 {
		t.Errorf("Counts[0] = %d, want 2", got.Counts[0])
	}

	// Another tenant sees only its own analyses.
	other, err := svc.Trend(context.Background(), "tenant-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Dates) != 1 || other.Counts[0] != 1 {
		t.Errorf("tenant-2 trend = %v, want single date with count 1", other)
	}
	_ = second
}

func TestTrend_emptyTenant(t *testing.T) {
	svc, _ := newTestDashboard(t)
	got, err := svc.Trend(context.Background(), "tenant-empty")
	if err != nil {
		t.Fatalf("Trend failed: %v", err)
	}
	if len(got.Dates) != 0 || len(got.Counts) != 0 {
		t.Errorf("empty trend = %+v, want no dates", got)
	}
}

func TestSources_fixedFourRows(t *testing.T) {
	svc, _ := newTestDashboard(t)

	got, err := svc.Sources(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Sources failed: %v", err)
	}
	if len(got.Sources) != 4 {
		t.Fatalf("len(Sources) = %d, want 4 fixed source rows", len(got.Sources))
	}

	total := 0.0
	for _, s := range got.Sources {
		if s.Name == "" {
			t.Error("source row with empty name")
		}
		if s.Count < 0 {
			t.Errorf("source %s has negative count %d", s.Name, s.Count)
		}
		total += s.Pct
	}
	if !almostEqual(total, 100) {
		t.Errorf("percentages sum to %v, want 100", total)
	}

	// Fixed rows are tenant-independent placeholders for the MVP.
	other, err := svc.Sources(context.Background(), "tenant-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Sources) != 4 || other.Sources[0].Name != got.Sources[0].Name {
		t.Errorf("sources differ across tenants: %+v vs %+v", got.Sources, other.Sources)
	}
}

func TestTopics_predefinedFour(t *testing.T) {
	svc, _ := newTestDashboard(t)

	got, err := svc.Topics(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("Topics failed: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len(Topics) = %d, want 4 predefined topics", len(got))
	}
	for _, tp := range got {
		if tp.Name == "" {
			t.Error("topic with empty name")
		}
		if tp.DocCount < 0 {
			t.Errorf("topic %s has negative doc count", tp.Name)
		}
		switch tp.Trend {
		case "rising", "stable", "falling":
		default:
			t.Errorf("topic %s has invalid trend %q", tp.Name, tp.Trend)
		}
	}

	other, err := svc.Topics(context.Background(), "tenant-2")
	if err != nil {
		t.Fatal(err)
	}
	if other[0].Name != got[0].Name || other[1].Name != got[1].Name {
		t.Errorf("topics differ across tenants: %+v vs %+v", got, other)
	}
}
