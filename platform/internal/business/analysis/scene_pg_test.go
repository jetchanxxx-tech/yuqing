package analysis

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestPGSceneSnapshotSurvivesConcurrencyRollbackAndReconstruction(t *testing.T) {
	pool := pgTestPool(t)
	store := newPGStore(pool)
	ctx := context.Background()
	tenant := newTestTenant()
	row := sampleAnalysis()
	row.ScenarioTemplateID = "quality_complaint"
	if err := store.put(ctx, tenant, row); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			errorsCh <- store.mutate(ctx, tenant, row.ID, func(a *AnalysisResult) error {
				a.Name = name
				return nil
			})
		}(name)
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	aborted := errors.New("abort scene write")
	if err := store.mutate(ctx, tenant, row.ID, func(a *AnalysisResult) error {
		a.ScenarioTemplateID = "crisis"
		return aborted
	}); !errors.Is(err, aborted) {
		t.Fatalf("expected aborted mutation: %v", err)
	}
	rebuilt := newPGStore(pool)
	got, err := rebuilt.get(ctx, tenant, row.ID)
	if err != nil || got.ScenarioTemplateID != "quality_complaint" {
		t.Fatalf("snapshot lost after reconstruction: %+v %v", got, err)
	}
	listed, err := rebuilt.list(ctx, tenant)
	if err != nil || len(listed) != 1 || listed[0].ScenarioTemplateID != "quality_complaint" {
		t.Fatalf("list snapshot lost: %+v %v", listed, err)
	}
}
