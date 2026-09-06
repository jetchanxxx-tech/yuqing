// Package alert implements threshold-based email alerts.
package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/id"
)

// EmailSender delivers alert notifications.
type EmailSender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Alert is a configured threshold rule for a tenant.
type Alert struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	Name            string    `json:"name"`
	RuleJSON        string    `json:"rule_json"`
	Threshold       float64   `json:"threshold"` // negative-sentiment fraction 0–1
	RecipientEmail  string    `json:"recipient_email"`
	LastTriggeredAt time.Time `json:"last_triggered_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// rule is the expected shape of RuleJSON.
type rule struct {
	Threshold float64 `json:"threshold"`
	Email     string  `json:"email"`
}

// Store persists alert rules.
type Store interface {
	Create(ctx context.Context, tenantID string, a Alert) error
	Get(ctx context.Context, tenantID, alertID string) (*Alert, error)
	List(ctx context.Context, tenantID string) ([]Alert, error)
	UpdateTrigger(ctx context.Context, tenantID, alertID string, at time.Time) error
}

// Service checks negative-sentiment ratios against alert rules and emails
// the configured recipients when a threshold is reached.
type Service struct {
	store  Store
	sender EmailSender
}

// NewService wires an alert store with an email sender.
// A nil sender silently discards emails (safe for the MVP memory mode).
func NewService(store Store, sender EmailSender) *Service {
	if sender == nil {
		sender = discardSender{}
	}
	return &Service{store: store, sender: sender}
}

// Create validates the rule JSON and stores a new alert.
// ruleJSON format: {"threshold": 0.5, "email": "ops@example.com"}
// where threshold is the negative-sentiment fraction (0, 1].
func (s *Service) Create(ctx context.Context, tenantID, name, ruleJSON string) (*Alert, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("alert: tenant_id is required")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("alert: name is required")
	}

	var r rule
	if err := json.Unmarshal([]byte(ruleJSON), &r); err != nil {
		return nil, fmt.Errorf("alert: invalid rule json: %w", err)
	}
	if r.Threshold <= 0 || r.Threshold > 1 {
		return nil, fmt.Errorf("alert: threshold must be in (0, 1], got %v", r.Threshold)
	}
	email := strings.TrimSpace(r.Email)
	if !strings.Contains(email, "@") {
		return nil, fmt.Errorf("alert: a recipient email is required")
	}

	a := Alert{
		ID:             id.New(),
		TenantID:       tenantID,
		Name:           name,
		RuleJSON:       ruleJSON,
		Threshold:      r.Threshold,
		RecipientEmail: email,
		CreatedAt:      time.Now().UTC(),
	}
	if err := s.store.Create(ctx, tenantID, a); err != nil {
		return nil, err
	}
	return &a, nil
}

// List returns all alert rules of a tenant in creation order.
func (s *Service) List(ctx context.Context, tenantID string) ([]Alert, error) {
	return s.store.List(ctx, tenantID)
}

// Check fires every alert whose threshold is met by negPct (the negative
// sentiment fraction of a completed analysis). Each fire updates
// last_triggered and emails the recipient.
func (s *Service) Check(ctx context.Context, tenantID string, negPct float64) ([]Alert, error) {
	alerts, err := s.store.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	var triggered []Alert
	for i := range alerts {
		a := &alerts[i]
		if negPct < a.Threshold {
			continue
		}

		now := time.Now().UTC()
		if err := s.store.UpdateTrigger(ctx, tenantID, a.ID, now); err != nil {
			return nil, err
		}
		a.LastTriggeredAt = now

		subject := fmt.Sprintf("舆情告警：%s 负面占比 %.1f%% 超过阈值 %.1f%%",
			a.Name, negPct*100, a.Threshold*100)
		body := fmt.Sprintf("租户 %s 的告警「%s」触发：负面舆情占比 %.1f%% ≥ 阈值 %.1f%%。请登录平台查看详情。",
			tenantID, a.Name, negPct*100, a.Threshold*100)
		if err := s.sender.Send(ctx, a.RecipientEmail, subject, body); err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "alert: failed to send email")
		}
		triggered = append(triggered, *a)
	}
	return triggered, nil
}

// discardSender drops emails when no sender is configured.
type discardSender struct{}

func (discardSender) Send(_ context.Context, _, _, _ string) error { return nil }
