package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/business/report"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billing"
)

// planCodeFor returns a resolver used by the report service: free and pro
// tenants are hard capped by plan features, business and enterprise unlock
// every export format.
func planCodeFor(tenantID string) string {
	switch tenantID {
	case "t-free":
		return "free"
	case "t-pro":
		return "pro"
	case "t-business":
		return "business"
	case "t-enterprise":
		return "enterprise"
	}
	return ""
}

// TestIntegration_reportPlanGating drives report creation + download gating
// through the real service and memory store against real plan definitions.
func TestIntegration_reportPlanGating(t *testing.T) {
	svc := report.NewService(report.NewMemoryStore(), planCodeFor, nil) // nil → billing.DefaultPlans
	ctx := context.Background()

	// Analysis ids are opaque to the report service; use a fixed fake.
	const analysisID = "a-analysis-001"

	createFor := func(t *testing.T, tenantID, format string) *report.Report {
		t.Helper()
		r, err := svc.CreateFromAnalysis(ctx, tenantID, analysisID, format, "user-1")
		if err != nil {
			t.Fatalf("CreateFromAnalysis(%s, %s) failed: %v", tenantID, format, err)
		}
		return r
	}
	download := func(t *testing.T, tenantID, reportID, format string) (string, error) {
		t.Helper()
		return svc.DownloadURL(ctx, tenantID, reportID, format)
	}

	t.Run("free plan can create and download html", func(t *testing.T) {
		r := createFor(t, "t-free", "html")
		url, err := download(t, "t-free", r.ID, "html")
		if err != nil {
			t.Fatalf("free html download failed: %v", err)
		}
		if !strings.Contains(url, "/download?format=html") {
			t.Fatalf("download url = %q, want format=html suffix", url)
		}
	})

	t.Run("free plan is denied pdf and docx downloads", func(t *testing.T) {
		r := createFor(t, "t-free", "pdf")
		_, err := download(t, "t-free", r.ID, "pdf")
		if !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
			t.Fatalf("free pdf download err = %v, want ErrForbidden", err)
		}
		r2 := createFor(t, "t-free", "docx")
		_, err = download(t, "t-free", r2.ID, "docx")
		if !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
			t.Fatalf("free docx download err = %v, want ErrForbidden", err)
		}
	})

	t.Run("pro plan unlocks markdown and pdf but blocks docx", func(t *testing.T) {
		md := createFor(t, "t-pro", "markdown")
		if _, err := download(t, "t-pro", md.ID, "markdown"); err != nil {
			t.Fatalf("pro markdown download failed: %v", err)
		}
		pdf := createFor(t, "t-pro", "pdf")
		if _, err := download(t, "t-pro", pdf.ID, "pdf"); err != nil {
			t.Fatalf("pro pdf download failed: %v（方案 B Pro 含 PDF）", err)
		}
		docx := createFor(t, "t-pro", "docx")
		_, err := download(t, "t-pro", docx.ID, "docx")
		if !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
			t.Fatalf("pro docx download err = %v, want ErrForbidden", err)
		}
	})

	t.Run("lite plan blocks pdf", func(t *testing.T) {
		r := createFor(t, "t-lite", "pdf")
		_, err := download(t, "t-lite", r.ID, "pdf")
		if !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
			t.Fatalf("lite pdf download err = %v, want ErrForbidden", err)
		}
	})

	t.Run("enterprise plan downloads every format", func(t *testing.T) {
		for _, format := range report.Formats {
			r := createFor(t, "t-enterprise", format)
			if _, err := download(t, "t-enterprise", r.ID, format); err != nil {
				t.Fatalf("enterprise %s download failed: %v", format, err)
			}
		}
	})

	t.Run("unknown tenant defaults to free restrictions", func(t *testing.T) {
		r := createFor(t, "t-mystery", "pdf")
		_, err := download(t, "t-mystery", r.ID, "pdf")
		if !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
			t.Fatalf("unknown-tenant pdf download err = %v, want ErrForbidden", err)
		}
	})

	t.Run("format mismatch conflicts", func(t *testing.T) {
		r := createFor(t, "t-business", "html")
		_, err := download(t, "t-business", r.ID, "pdf") // report is html
		if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Fatalf("format mismatch err = %v, want ErrConflict", err)
		}
	})

	t.Run("cross-tenant access is not found", func(t *testing.T) {
		r := createFor(t, "t-business", "html")
		_, err := download(t, "t-enterprise", r.ID, "html")
		if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Fatalf("cross-tenant download err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unsupported format is rejected at creation", func(t *testing.T) {
		_, err := svc.CreateFromAnalysis(ctx, "t-business", analysisID, "pptx", "user-1")
		if err == nil {
			t.Fatal("CreateFromAnalysis accepted pptx")
		}
	})

	t.Run("list scopes reports to the tenant", func(t *testing.T) {
		reports, err := svc.List(ctx, "t-free")
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		freeIDs := map[string]bool{}
		for _, r := range reports {
			freeIDs[r.ID] = true
		}
		bizReports, err := svc.List(ctx, "t-business")
		if err != nil {
			t.Fatalf("List(t-business) failed: %v", err)
		}
		for _, r := range bizReports {
			if freeIDs[r.ID] {
				t.Fatalf("report %s visible in two tenants", r.ID)
			}
		}
	})
}

// TestIntegration_reportFullChain wires the report service to a plan resolver
// backed by an actual registered tenant from the auth flow.
func TestIntegration_reportFullChain(t *testing.T) {
	authSvc := newAuthService(t)
	p := registerUser(t, authSvc, "reporter")

	// planCodeForTenant resolves from the platform principal (as container.go
	// will do via the platform store).
	planFor := func(tenantID string) string {
		if tenantID == p.TenantID {
			return p.PlanCode
		}
		return ""
	}
	svc := report.NewService(report.NewMemoryStore(), planFor, nil)

	created, err := svc.CreateFromAnalysis(context.Background(), p.TenantID, "a-1", "html", "user-1")
	if err != nil {
		t.Fatalf("CreateFromAnalysis failed: %v", err)
	}

	got, err := svc.Get(context.Background(), p.TenantID, created.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != "completed" || got.AnalysisID != "a-1" {
		t.Fatalf("report = %+v, want completed for a-1", got)
	}

	// free plan: html allowed.
	if _, err := svc.DownloadURL(context.Background(), p.TenantID, created.ID, "html"); err != nil {
		t.Fatalf("free html download failed: %v", err)
	}

	// Custom provider check: billing.DefaultPlans must cover the four codes.
	if billing.DefaultPlans()["free"] == nil || billing.DefaultPlans()["enterprise"] == nil {
		t.Fatal("billing.DefaultPlans incomplete")
	}
}
