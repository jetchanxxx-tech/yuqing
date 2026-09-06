// Package integration tests the real platform services end to end through
// their in-memory stores — no mocks beyond the documented fakes the platform
// itself provides (llm.FakeProvider, the memory queue). These tests live
// outside internal/ to exercise services the same way the future HTTP
// handlers will: one process, real service objects, real state transitions.
package integration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yuging/platform/internal/business/alert"
	"github.com/yuging/platform/internal/business/analysis"
	"github.com/yuging/platform/internal/business/dashboard"
	"github.com/yuging/platform/internal/pkg/queue"
	"github.com/yuging/platform/internal/platform/auth"
	"github.com/yuging/platform/internal/platform/usage"
)

const integrationSecret = "integration-secret-min-32-chars!!!"
const analysisTopic = "analysis.tasks" // queue topic analysis.Service publishes on

var emailSeq atomic.Int64

// newAuthService returns an auth service over a fresh in-memory platform store.
func newAuthService(t *testing.T) *auth.Service {
	t.Helper()
	return auth.NewService(auth.NewMemoryStore(), integrationSecret, "15m", "720h")
}

// registerUser performs a full self-registration and returns the principal.
// Email uniqueness is guaranteed by a package-level sequence.
func registerUser(t *testing.T, svc *auth.Service, prefix string) *auth.Principal {
	t.Helper()
	n := emailSeq.Add(1)
	p, pair, err := svc.Register(context.Background(),
		fmt.Sprintf("%s-%d@example.com", prefix, n), "password-123456", prefix)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if p == nil || pair == nil || pair.AccessToken == "" {
		t.Fatal("Register returned nil principal or empty token pair")
	}
	if p.PlanCode != "free" || p.TenantStatus != "active" {
		t.Fatalf("fresh tenant = plan %q status %q, want free/active", p.PlanCode, p.TenantStatus)
	}
	return p
}

// mustLogin verifies the credential round trip (register → login) and returns
// the login principal.
func mustLogin(t *testing.T, svc *auth.Service, email, password string) *auth.Principal {
	t.Helper()
	p, _, err := svc.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login(%s) failed: %v", email, err)
	}
	return p
}

// newAnalysisService returns an analysis service over the memory queue.
func newAnalysisService() *analysis.Service {
	return analysis.NewService(queue.NewMemory(), 4)
}

// analysisCreate builds the request payload for a new sentiment analysis.
func analysisCreate(tenantID, name string) analysis.CreateAnalysisRequest {
	return analysis.CreateAnalysisRequest{
		TenantID:     tenantID,
		UserID:       "u_test",
		Name:         name,
		AnalysisType: "sentiment",
		Keywords:     []string{"雅阁后排"},
		Sources:      []string{"weibo", "wechat", "news"},
	}
}

// createAnalysis is a typed convenience wrapper around Service.Create.
func createAnalysis(t *testing.T, svc *analysis.Service, tenantID, name string) *analysis.AnalysisResult {
	t.Helper()
	a, err := svc.Create(context.Background(), analysisCreate(tenantID, name))
	if err != nil {
		t.Fatalf("analysis.Create(%q) failed: %v", name, err)
	}
	return a
}

// completeAnalysis pushes one analysis through a full happy-path run:
// queued → acquiring_budget → fetching → analyzing → generating_report → completed.
func completeAnalysis(t *testing.T, svc *analysis.Service, tenantID, id string) {
	t.Helper()
	for _, to := range []string{
		string(analysis.StateAcquiringBudget),
		string(analysis.StateFetching),
		string(analysis.StateAnalyzing),
		string(analysis.StateGeneratingReport),
		string(analysis.StateCompleted),
	} {
		if err := svc.Transition(context.Background(), tenantID, id, to); err != nil {
			t.Fatalf("Transition → %s failed: %v", to, err)
		}
	}
}

// mustGet returns one analysis or fails the test.
func mustGet(t *testing.T, svc *analysis.Service, tenantID, id string) *analysis.AnalysisResult {
	t.Helper()
	a, err := svc.Get(context.Background(), tenantID, id)
	if err != nil {
		t.Fatalf("Get(%s) failed: %v", id, err)
	}
	return a
}

// newDashboard builds a dashboard service over the given analysis service.
func newDashboard(analysisSvc *analysis.Service) *dashboard.Service {
	return dashboard.NewService(analysisSvc, nil)
}

// mockEmailSender is the alert package's EmailSender double: it records every
// send so tests can assert recipient, count and content.
type mockEmailSender struct {
	mu    sync.Mutex
	calls []alertCall
}

type alertCall struct {
	to, subject, body string
}

func (m *mockEmailSender) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, alertCall{to: to, subject: subject, body: body})
	return nil
}

func (m *mockEmailSender) snapshot() []alertCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]alertCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// newAlertService wires the real alert service with a recording sender.
func newAlertService(t *testing.T) (*alert.Service, *mockEmailSender) {
	t.Helper()
	sender := &mockEmailSender{}
	return alert.NewService(alert.NewMemoryStore(), sender), sender
}

// newUsageMeter returns the real in-memory usage meter (llm.Meter impl).
func newUsageMeter() *usage.Meter { return usage.NewMeter() }
