package report

import (
	"context"
	"strings"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billing"
)

func newTestReportService(t *testing.T) (*Service, *MemoryStore, map[string]string) {
	t.Helper()
	st := NewMemoryStore()
	tenantPlans := make(map[string]string)
	plans := billing.DefaultPlans()
	svc := NewService(st,
		func(tenantID string) string { return tenantPlans[tenantID] },
		func(code string) *billing.Plan { return plans[code] },
	)
	return svc, st, tenantPlans
}

func createReport(t *testing.T, svc *Service, tenantID, analysisID, format string) *Report {
	t.Helper()
	r, err := svc.CreateFromAnalysis(context.Background(), tenantID, analysisID, format, "user-1")
	if err != nil {
		t.Fatalf("CreateFromAnalysis(%s) failed: %v", format, err)
	}
	return r
}

func TestServiceCreateFromAnalysis_allFormats(t *testing.T) {
	svc, _, _ := newTestReportService(t)

	for _, format := range Formats {
		r := createReport(t, svc, "tenant-1", "analysis-1", format)

		if r.ID == "" || len(r.ID) != 26 {
			t.Errorf("[%s] ID = %q, want 26-char ULID", format, r.ID)
		}
		if r.AnalysisID != "analysis-1" {
			t.Errorf("[%s] AnalysisID = %q, want analysis-1", format, r.AnalysisID)
		}
		if r.Status != "completed" {
			t.Errorf("[%s] Status = %q, want completed", format, r.Status)
		}
		wantKey := "reports/" + r.ID + "." + format
		if r.FileKey != wantKey {
			t.Errorf("[%s] FileKey = %q, want %q", format, r.FileKey, wantKey)
		}
	}
}

func TestServiceCreateFromAnalysis_unsupportedFormat(t *testing.T) {
	svc, st, _ := newTestReportService(t)

	for _, format := range []string{"", "md", "exe", "HTML"} {
		if _, err := svc.CreateFromAnalysis(context.Background(), "tenant-1", "analysis-1", format, "user-1"); err == nil {
			t.Errorf("CreateFromAnalysis(%q): expected error", format)
		}
	}
	if len(st.byTenant) != 0 {
		t.Error("failed creates must not persist anything")
	}
}

func TestServiceCreateFromAnalysis_missingArguments(t *testing.T) {
	svc, _, _ := newTestReportService(t)

	if _, err := svc.CreateFromAnalysis(context.Background(), "", "analysis-1", "html", "user-1"); err == nil {
		t.Error("empty tenant_id: expected error")
	}
	if _, err := svc.CreateFromAnalysis(context.Background(), "tenant-1", "", "html", "user-1"); err == nil {
		t.Error("empty analysis_id: expected error")
	}
}

func TestServiceGet_found(t *testing.T) {
	svc, _, _ := newTestReportService(t)
	want := createReport(t, svc, "tenant-1", "analysis-1", "html")

	got, err := svc.Get(context.Background(), "tenant-1", want.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != want.ID || got.AnalysisID != want.AnalysisID || got.Format != "html" {
		t.Errorf("got %+v, want report %+v", got, want)
	}
}

func TestServiceGet_notFoundAndIsolation(t *testing.T) {
	svc, _, _ := newTestReportService(t)
	r := createReport(t, svc, "tenant-a", "analysis-1", "html")

	if _, err := svc.Get(context.Background(), "tenant-a", "01MISSINGNOTFOUND000000000"); err == nil {
		t.Fatal("expected error for missing report")
	} else if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}

	if _, err := svc.Get(context.Background(), "tenant-b", r.ID); err == nil {
		t.Fatal("expected ErrNotFound for another tenant's report")
	} else if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceList_emptyAndCreated(t *testing.T) {
	svc, _, _ := newTestReportService(t)

	empty, err := svc.List(context.Background(), "tenant-empty")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty tenant list len = %d, want 0", len(empty))
	}

	a := createReport(t, svc, "tenant-1", "analysis-1", "html")
	b := createReport(t, svc, "tenant-1", "analysis-2", "markdown")
	createReport(t, svc, "tenant-2", "analysis-3", "html")

	got, err := svc.List(context.Background(), "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("tenant-1 list len = %d, want 2", len(got))
	}
	if got[0].ID != a.ID || got[1].ID != b.ID {
		t.Errorf("list order = [%s %s], want [%s %s] (creation order)", got[0].ID, got[1].ID, a.ID, b.ID)
	}
}

func TestServiceDownloadURL_returnsDownloadLink(t *testing.T) {
	svc, _, tenantPlans := newTestReportService(t)
	tenantPlans["tenant-1"] = "free"
	r := createReport(t, svc, "tenant-1", "analysis-1", "html")

	url, err := svc.DownloadURL(context.Background(), "tenant-1", r.ID, "html")
	if err != nil {
		t.Fatalf("DownloadURL failed: %v", err)
	}
	if !strings.Contains(url, r.ID) || !strings.Contains(url, "format=html") {
		t.Errorf("DownloadURL = %q, want it to reference report %s as html", url, r.ID)
	}
}

func TestServiceDownloadURL_planGating(t *testing.T) {
	tests := []struct {
		name     string
		planCode string
		format   string
		wantErr  bool
	}{
		{"free can download html", "free", "html", false},
		{"free cannot download markdown", "free", "markdown", true},
		{"lite can download markdown", "lite", "markdown", false},
		{"lite cannot download pdf", "lite", "pdf", true},
		{"pro can download pdf", "pro", "pdf", false},
		{"pro cannot download docx", "pro", "docx", true},
		{"enterprise can download everything", "enterprise", "docx", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, tenantPlans := newTestReportService(t)
			tenantPlans["tenant-g"] = tt.planCode
			r := createReport(t, svc, "tenant-g", "analysis-1", tt.format)

			_, err := svc.DownloadURL(context.Background(), "tenant-g", r.ID, tt.format)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected format %s to be gated for plan %s", tt.format, tt.planCode)
				}
				if !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
					t.Errorf("error = %v, want ErrForbidden", err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestServiceDownloadURL_unknownPlanOrTenant(t *testing.T) {
	svc, _, tenantPlans := newTestReportService(t)

	// Tenant with an unknown plan code: gating must fail closed.
	tenantPlans["tenant-mystery"] = "platinum"
	r := createReport(t, svc, "tenant-mystery", "analysis-1", "pdf")
	if _, err := svc.DownloadURL(context.Background(), "tenant-mystery", r.ID, "pdf"); err == nil {
		t.Error("expected ErrForbidden for unknown plan code")
	} else if !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}

	// Tenant never seen before defaults to the free plan (html allowed, markdown not).
	r2 := createReport(t, svc, "tenant-new", "analysis-2", "markdown")
	if _, err := svc.DownloadURL(context.Background(), "tenant-new", r2.ID, "markdown"); err == nil {
		t.Error("expected markdown to be gated for a tenant without a plan record")
	}
}

func TestServiceDownloadURL_formatMismatch(t *testing.T) {
	svc, _, tenantPlans := newTestReportService(t)
	tenantPlans["tenant-1"] = "enterprise"
	r := createReport(t, svc, "tenant-1", "analysis-1", "pdf")

	_, err := svc.DownloadURL(context.Background(), "tenant-1", r.ID, "docx")
	if err == nil {
		t.Fatal("expected error when requested format differs from the report's format")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestServiceDownloadURL_notFound(t *testing.T) {
	svc, _, _ := newTestReportService(t)
	_, err := svc.DownloadURL(context.Background(), "tenant-1", "01MISSINGNOTFOUND000000000", "html")
	if err == nil {
		t.Fatal("expected error for missing report")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_UpdateStatusAndFilter(t *testing.T) {
	st := NewMemoryStore()
	r := Report{ID: "report-1", AnalysisID: "analysis-1", Title: "", Format: "html", Status: "queued", FileKey: "reports/report-1.html"}
	if err := st.Create(context.Background(), "tenant-1", "user-1", r); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := st.UpdateStatus(context.Background(), "tenant-1", r.ID, "completed"); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	got, err := st.Get(context.Background(), "tenant-1", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}

	// Filter narrows to one analysis.
	byAnalysis, err := st.List(context.Background(), "tenant-1", Filter{AnalysisID: "analysis-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAnalysis) != 1 {
		t.Errorf("filtered list len = %d, want 1", len(byAnalysis))
	}
	none, err := st.List(context.Background(), "tenant-1", Filter{Status: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("status-filtered list len = %d, want 0", len(none))
	}
}
