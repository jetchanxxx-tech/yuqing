package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// TestIntegration_concurrentReadsSameTenant runs 50 goroutines × 25 iterations
// of List + Get against one tenant and requires zero panics, zero unexpected
// errors and a stable store afterwards. The in-memory stores must stay
// race-free; run with `go test -race ./test/integration` to prove it.
func TestIntegration_concurrentReadsSameTenant(t *testing.T) {
	analysisSvc := newAnalysisService()
	tenantID := "t-concurrent-read"
	ctx := context.Background()

	const seeded = 10
	ids := make([]string, 0, seeded)
	for i := 0; i < seeded; i++ {
		ids = append(ids, createAnalysis(t, analysisSvc, tenantID, fmt.Sprintf("并发分析-%d", i)).ID)
	}

	const goroutines = 50
	const iterations = 25
	errCh := make(chan error, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				list, err := analysisSvc.List(ctx, tenantID)
				if err != nil {
					errCh <- fmt.Errorf("g%d list: %w", g, err)
					return
				}
				if len(list) != seeded {
					errCh <- fmt.Errorf("g%d list length = %d, want %d", g, len(list), seeded)
					return
				}
				id := ids[(g+i)%seeded]
				if _, err := analysisSvc.Get(ctx, tenantID, id); err != nil {
					errCh <- fmt.Errorf("g%d get %s: %w", g, id, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// Store still consistent after the stampede.
	final, err := analysisSvc.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("final List failed: %v", err)
	}
	if len(final) != seeded {
		t.Fatalf("final list length = %d, want %d", len(final), seeded)
	}
}

// TestIntegration_concurrentMixedOperations interleaves writers (Create +
// transition + occasional Cancel) with readers (List/Get) on the same tenant,
// mirroring dashboard polling against a busy tenant. Goroutines report errors
// through errCh — t.Fatal is only called on the test goroutine.
func TestIntegration_concurrentMixedOperations(t *testing.T) {
	analysisSvc := newAnalysisService()
	tenantID := "t-concurrent-mixed"
	ctx := context.Background()

	base := createAnalysis(t, analysisSvc, tenantID, "基础分析")

	const writers = 8
	const readsPerWriter = 30
	errCh := make(chan error, writers*2)
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		// Writer: create → advance → occasionally cancel.
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			created, err := analysisSvc.Create(ctx, analysisCreate(tenantID, fmt.Sprintf("写者-%d", w)))
			if err != nil {
				errCh <- fmt.Errorf("w%d create: %w", w, err)
				return
			}
			if err := analysisSvc.Transition(ctx, tenantID, created.ID, "acquiring_budget"); err != nil {
				errCh <- fmt.Errorf("w%d transition: %w", w, err)
				return
			}
			if w%3 == 0 {
				if err := analysisSvc.Cancel(ctx, tenantID, created.ID); err != nil {
					errCh <- fmt.Errorf("w%d cancel: %w", w, err)
				}
			}
		}(w)
		// Reader: hammer List + Get on the shared tenant.
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < readsPerWriter; i++ {
				if _, err := analysisSvc.Get(ctx, tenantID, base.ID); err != nil {
					errCh <- fmt.Errorf("w%d-r%d get: %w", w, i, err)
					return
				}
				if _, err := analysisSvc.List(ctx, tenantID); err != nil {
					errCh <- fmt.Errorf("w%d-r%d list: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	final, err := analysisSvc.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("final List failed: %v", err)
	}
	if len(final) != 1+writers {
		t.Fatalf("final count = %d, want %d", len(final), 1+writers)
	}
}

// TestIntegration_concurrentCancelSameAnalysis: concurrent cancels of the same
// analysis must yield exactly one success and N-1 conflicts — the store's
// mutate lock serializes the state transition.
func TestIntegration_concurrentCancelSameAnalysis(t *testing.T) {
	analysisSvc := newAnalysisService()
	tenantID := "t-concurrent-cancel"
	ctx := context.Background()

	created := createAnalysis(t, analysisSvc, tenantID, "群取消目标")

	const cancelers = 10
	var wg sync.WaitGroup
	results := make(chan error, cancelers)
	for i := 0; i < cancelers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- analysisSvc.Cancel(ctx, tenantID, created.ID)
		}()
	}
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case pkgerrors.Is(err, pkgerrors.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != cancelers-1 {
		t.Fatalf("successes = %d, conflicts = %d, want 1 and %d", successes, conflicts, cancelers-1)
	}

	got := mustGet(t, analysisSvc, tenantID, created.ID)
	if got.State != "canceled" {
		t.Fatalf("final state = %s, want canceled", got.State)
	}
}
