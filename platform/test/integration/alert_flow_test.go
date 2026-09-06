package integration

import (
	"context"
	"fmt"
	"testing"
)

// alertRuleJSON builds the rule payload the frontend will send: threshold is
// the negative-sentiment fraction (0, 1].
func alertRuleJSON(threshold float64, email string) string {
	return fmt.Sprintf(`{"threshold": %v, "email": %q}`, threshold, email)
}

// TestIntegration_alertChain drives the real alert service with a recording
// email sender: create → trigger → verify email → silent below threshold →
// re-trigger fires again (alerts are per-check, not deduplicated).
func TestIntegration_alertChain(t *testing.T) {
	svc, sender := newAlertService(t)
	ctx := context.Background()
	const tenantID = "t-alert-owner"
	const recipient = "ops@example.com"

	created, err := svc.Create(ctx, tenantID, "负面舆情告警", alertRuleJSON(0.7, recipient))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if created.Threshold != 0.7 || created.RecipientEmail != recipient {
		t.Fatalf("created alert = %+v, want threshold 0.7 to %s", created, recipient)
	}

	t.Run("check above threshold fires one email", func(t *testing.T) {
		triggered, err := svc.Check(ctx, tenantID, 0.85)
		if err != nil {
			t.Fatalf("Check failed: %v", err)
		}
		if len(triggered) != 1 || triggered[0].ID != created.ID {
			t.Fatalf("triggered = %+v, want the one alert", triggered)
		}
		calls := sender.snapshot()
		if len(calls) != 1 {
			t.Fatalf("emails sent = %d, want 1", len(calls))
		}
		if calls[0].to != recipient {
			t.Errorf("email to = %q, want %q", calls[0].to, recipient)
		}
		if calls[0].subject == "" || calls[0].body == "" {
			t.Error("email subject/body empty")
		}
	})

	t.Run("last triggered timestamp was recorded", func(t *testing.T) {
		list, err := svc.List(ctx, tenantID)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		if len(list) != 1 || list[0].LastTriggeredAt.IsZero() {
			t.Fatalf("list = %+v, want 1 alert with LastTriggeredAt set", list)
		}
	})

	t.Run("check below threshold stays silent", func(t *testing.T) {
		triggered, err := svc.Check(ctx, tenantID, 0.3)
		if err != nil {
			t.Fatalf("Check failed: %v", err)
		}
		if len(triggered) != 0 {
			t.Fatalf("triggered = %+v, want none below threshold", triggered)
		}
		if calls := sender.snapshot(); len(calls) != 1 {
			t.Fatalf("emails sent = %d, want still 1", len(calls))
		}
	})

	t.Run("re-check above threshold fires again", func(t *testing.T) {
		if _, err := svc.Check(ctx, tenantID, 0.9); err != nil {
			t.Fatalf("Check failed: %v", err)
		}
		if calls := sender.snapshot(); len(calls) != 2 {
			t.Fatalf("emails sent = %d, want 2 (per-check firing)", len(calls))
		}
	})
}

// TestIntegration_alertMultiRuleOnlyMatchingFire: with two rules at different
// thresholds one check can fire exactly one of them.
func TestIntegration_alertMultiRuleOnlyMatchingFire(t *testing.T) {
	svc, sender := newAlertService(t)
	ctx := context.Background()
	const tenantID = "t-alert-multi"

	low, err := svc.Create(ctx, tenantID, "严格告警", alertRuleJSON(0.5, "a@example.com"))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	high, err := svc.Create(ctx, tenantID, "宽松告警", alertRuleJSON(0.9, "b@example.com"))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	triggered, err := svc.Check(ctx, tenantID, 0.6)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if len(triggered) != 1 || triggered[0].ID != low.ID {
		t.Fatalf("triggered = %+v, want only the 0.5 rule", triggered)
	}
	if high.Threshold == 0.5 || low.Threshold != 0.5 {
		t.Fatal("rule bookkeeping corrupt")
	}
	if calls := sender.snapshot(); len(calls) != 1 {
		t.Fatalf("emails = %d, want 1 (only the matching rule)", len(calls))
	}
}

// TestIntegration_alertValidation mirrors the create-time rules the frontend
// depends on: bad JSON, out-of-range thresholds and missing recipients are
// rejected before any rule is stored.
func TestIntegration_alertValidation(t *testing.T) {
	svc, _ := newAlertService(t)
	ctx := context.Background()

	cases := []struct {
		name     string
		ruleJSON string
	}{
		{"invalid json", `{threshold: 0.5}`},
		{"threshold zero", alertRuleJSON(0, "a@example.com")},
		{"threshold above one", alertRuleJSON(1.5, "a@example.com")},
		{"missing email", alertRuleJSON(0.5, "")},
		{"email without host", alertRuleJSON(0.5, "not-an-email")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, "t-alert-validate", "x", tc.ruleJSON)
			if err == nil {
				t.Fatalf("Create accepted rule %q", tc.ruleJSON)
			}
		})
	}

	// Nothing was stored.
	list, err := svc.List(ctx, "t-alert-validate")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("stored %d invalid alerts", len(list))
	}
}
