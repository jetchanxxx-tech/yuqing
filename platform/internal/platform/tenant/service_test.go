package tenant

import (
	"context"
	"testing"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

func newTestTenantService(t *testing.T) (*Service, *MemoryStore) {
	t.Helper()
	st := NewMemoryStore()
	return NewService(st), st
}

func seedTenant(t *testing.T, st *MemoryStore, name string) Tenant {
	t.Helper()
	tn := Tenant{ID: "tenant-" + name, Name: name, Slug: "slug-" + name, DBName: DBName("tenant-" + name), Status: StatusActive, PlanCode: "free"}
	if err := st.Create(context.Background(), tn); err != nil {
		t.Fatalf("seed tenant %q failed: %v", name, err)
	}
	return tn
}

func TestTenantService_Get_found(t *testing.T) {
	svc, st := newTestTenantService(t)
	want := seedTenant(t, st, "alpha")

	got, err := svc.Get(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != want.ID || got.Name != want.Name {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if got.DBName != DBName(want.ID) {
		t.Errorf("DBName = %q, want %q", got.DBName, DBName(want.ID))
	}
}

func TestTenantService_Get_notFound(t *testing.T) {
	svc, _ := newTestTenantService(t)
	_, err := svc.Get(context.Background(), "tenant-missing")
	if err == nil {
		t.Fatal("expected error for missing tenant")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestTenantService_List_empty(t *testing.T) {
	svc, _ := newTestTenantService(t)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(List) = %d, want 0", len(got))
	}
}

func TestTenantService_List_returnsCreatedTenants(t *testing.T) {
	svc, st := newTestTenantService(t)
	seedTenant(t, st, "one")
	seedTenant(t, st, "two")
	seedTenant(t, st, "three")

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(List) = %d, want 3", len(got))
	}
	// Stable creation order.
	wantNames := []string{"one", "two", "three"}
	for i, tn := range got {
		if tn.Name != wantNames[i] {
			t.Errorf("List[%d].Name = %q, want %q", i, tn.Name, wantNames[i])
		}
	}
}

func TestTenantService_Suspend_activeTenant(t *testing.T) {
	svc, st := newTestTenantService(t)
	tn := seedTenant(t, st, "active-co")

	if err := svc.Suspend(context.Background(), tn.ID); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}
	got, err := svc.Get(context.Background(), tn.ID)
	if err != nil {
		t.Fatalf("Get after suspend failed: %v", err)
	}
	if got.Status != StatusSuspended {
		t.Errorf("Status = %q, want suspended", got.Status)
	}
}

func TestTenantService_Suspend_nonActiveTenant(t *testing.T) {
	tests := []struct {
		name   string
		status Status
	}{
		{"already suspended", StatusSuspended},
		{"closed", StatusClosed},
		{"still provisioning", StatusProvisioning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, st := newTestTenantService(t)
			tn := Tenant{ID: "tenant-x", Name: "x", Slug: "x", DBName: DBName("tenant-x"), Status: tt.status, PlanCode: "free"}
			if err := st.Create(context.Background(), tn); err != nil {
				t.Fatal(err)
			}
			err := svc.Suspend(context.Background(), tn.ID)
			if err == nil {
				t.Fatalf("Suspend from %q: expected error", tt.status)
			}
			if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
				t.Errorf("error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestTenantService_Suspend_notFound(t *testing.T) {
	svc, _ := newTestTenantService(t)
	err := svc.Suspend(context.Background(), "tenant-missing")
	if err == nil {
		t.Fatal("expected error for missing tenant")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestTenantService_Resume_suspendedTenant(t *testing.T) {
	svc, st := newTestTenantService(t)
	tn := Tenant{ID: "tenant-y", Name: "y", Slug: "y", DBName: DBName("tenant-y"), Status: StatusSuspended, PlanCode: "free"}
	if err := st.Create(context.Background(), tn); err != nil {
		t.Fatal(err)
	}

	if err := svc.Resume(context.Background(), tn.ID); err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	got, err := svc.Get(context.Background(), tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
}

func TestTenantService_Resume_nonSuspendedTenant(t *testing.T) {
	svc, st := newTestTenantService(t)
	tn := seedTenant(t, st, "already-active")
	err := svc.Resume(context.Background(), tn.ID)
	if err == nil {
		t.Fatal("expected error when resuming an active tenant")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestTenantService_Resume_notFound(t *testing.T) {
	svc, _ := newTestTenantService(t)
	err := svc.Resume(context.Background(), "tenant-missing")
	if err == nil {
		t.Fatal("expected error for missing tenant")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestTenantMemoryStore_Create_conflictingID(t *testing.T) {
	st := NewMemoryStore()
	tn := Tenant{ID: "tenant-z", Name: "z", Slug: "z", DBName: DBName("tenant-z"), Status: StatusActive, PlanCode: "free"}
	if err := st.Create(context.Background(), tn); err != nil {
		t.Fatalf("first Create failed: %v", err)
	}
	err := st.Create(context.Background(), tn)
	if err == nil {
		t.Fatal("expected error for duplicate tenant ID")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestTenantMemoryStore_UpdateStatus_notFound(t *testing.T) {
	st := NewMemoryStore()
	err := st.UpdateStatus(context.Background(), "tenant-missing", StatusActive)
	if err == nil {
		t.Fatal("expected error updating a missing tenant")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
