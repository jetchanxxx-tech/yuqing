package report

import (
	"context"
	"fmt"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/platform/billing"
)

// statusCompleted is the status reports are born in: generation is treated
// as instant in the MVP (the report engine fills the file asynchronously).
const statusCompleted = "completed"

// Service creates reports from analyses and controls format downloads per plan.
type Service struct {
	store Store
	// planCodeFor resolves a tenant ID to its plan code ("" = unknown).
	planCodeFor func(tenantID string) string
	// planProvider returns the plan definition for a plan code; nil falls
	// back to billing.DefaultPlans.
	planProvider func(planCode string) *billing.Plan
}

// NewService wires the report store with plan gating.
func NewService(store Store, planCodeFor func(tenantID string) string, planProvider func(planCode string) *billing.Plan) *Service {
	return &Service{store: store, planCodeFor: planCodeFor, planProvider: planProvider}
}

// CreateFromAnalysis creates a completed report for an analysis in a format
// from report.Formats. file_key follows reports/{id}.{format}.
func (s *Service) CreateFromAnalysis(ctx context.Context, tenantID, analysisID, format string) (*Report, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("report: tenant_id is required")
	}
	if analysisID == "" {
		return nil, fmt.Errorf("report: analysis_id is required")
	}
	if !supportedFormat(format) {
		return nil, fmt.Errorf("report: unsupported format %q (supported: %v)", format, Formats)
	}

	reportID := id.New()
	r := Report{
		ID:         reportID,
		AnalysisID: analysisID,
		Format:     format,
		Status:     statusCompleted,
		FileKey:    fmt.Sprintf("reports/%s.%s", reportID, format),
	}
	if err := s.store.Create(ctx, tenantID, r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Get returns one report scoped to the tenant.
func (s *Service) Get(ctx context.Context, tenantID, reportID string) (*Report, error) {
	r, err := s.store.Get(ctx, tenantID, reportID)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// List returns all reports of a tenant.
func (s *Service) List(ctx context.Context, tenantID string) ([]Report, error) {
	return s.store.List(ctx, tenantID, Filter{})
}

// DownloadURL checks the tenant's plan allows the report's format and returns
// the download URL for the file.
func (s *Service) DownloadURL(ctx context.Context, tenantID, reportID, format string) (string, error) {
	r, err := s.store.Get(ctx, tenantID, reportID)
	if err != nil {
		return "", err
	}
	if format != r.Format {
		return "", pkgerrors.Wrap(pkgerrors.ErrConflict,
			fmt.Sprintf("report %s is a %s report, not %s", r.ID, r.Format, format))
	}

	planCode := ""
	if s.planCodeFor != nil {
		planCode = s.planCodeFor(tenantID)
	}
	if planCode == "" {
		planCode = "free" // unknown tenant defaults to the most restrictive plan
	}

	plan := s.plan(planCode)
	if plan == nil || !plan.IsFeatureEnabled("reports:"+r.Format) {
		return "", pkgerrors.Wrap(pkgerrors.ErrForbidden,
			fmt.Sprintf("format %s is not included in plan %s", r.Format, planCode))
	}

	return fmt.Sprintf("/tenants/%s/reports/%s/download?format=%s", tenantID, r.ID, r.Format), nil
}

func (s *Service) plan(code string) *billing.Plan {
	if s.planProvider != nil {
		return s.planProvider(code)
	}
	return billing.DefaultPlans()[code]
}

func supportedFormat(format string) bool {
	for _, f := range Formats {
		if f == format {
			return true
		}
	}
	return false
}
