package integration

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/business/analysis"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/queue"
)

// TestIntegration_registerLoginTenantFlow covers the platform provisioning
// chain: self-register provisions user + tenant + membership + free-plan
// quota, and the same credentials log back in with a working token.
func TestIntegration_registerLoginTenantFlow(t *testing.T) {
	svc := newAuthService(t)

	t.Run("register provisions user tenant and admin role", func(t *testing.T) {
		p := registerUser(t, svc, "alice")
		if p.UserID == "" || p.TenantID == "" {
			t.Fatal("principal missing user/tenant ids")
		}
		if len(p.Roles) != 1 || p.Roles[0] != "tenant_admin" {
			t.Fatalf("roles = %v, want [tenant_admin]", p.Roles)
		}
		if p.TenantStatus != "active" || p.PlanCode != "free" {
			t.Fatalf("principal = %+v, want active free tenant", p)
		}
	})

	t.Run("same email cannot register twice", func(t *testing.T) {
		p := registerUser(t, svc, "bob")
		_, _, err := svc.Register(context.Background(), p.Email, "password-123456", "bob-clone")
		if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Fatalf("duplicate register err = %v, want ErrConflict", err)
		}
	})

	t.Run("login round trips and normalizes email", func(t *testing.T) {
		p := registerUser(t, svc, "carol")
		up := mustLogin(t, svc, p.Email, "password-123456")
		if up.TenantID != p.TenantID || up.UserID != p.UserID {
			t.Fatalf("login principal = %+v, want same user/tenant as registered", up)
		}
	})

	t.Run("wrong password is unauthorized", func(t *testing.T) {
		p := registerUser(t, svc, "dave")
		_, _, err := svc.Login(context.Background(), p.Email, "wrong-password")
		if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			t.Fatalf("bad-password err = %v, want ErrUnauthorized", err)
		}
	})
}

// TestIntegration_analysisLifecycleFullChain drives one analysis through the
// whole state machine exactly as a worker would (create → queue message →
// five transitions → terminal), then asserts the dashboard aggregates reflect
// the completed run.
func TestIntegration_analysisLifecycleFullChain(t *testing.T) {
	q := queue.NewMemory()
	analysisSvc := analysis.NewService(q, 4)
	tid := registerUser(t, newAuthService(t), "chainA").TenantID

	// Subscribe like a worker BEFORE creating, so the published task is seen.
	taskCh := make(chan string, 1)
	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := q.Subscribe(subCtx, analysisTopic, func(_ context.Context, msg queue.Message) error {
		taskCh <- string(msg.Body)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	created := createAnalysis(t, analysisSvc, tid, "雅阁后排专项")
	if created.State != analysis.StateQueued {
		t.Fatalf("new analysis state = %s, want queued", created.State)
	}

	t.Run("create published a task to the queue", func(t *testing.T) {
		select {
		case payload := <-taskCh:
			// 载荷是 JSON（analysis_id + tenant_id）；管线需 tenantID 定位任务
			msg, err := analysis.DecodeTaskMessage([]byte(payload))
			if err != nil {
				t.Fatalf("task payload not decodable: %v (payload=%q)", err, payload)
			}
			if msg.AnalysisID != created.ID {
				t.Fatalf("task analysis_id = %q, want %q", msg.AnalysisID, created.ID)
			}
			if msg.TenantID != tid {
				t.Fatalf("task tenant_id = %q, want %q", msg.TenantID, tid)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no analysis task received on queue")
		}
	})

	// Worker advances the machine: queued → acquiring_budget → fetching →
	// analyzing → generating_report → completed.
	completeAnalysis(t, analysisSvc, tid, created.ID)

	t.Run("terminal state is durable and timestamped", func(t *testing.T) {
		got := mustGet(t, analysisSvc, tid, created.ID)
		if got.State != analysis.StateCompleted {
			t.Fatalf("final state = %s, want completed", got.State)
		}
		if got.FinishedAt.IsZero() {
			t.Error("FinishedAt not set on completion")
		}
	})

	t.Run("terminal state rejects further transitions", func(t *testing.T) {
		err := analysisSvc.Transition(context.Background(), tid, created.ID,
			string(analysis.StateFailed))
		if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
			t.Fatalf("completed→failed err = %v, want ErrConflict", err)
		}
	})

	t.Run("rerun from terminal state requeues a fresh task", func(t *testing.T) {
		if err := analysisSvc.Rerun(context.Background(), tid, created.ID); err != nil {
			t.Fatalf("Rerun failed: %v", err)
		}
		got := mustGet(t, analysisSvc, tid, created.ID)
		if got.State != analysis.StateQueued || !got.FinishedAt.IsZero() {
			t.Fatalf("state after rerun = %+v, want queued without FinishedAt", got)
		}
		select {
		case payload := <-taskCh:
			msg, err := analysis.DecodeTaskMessage([]byte(payload))
			if err != nil {
				t.Fatalf("rerun payload not decodable: %v (payload=%q)", err, payload)
			}
			if msg.AnalysisID != created.ID {
				t.Fatalf("rerun analysis_id = %q, want %q", msg.AnalysisID, created.ID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no rerun task received on queue")
		}
	})

	// Finish the rerun so dashboard assertions are deterministic.
	completeAnalysis(t, analysisSvc, tid, created.ID)
	dash := newDashboard(analysisSvc)

	t.Run("dashboard overview reflects one completed run", func(t *testing.T) {
		ov, err := dash.Overview(context.Background(), tid)
		if err != nil {
			t.Fatalf("Overview failed: %v", err)
		}
		if ov.TotalAnalyses != 1 || ov.ActiveTasks != 0 || ov.SuccessRate != 100.0 {
			t.Fatalf("overview = %+v, want 1 total / 0 active / 100%% success", ov)
		}
	})

	t.Run("dashboard trend shows the run date", func(t *testing.T) {
		tr, err := dash.Trend(context.Background(), tid)
		if err != nil {
			t.Fatalf("Trend failed: %v", err)
		}
		if len(tr.Dates) != 1 || len(tr.Counts) != 1 || tr.Counts[0] != 1 {
			t.Fatalf("trend = %v / %v, want one day with 1 run", tr.Dates, tr.Counts)
		}
	})

	t.Run("dashboard sources and topics are served", func(t *testing.T) {
		src, err := dash.Sources(context.Background(), tid)
		if err != nil || len(src.Sources) != 4 {
			t.Fatalf("Sources err = %v, rows = %d, want 4", err, len(src.Sources))
		}
		topics, err := dash.Topics(context.Background(), tid)
		if err != nil || len(topics) != 4 {
			t.Fatalf("Topics err = %v, count = %d, want 4", err, len(topics))
		}
	})
}

// TestIntegration_analysisCancelFlow: canceling an active analysis is
// observable through Get/List, and a terminal analysis cannot be re-canceled.
func TestIntegration_analysisCancelFlow(t *testing.T) {
	analysisSvc := newAnalysisService()
	tid := registerUser(t, newAuthService(t), "cancelA").TenantID

	created := createAnalysis(t, analysisSvc, tid, "将被取消的分析")
	if err := analysisSvc.Cancel(context.Background(), tid, created.ID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if got := mustGet(t, analysisSvc, tid, created.ID); got.State != analysis.StateCanceled {
		t.Fatalf("state = %s, want canceled", got.State)
	}

	list, err := analysisSvc.List(context.Background(), tid)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 || list[0].State != analysis.StateCanceled {
		t.Fatalf("list = %+v, want one canceled analysis", list)
	}

	err = analysisSvc.Cancel(context.Background(), tid, created.ID)
	if !pkgerrors.Is(err, pkgerrors.ErrConflict) {
		t.Fatalf("double cancel err = %v, want ErrConflict", err)
	}
}
