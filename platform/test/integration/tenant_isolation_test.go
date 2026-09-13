package integration

import (
	"context"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// TestIntegration_tenantIsolation proves one analysis service instance keeps
// every tenant's data hermetic: lists, gets, cancels and dashboard views are
// all scoped by tenant id — the same isolation the DB-per-tenant layer must
// guarantee once services are wired to real databases.
func TestIntegration_tenantIsolation(t *testing.T) {
	authSvc := newAuthService(t)
	tenantA := registerUser(t, authSvc, "tenantA")
	tenantB := registerUser(t, authSvc, "tenantB")
	analysisSvc := newAnalysisService()
	ctx := context.Background()

	// Two tenants share one auth store — the platform DB simulation.
	if tenantA.TenantID == tenantB.TenantID {
		t.Fatal("two registrations must produce distinct tenants")
	}
	// Registrations also provision distinct users.
	if tenantA.UserID == tenantB.UserID {
		t.Fatal("two registrations must produce distinct users")
	}

	a1 := createAnalysis(t, analysisSvc, tenantA.TenantID, "A 的雅阁分析")
	a2 := createAnalysis(t, analysisSvc, tenantA.TenantID, "A 的第二分析")
	b1 := createAnalysis(t, analysisSvc, tenantB.TenantID, "B 的竞品分析")

	t.Run("list is scoped per tenant", func(t *testing.T) {
		listA, err := analysisSvc.List(ctx, tenantA.TenantID)
		if err != nil {
			t.Fatalf("List(A) failed: %v", err)
		}
		if len(listA) != 2 {
			t.Fatalf("A list length = %d, want 2", len(listA))
		}
		listB, err := analysisSvc.List(ctx, tenantB.TenantID)
		if err != nil {
			t.Fatalf("List(B) failed: %v", err)
		}
		if len(listB) != 1 {
			t.Fatalf("B list length = %d, want 1", len(listB))
		}
		for _, a := range listA {
			if a.ID == b1.ID {
				t.Fatal("tenant A list contains tenant B's analysis")
			}
		}
		for _, b := range listB {
			if b.ID == a1.ID || b.ID == a2.ID {
				t.Fatal("tenant B list contains tenant A's analysis")
			}
		}
	})

	t.Run("cross-tenant get is not found", func(t *testing.T) {
		_, err := analysisSvc.Get(ctx, tenantA.TenantID, b1.ID)
		if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Fatalf("A.Get(B's analysis) err = %v, want ErrNotFound", err)
		}
		_, err = analysisSvc.Get(ctx, tenantB.TenantID, a1.ID)
		if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Fatalf("B.Get(A's analysis) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("cross-tenant cancel fails", func(t *testing.T) {
		err := analysisSvc.Cancel(ctx, tenantB.TenantID, a1.ID)
		if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Fatalf("B.Cancel(A's analysis) err = %v, want ErrNotFound", err)
		}
		// The owner's analysis must be untouched.
		got := mustGet(t, analysisSvc, tenantA.TenantID, a1.ID)
		if got.State != "queued" {
			t.Fatalf("A's analysis state = %s after B's cancel attempt, want queued", got.State)
		}
	})

	t.Run("tenant state machine advances independently", func(t *testing.T) {
		completeAnalysis(t, analysisSvc, tenantA.TenantID, a1.ID)
		gotA := mustGet(t, analysisSvc, tenantA.TenantID, a1.ID)
		if gotA.State != "completed" {
			t.Fatalf("A analysis = %s, want completed", gotA.State)
		}
		gotB := mustGet(t, analysisSvc, tenantB.TenantID, b1.ID)
		if gotB.State != "queued" {
			t.Fatalf("B analysis = %s, want untouched queued", gotB.State)
		}
	})

	t.Run("dashboard overview is isolated", func(t *testing.T) {
		dash := newDashboard(analysisSvc)
		ovA, err := dash.Overview(ctx, tenantA.TenantID)
		if err != nil {
			t.Fatalf("Overview(A) failed: %v", err)
		}
		// A: a1 completed + a2 queued → 1 active, 50% success.
		if ovA.TotalAnalyses != 2 || ovA.ActiveTasks != 1 {
			t.Fatalf("A overview = %+v, want 2 total / 1 active", ovA)
		}
		if ovA.SuccessRate != 50.0 {
			t.Fatalf("A success rate = %v, want 50", ovA.SuccessRate)
		}
		ovB, err := dash.Overview(ctx, tenantB.TenantID)
		if err != nil {
			t.Fatalf("Overview(B) failed: %v", err)
		}
		if ovB.TotalAnalyses != 1 || ovB.ActiveTasks != 1 || ovB.SuccessRate != 0 {
			t.Fatalf("B overview = %+v, want 1 total / 1 active / 0%% success", ovB)
		}
	})
}
