package analysis

import (
	"context"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/queue"
)

func newTestAnalysisService(t *testing.T) (*Service, queue.Queue) {
	t.Helper()
	q := queue.NewMemory()
	svc := NewService(q, 4)
	t.Cleanup(func() { _ = q.Close() })
	return svc, q
}

// subscribeTasks captures every published analysis task body.
func subscribeTasks(t *testing.T, q queue.Queue) <-chan string {
	t.Helper()
	ch := make(chan string, 32)
	if err := q.Subscribe(context.Background(), topicAnalysisTasks,
		func(_ context.Context, msg queue.Message) error {
			ch <- string(msg.Body)
			return nil
		}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	return ch
}

func nextTask(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case body := <-ch:
		return body
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for queued task")
		return ""
	}
}

func createAnalysis(t *testing.T, svc *Service, tenantID string) *AnalysisResult {
	t.Helper()
	got, err := svc.Create(context.Background(), CreateAnalysisRequest{
		TenantID:     tenantID,
		UserID:       "user-1",
		Name:         "雅阁后排舆情",
		AnalysisType: "brand",
		Keywords:     []string{"雅阁后排"},
		Sources:      []string{"weibo", "news"},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	return got
}

func advanceTo(t *testing.T, svc *Service, tenantID, id string, states ...string) {
	t.Helper()
	for _, to := range states {
		if err := svc.Transition(context.Background(), tenantID, id, to); err != nil {
			t.Fatalf("Transition to %q failed: %v", to, err)
		}
	}
}

func TestServiceCreate_validationErrors(t *testing.T) {
	svc, _ := newTestAnalysisService(t)

	if _, err := svc.Create(context.Background(), CreateAnalysisRequest{TenantID: "tenant-1", Name: ""}); err == nil {
		t.Error("Create with empty name: expected error")
	}
	if _, err := svc.Create(context.Background(), CreateAnalysisRequest{TenantID: "", Name: "x"}); err == nil {
		t.Error("Create with empty tenant_id: expected error")
	}
}

func TestServiceCreate_persistsQueuedState(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	want := createAnalysis(t, svc, "tenant-1")

	if want.State != StateQueued {
		t.Errorf("new analysis State = %q, want queued", want.State)
	}
	if want.ID == "" || len(want.ID) != 26 {
		t.Errorf("ID = %q, want a 26-char ULID", want.ID)
	}

	got, err := svc.Get(context.Background(), "tenant-1", want.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != want.ID || got.Name != want.Name {
		t.Errorf("Get returned %+v, want %+v", got, want)
	}
	if got.State != StateQueued {
		t.Errorf("stored State = %q, want queued", got.State)
	}
}

func TestServiceCreate_publishesTaskToQueue(t *testing.T) {
	svc, q := newTestAnalysisService(t)
	ch := subscribeTasks(t, q)

	got := createAnalysis(t, svc, "tenant-1")
	body := nextTask(t, ch)

	// 载荷为 JSON（含 analysis_id + tenant_id），不再是裸 ID：
	// 管线需要 tenantID 才能定位任务（store 按租户分桶）。
	msg, err := DecodeTaskMessage([]byte(body))
	if err != nil {
		t.Fatalf("published payload not decodable: %v (payload=%q)", err, body)
	}
	if msg.AnalysisID != got.ID {
		t.Errorf("analysis_id = %q, want %q", msg.AnalysisID, got.ID)
	}
	if msg.TenantID != "tenant-1" {
		t.Errorf("tenant_id = %q, want tenant-1", msg.TenantID)
	}
}

func TestServiceGet_notFound(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	_, err := svc.Get(context.Background(), "tenant-1", "01MISSINGNOTFOUND000000000")
	if err == nil {
		t.Fatal("expected error for missing analysis")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceGet_isolatedByTenant(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	a := createAnalysis(t, svc, "tenant-a")

	if _, err := svc.Get(context.Background(), "tenant-b", a.ID); err == nil {
		t.Fatal("expected ErrNotFound when reading another tenant's analysis")
	} else if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceList_groupsByTenantAndSorts(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	createAnalysis(t, svc, "tenant-a")
	createAnalysis(t, svc, "tenant-a")
	createAnalysis(t, svc, "tenant-b")

	gotA, err := svc.List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(gotA) != 2 {
		t.Errorf("tenant-a list len = %d, want 2", len(gotA))
	}
	// Stable ordering: earlier CreatedAt first, ID as tiebreak.
	if gotA[0].CreatedAt.After(gotA[1].CreatedAt) {
		t.Error("list must be ordered by CreatedAt ascending")
	}

	gotB, err := svc.List(context.Background(), "tenant-b")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(gotB) != 1 {
		t.Errorf("tenant-b list len = %d, want 1", len(gotB))
	}

	gotEmpty, err := svc.List(context.Background(), "tenant-empty")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(gotEmpty) != 0 {
		t.Errorf("empty tenant list len = %d, want 0", len(gotEmpty))
	}
}

func TestServiceCancel_activeAnalysis(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	a := createAnalysis(t, svc, "tenant-1")

	if err := svc.Cancel(context.Background(), "tenant-1", a.ID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	got, err := svc.Get(context.Background(), "tenant-1", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCanceled {
		t.Errorf("State = %q, want canceled", got.State)
	}
	if got.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set when an analysis is canceled")
	}
}

func TestServiceCancel_terminalAnalysisReturnsConflict(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	a := createAnalysis(t, svc, "tenant-1")

	if err := svc.Cancel(context.Background(), "tenant-1", a.ID); err != nil {
		t.Fatalf("first Cancel failed: %v", err)
	}
	err := svc.Cancel(context.Background(), "tenant-1", a.ID)
	if err == nil {
		t.Fatal("expected error when canceling a terminal (canceled) analysis")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestServiceCancel_notFound(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	err := svc.Cancel(context.Background(), "tenant-1", "01MISSINGNOTFOUND000000000")
	if err == nil {
		t.Fatal("expected error for missing analysis")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceTransition_forwardPipeline(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	a := createAnalysis(t, svc, "tenant-1")

	advanceTo(t, svc, "tenant-1", a.ID,
		"acquiring_budget", "fetching", "analyzing", "generating_report", "completed")

	got, err := svc.Get(context.Background(), "tenant-1", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateCompleted {
		t.Errorf("State = %q, want completed", got.State)
	}
	if got.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set when the pipeline completes")
	}

	// Terminal states can never transition again.
	if err := svc.Transition(context.Background(), "tenant-1", a.ID, "analyzing"); err == nil {
		t.Error("expected error transitioning out of completed")
	} else if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}
}

func TestServiceTransition_invalidMoveAndState(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	a := createAnalysis(t, svc, "tenant-1")

	// Skipping a machine state is illegal.
	err := svc.Transition(context.Background(), "tenant-1", a.ID, "completed")
	if err == nil {
		t.Fatal("expected error skipping from queued straight to completed")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}

	// Unknown target state is rejected.
	if err := svc.Transition(context.Background(), "tenant-1", a.ID, "banana"); err == nil {
		t.Error("expected error for unknown target state")
	}

	// Missing analysis.
	err = svc.Transition(context.Background(), "tenant-1", "01MISSINGNOTFOUND000000000", "fetching")
	if err == nil {
		t.Fatal("expected error for missing analysis")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceRerun_fromTerminalStates(t *testing.T) {
	tests := []struct {
		name  string
		toEnd func(t *testing.T, svc *Service, tenantID, id string)
	}{
		{"failed", func(t *testing.T, svc *Service, tenantID, id string) {
			advanceTo(t, svc, tenantID, id, "failed")
		}},
		{"canceled", func(t *testing.T, svc *Service, tenantID, id string) {
			if err := svc.Cancel(context.Background(), tenantID, id); err != nil {
				t.Fatalf("Cancel failed: %v", err)
			}
		}},
		{"completed", func(t *testing.T, svc *Service, tenantID, id string) {
			advanceTo(t, svc, tenantID, id, "acquiring_budget", "fetching", "analyzing", "generating_report", "completed")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, q := newTestAnalysisService(t)
			ch := subscribeTasks(t, q)
			a := createAnalysis(t, svc, "tenant-1")
			nextTask(t, ch) // consume the initial enqueue

			tt.toEnd(t, svc, "tenant-1", a.ID)

			if err := svc.Rerun(context.Background(), "tenant-1", a.ID); err != nil {
				t.Fatalf("Rerun failed: %v", err)
			}
			got, err := svc.Get(context.Background(), "tenant-1", a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StateQueued {
				t.Errorf("State = %q, want queued after rerun", got.State)
			}
			if !got.FinishedAt.IsZero() || !got.StartedAt.IsZero() {
				t.Error("rerun should clear StartedAt/FinishedAt")
			}
			// 载荷为 JSON（analysis_id + tenant_id）
			body := nextTask(t, ch)
			msg, err := DecodeTaskMessage([]byte(body))
			if err != nil {
				t.Fatalf("rerun payload not decodable: %v (payload=%q)", err, body)
			}
			if msg.AnalysisID != a.ID {
				t.Errorf("rerun analysis_id = %q, want %q", msg.AnalysisID, a.ID)
			}
			if msg.TenantID != "tenant-1" {
				t.Errorf("rerun tenant_id = %q, want tenant-1", msg.TenantID)
			}
		})
	}
}

func TestServiceRerun_activeAnalysisReturnsConflict(t *testing.T) {
	svc, _ := newTestAnalysisService(t)
	a := createAnalysis(t, svc, "tenant-1")

	err := svc.Rerun(context.Background(), "tenant-1", a.ID)
	if err == nil {
		t.Fatal("expected error rerunning an active (queued) analysis")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Errorf("error = %v, want ErrConflict", err)
	}

	err = svc.Rerun(context.Background(), "tenant-1", "01MISSINGNOTFOUND000000000")
	if err == nil {
		t.Fatal("expected error rerunning a missing analysis")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}
