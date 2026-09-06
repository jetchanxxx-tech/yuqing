package alert

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

// mockEmailSender records sends for assertions.
type mockEmailSender struct {
	mu    sync.Mutex
	calls []emailCall
	err   error // when set, every Send fails
}

type emailCall struct {
	to      string
	subject string
	body    string
}

func (m *mockEmailSender) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, emailCall{to: to, subject: subject, body: body})
	return m.err
}

func (m *mockEmailSender) snapshot() []emailCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]emailCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func newTestAlertService(t *testing.T) (*Service, *MemoryStore, *mockEmailSender) {
	t.Helper()
	st := NewMemoryStore()
	sender := &mockEmailSender{}
	return NewService(st, sender), st, sender
}

func mustCreate(t *testing.T, svc *Service, tenantID, name, ruleJSON string) *Alert {
	t.Helper()
	a, err := svc.Create(context.Background(), tenantID, name, ruleJSON)
	if err != nil {
		t.Fatalf("Create(%s) failed: %v", name, err)
	}
	return a
}

func ruleJSON(threshold float64, email string) string {
	return fmt.Sprintf(`{"threshold": %v, "email": %q}`, threshold, email)
}

func TestServiceCreate_validRule(t *testing.T) {
	svc, st, _ := newTestAlertService(t)
	a := mustCreate(t, svc, "tenant-1", "负面占比过高", ruleJSON(0.3, "ops@example.com"))

	if len(a.ID) != 26 {
		t.Errorf("ID = %q, want 26-char ULID", a.ID)
	}
	if a.Name != "负面占比过高" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.Threshold != 0.3 {
		t.Errorf("Threshold = %v, want 0.3", a.Threshold)
	}
	if a.RecipientEmail != "ops@example.com" {
		t.Errorf("RecipientEmail = %q", a.RecipientEmail)
	}
	if a.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	got, err := st.Get(context.Background(), "tenant-1", a.ID)
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}
	if got.Threshold != 0.3 {
		t.Errorf("stored threshold = %v, want 0.3", got.Threshold)
	}
}

func TestServiceCreate_invalidRuleJSON(t *testing.T) {
	svc, st, _ := newTestAlertService(t)

	for _, bad := range []string{"", "{", "[]", "not json at all"} {
		if _, err := svc.Create(context.Background(), "tenant-1", "rule", bad); err == nil {
			t.Errorf("Create with rule %q: expected error", bad)
		}
	}
	if len(st.alerts) != 0 {
		t.Error("failed creates must not persist alerts")
	}
}

func TestServiceCreate_invalidRuleValues(t *testing.T) {
	svc, _, _ := newTestAlertService(t)

	tests := []struct {
		name     string
		ruleJSON string
	}{
		{"zero threshold", `{"threshold": 0.0, "email": "a@b.com"}`},
		{"negative threshold", `{"threshold": -0.2, "email": "a@b.com"}`},
		{"threshold above 1", `{"threshold": 1.2, "email": "a@b.com"}`},
		{"missing email", `{"threshold": 0.5}`},
		{"empty email", `{"threshold": 0.5, "email": ""}`},
		{"malformed email", `{"threshold": 0.5, "email": "not-an-email"}`},
	}
	for _, tt := range tests {
		if _, err := svc.Create(context.Background(), "tenant-1", tt.name, tt.ruleJSON); err == nil {
			t.Errorf("Create(%s): expected error", tt.name)
		}
	}
}

func TestServiceCreate_missingArguments(t *testing.T) {
	svc, _, _ := newTestAlertService(t)

	if _, err := svc.Create(context.Background(), "", "name", ruleJSON(0.3, "ops@example.com")); err == nil {
		t.Error("empty tenant_id: expected error")
	}
	if _, err := svc.Create(context.Background(), "tenant-1", "", ruleJSON(0.3, "ops@example.com")); err == nil {
		t.Error("empty name: expected error")
	}
}

func TestServiceList_emptyOrderedAndIsolated(t *testing.T) {
	svc, _, _ := newTestAlertService(t)

	empty, err := svc.List(context.Background(), "tenant-empty")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty tenant list len = %d, want 0", len(empty))
	}

	first := mustCreate(t, svc, "tenant-1", "first", ruleJSON(0.3, "a@b.com"))
	second := mustCreate(t, svc, "tenant-1", "second", ruleJSON(0.5, "a@b.com"))
	mustCreate(t, svc, "tenant-2", "other", ruleJSON(0.3, "a@b.com"))

	got, err := svc.List(context.Background(), "tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("tenant-1 list len = %d, want 2", len(got))
	}
	if got[0].ID != first.ID || got[1].ID != second.ID {
		t.Errorf("list order = [%s %s], want creation order", got[0].ID, got[1].ID)
	}
}

func TestMemoryStore_Get_notFoundAndTrigger(t *testing.T) {
	st := NewMemoryStore()
	if _, err := st.Get(context.Background(), "tenant-1", "01MISSINGNOTFOUND000000000"); err == nil {
		t.Fatal("expected error for missing alert")
	} else if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
	if err := st.UpdateTrigger(context.Background(), "tenant-1", "01MISSINGNOTFOUND000000000", time.Now()); err == nil {
		t.Fatal("expected error updating a missing alert")
	} else if !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestServiceCheck_belowThresholdNoTrigger(t *testing.T) {
	svc, st, sender := newTestAlertService(t)
	a := mustCreate(t, svc, "tenant-1", "strict", ruleJSON(0.8, "ops@example.com"))

	triggered, err := svc.Check(context.Background(), "tenant-1", 0.3)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(triggered) != 0 {
		t.Errorf("triggered = %+v, want none", triggered)
	}
	if len(sender.snapshot()) != 0 {
		t.Error("no email should be sent below threshold")
	}
	got, _ := st.Get(context.Background(), "tenant-1", a.ID)
	if !got.LastTriggeredAt.IsZero() {
		t.Error("LastTriggeredAt should stay zero below threshold")
	}
}

func TestServiceCheck_atThresholdTriggersAndSends(t *testing.T) {
	svc, st, sender := newTestAlertService(t)
	a := mustCreate(t, svc, "tenant-1", "负面占比过高", ruleJSON(0.3, "ops@example.com"))

	triggered, err := svc.Check(context.Background(), "tenant-1", 0.3)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(triggered) != 1 || triggered[0].ID != a.ID {
		t.Fatalf("triggered = %+v, want alert %s", triggered, a.ID)
	}

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("email calls = %d, want 1", len(calls))
	}
	if calls[0].to != "ops@example.com" {
		t.Errorf("email to = %q, want ops@example.com", calls[0].to)
	}
	if calls[0].subject == "" || calls[0].body == "" {
		t.Error("email subject and body must be non-empty")
	}

	got, _ := st.Get(context.Background(), "tenant-1", a.ID)
	if got.LastTriggeredAt.IsZero() {
		t.Error("LastTriggeredAt should be set after a trigger")
	}
}

func TestServiceCheck_triggersOnlyMatchingAlerts(t *testing.T) {
	svc, _, sender := newTestAlertService(t)
	lo := mustCreate(t, svc, "tenant-1", "sensitive", ruleJSON(0.2, "a@b.com"))
	mustCreate(t, svc, "tenant-1", "calm", ruleJSON(0.8, "a@b.com"))

	triggered, err := svc.Check(context.Background(), "tenant-1", 0.5)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(triggered) != 1 || triggered[0].ID != lo.ID {
		t.Errorf("triggered = %+v, want only the 0.2-threshold alert", triggered)
	}
	if calls := sender.snapshot(); len(calls) != 1 {
		t.Errorf("email calls = %d, want 1", len(calls))
	}
}

func TestServiceCheck_multipleAlertsAllBelowNegPct(t *testing.T) {
	svc, _, sender := newTestAlertService(t)
	mustCreate(t, svc, "tenant-1", "a", ruleJSON(0.2, "a@b.com"))
	mustCreate(t, svc, "tenant-1", "b", ruleJSON(0.3, "b@b.com"))
	mustCreate(t, svc, "tenant-1", "c", ruleJSON(0.7, "c@b.com"))

	triggered, err := svc.Check(context.Background(), "tenant-1", 0.8)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(triggered) != 3 {
		t.Errorf("triggered = %d, want all 3", len(triggered))
	}
	if calls := sender.snapshot(); len(calls) != 3 {
		t.Errorf("email calls = %d, want 3", len(calls))
	}
}

func TestServiceCheck_sendFailureReturnsInternalError(t *testing.T) {
	svc, st, sender := newTestAlertService(t)
	sender.err = context.DeadlineExceeded
	mustCreate(t, svc, "tenant-1", "a", ruleJSON(0.2, "a@b.com"))

	_, err := svc.Check(context.Background(), "tenant-1", 0.9)
	if err == nil {
		t.Fatal("expected error when email send fails")
	}
	if !pkgerrors.Is(err, pkgerrors.ErrInternal) {
		t.Errorf("error = %v, want ErrInternal", err)
	}
	if len(st.alerts["tenant-1"]) != 1 {
		t.Error("send failure must not lose alerts")
	}
}

func TestServiceCheck_unknownTenant(t *testing.T) {
	svc, _, sender := newTestAlertService(t)
	triggered, err := svc.Check(context.Background(), "tenant-empty", 0.9)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(triggered) != 0 {
		t.Errorf("triggered = %+v, want none for unknown tenant", triggered)
	}
	if len(sender.snapshot()) != 0 {
		t.Error("no emails for unknown tenant")
	}
}
