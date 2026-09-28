package monitorplan

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestPGStoreDraftTenantRevisionContract(t *testing.T) {
	pool := pgtest.Pool(t, "monitorplan", pgtest.PlatformMigrations)
	store := NewPGStore(pool)
	ctx := context.Background()
	draft := Plan{TenantID: "tenant-a", OwnerID: "user-a", Name: "测试方案", TemplateID: "brand_daily", TemplateVersion: 1, AnalysisType: "brand", Inputs: map[string]string{"brand_name": "A"}, Config: map[string]Candidate{"keywords": {State: "proposed", Value: []string{"A"}, Source: "input"}}}
	created, err := store.Create(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.State != "draft" {
		t.Fatalf("created = %+v", created)
	}
	if _, err := store.Get(ctx, "tenant-b", created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross tenant Get = %v", err)
	}
	if list, err := store.List(ctx, "tenant-b", 20, 0); err != nil || len(list) != 0 {
		t.Errorf("cross tenant List = %+v, %v", list, err)
	}
	first, err := store.Get(ctx, "tenant-a", created.ID)
	if err != nil || first.Name != draft.Name {
		t.Fatalf("Get = %+v, %v", first, err)
	}
	first.Config["keywords"] = Candidate{State: "proposed", Value: []string{"edited"}, Source: "user"}
	updated, err := store.Update(ctx, "tenant-a", *first)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("Update = %+v, %v", updated, err)
	}
	if _, err := store.Update(ctx, "tenant-a", *first); !errors.Is(err, ErrConflict) {
		t.Errorf("stale revision = %v", err)
	}
	if _, err := store.Update(ctx, "tenant-b", *first); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross tenant update = %v", err)
	}
	got, err := store.Get(ctx, "tenant-a", created.ID)
	if err != nil || got.Config["keywords"].Source != "user" {
		t.Fatalf("edited config lost: %+v, %v", got, err)
	}
	for _, index := range []string{"idx_monitor_plans_tenant_id", "idx_monitor_plans_owner_created"} {
		var present bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND tablename='monitor_plans' AND indexname=$1)`, index).Scan(&present); err != nil || !present {
			t.Errorf("index %s missing: %v", index, err)
		}
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"A", "B"} {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			draft := *got
			draft.Name = name
			_, err := store.Update(ctx, "tenant-a", draft)
			results <- err
		}(name)
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Errorf("concurrent update: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("concurrent updates: success=%d conflict=%d", successes, conflicts)
	}
}

func TestPGStoreUpdateRejectsMismatchedTenantWithoutDB(t *testing.T) {
	store := NewPGStore(nil)
	draft := Plan{ID: "plan-a", TenantID: "tenant-a", Revision: 1}
	for _, tenantID := range []string{"tenant-b", ""} {
		if _, err := store.Update(context.Background(), tenantID, draft); !errors.Is(err, ErrNotFound) {
			t.Errorf("Update(%q, tenant-a) = %v, want NotFound", tenantID, err)
		}
	}
}
