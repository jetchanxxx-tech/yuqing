package analysis

import (
	"context"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

func TestResilientPGConcurrentSameTopicRequestMergesOneRunChargeAndQueue(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	f := newK4BillingPGFixture(t, 8)
	start := make(chan struct{})
	results := make(chan *AnalysisResult, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "same research"))
			results <- a
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	identity := ""
	for a := range results {
		if identity == "" {
			identity = a.ID
		}
		if a.ID != identity {
			t.Fatalf("duplicate research admitted new ID %s/%s", identity, a.ID)
		}
	}
	f.assertBalance(t, 7)
	f.assertEffects(t, 1, 1, 1, 0)
}

func TestResilientPGDailyBudgetStopsDistinctRefundedBriefLoop(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	t.Setenv("YUQING_RESILIENT_DAILY_RUN_LIMIT", "1")
	f := newK4BillingPGFixture(t, 1)
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "first bounded research"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPipeline(f.svc, &fakeFetcher{}, time.Minute, nil)
	if err = p.Handle(f.ctx, TaskMessage{TenantID: f.tenantID, AnalysisID: a.ID, RunID: a.CurrentRunID}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Create(f.ctx, f.request(f.ordinaryID, "different research"))
	if !pkgerrors.Is(err, pkgerrors.ErrQuotaExceeded) {
		t.Fatalf("refunded brief bypassed daily budget: %v", err)
	}
	f.assertBalance(t, 1)
	f.assertEffects(t, 1, 1, 1, 1)
}

func TestResilientPGResumePreviewDescribesRetainedSnapshotAndNormalCharge(t *testing.T) {
	t.Setenv("YUQING_RESILIENT_DELIVERY", "1")
	f := newK4BillingPGFixture(t, 1)
	a, err := f.svc.Create(f.ctx, f.request(f.ordinaryID, "resume preview"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPipeline(f.svc, &fakeFetcher{docs: []Document{{ID: "material", Content: "retained", SourceType: "weibo"}}}, time.Minute, nil)
	if err = p.Handle(f.ctx, TaskMessage{TenantID: f.tenantID, AnalysisID: a.ID, RunID: a.CurrentRunID}); err != nil {
		t.Fatal(err)
	}
	// Test through a dynamic interface so the RED baseline compiles before the
	// preflight method exists, and fails for its missing externally visible API.
	previewer, ok := any(f.svc).(interface {
		ResumePreviewJSON(context.Context, string, string, billingpolicy.Actor) (map[string]any, error)
	})
	if !ok {
		t.Fatal("resume preflight is unavailable")
	}
	preview, err := previewer.ResumePreviewJSON(f.ctx, f.tenantID, a.ID, billingpolicy.Actor{UserID: f.ordinaryID})
	if err != nil {
		t.Fatal(err)
	}
	if preview["resume_from_run_id"] != a.CurrentRunID || preview["retained_material_count"] != 1 || preview["report_credit_reservation"] != 1 || preview["supplier_calls"] != nil {
		t.Fatalf("resume preview overstates reuse/cost: %+v", preview)
	}
}
