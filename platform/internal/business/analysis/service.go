// Package analysis provides the analysis orchestration service.
package analysis

import (
	"context"
	"fmt"
	"time"

	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/pkg/id"
	"github.com/yuging/platform/internal/pkg/queue"
)

// topicAnalysisTasks is the queue topic for newly scheduled analysis runs.
const topicAnalysisTasks = "analysis.tasks"

// Service orchestrates analysis tasks through their lifecycle.
// Runs are persisted in an in-memory store keyed by tenant, then published
// to the queue. The queue stays the only cross-process handoff point.
type Service struct {
	queue       queue.Queue
	concurrency int
	store       *memoryStore
}

// NewService creates an analysis orchestration service.
func NewService(q queue.Queue, concurrency int) *Service {
	return &Service{
		queue:       q,
		concurrency: concurrency,
		store:       newMemoryStore(),
	}
}

// CreateAnalysisRequest holds the parameters for a new analysis.
type CreateAnalysisRequest struct {
	TenantID     string   `json:"tenant_id"`
	UserID       string   `json:"user_id"`
	Name         string   `json:"name"`
	AnalysisType string   `json:"analysis_type"`
	Keywords     []string `json:"keywords"`
	Sources      []string `json:"sources"`
	DateFrom     string   `json:"date_from,omitempty"`
	DateTo       string   `json:"date_to,omitempty"`
}

// AnalysisResult is the full result of a completed analysis.
type AnalysisResult struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	AnalysisType string    `json:"analysis_type"`
	State        State     `json:"state"`
	Progress     int       `json:"progress"`
	ErrorCode    string    `json:"error_code,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Create validates parameters, persists the analysis as queued, and
// publishes a task so workers pick it up.
func (s *Service) Create(ctx context.Context, req CreateAnalysisRequest) (*AnalysisResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("analysis: name is required")
	}
	if req.TenantID == "" {
		return nil, fmt.Errorf("analysis: tenant_id is required")
	}

	now := time.Now().UTC()
	result := &AnalysisResult{
		ID:           id.New(),
		Name:         req.Name,
		AnalysisType: req.AnalysisType,
		State:        StateQueued,
		Progress:     0,
		CreatedAt:    now,
	}

	if err := s.store.put(ctx, req.TenantID, result); err != nil {
		return nil, err
	}

	err := s.queue.Publish(ctx, topicAnalysisTasks, []byte(result.ID))
	if err != nil {
		return nil, fmt.Errorf("analysis: failed to enqueue: %w", err)
	}
	return result, nil
}

// Get returns one analysis scoped to the tenant.
func (s *Service) Get(ctx context.Context, tenantID, analysisID string) (*AnalysisResult, error) {
	return s.store.get(ctx, tenantID, analysisID)
}

// List returns all analyses of a tenant in creation order.
func (s *Service) List(ctx context.Context, tenantID string) ([]AnalysisResult, error) {
	return s.store.list(ctx, tenantID)
}

// Cancel aborts an active analysis. Terminal analyses cannot be canceled.
func (s *Service) Cancel(ctx context.Context, tenantID, analysisID string) error {
	return s.transition(ctx, tenantID, analysisID, StateCanceled)
}

// Transition moves an analysis along the state machine. It is the single
// write path used by workers to advance queued → … → completed.
func (s *Service) Transition(ctx context.Context, tenantID, analysisID, to string) error {
	if !IsValidState(to) {
		return fmt.Errorf("analysis: invalid target state %q", to)
	}
	return s.transition(ctx, tenantID, analysisID, State(to))
}

func (s *Service) transition(ctx context.Context, tenantID, analysisID string, to State) error {
	now := time.Now().UTC()
	err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		if !CanTransition(string(a.State), string(to)) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict,
				fmt.Sprintf("analysis cannot transition from %s to %s", a.State, to))
		}
		a.State = to
		if IsTerminal(string(to)) {
			a.FinishedAt = now
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Rerun requeues a terminal analysis (completed/failed/canceled) as a fresh
// queued run and republishes its task.
func (s *Service) Rerun(ctx context.Context, tenantID, analysisID string) error {
	err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		if !IsTerminal(string(a.State)) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict,
				fmt.Sprintf("only terminal analyses can be rerun, current state is %s", a.State))
		}
		a.State = StateQueued
		a.Progress = 0
		a.ErrorCode = ""
		a.StartedAt = time.Time{}
		a.FinishedAt = time.Time{}
		return nil
	})
	if err != nil {
		return err
	}

	// Re-enqueue: same payload contract as Create.
	if err := s.queue.Publish(ctx, topicAnalysisTasks, []byte(analysisID)); err != nil {
		return fmt.Errorf("analysis: failed to re-enqueue: %w", err)
	}
	return nil
}
