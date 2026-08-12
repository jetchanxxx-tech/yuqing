// Package analysis provides the analysis orchestration service.
package analysis

import (
	"context"
	"fmt"
	"time"

	"github.com/yuging/platform/internal/pkg/id"
	"github.com/yuging/platform/internal/pkg/queue"
)

// Service orchestrates analysis tasks through their lifecycle.
type Service struct {
	queue        queue.Queue
	concurrency  int
}

// NewService creates an analysis orchestration service.
func NewService(q queue.Queue, concurrency int) *Service {
	return &Service{queue: q, concurrency: concurrency}
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

// Create validates parameters and enqueues a new analysis task.
func (s *Service) Create(ctx context.Context, req CreateAnalysisRequest) (*AnalysisResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("analysis: name is required")
	}
	if req.TenantID == "" {
		return nil, fmt.Errorf("analysis: tenant_id is required")
	}
	if !IsValidState(string(StateDraft)) {
		return nil, fmt.Errorf("analysis: invalid initial state")
	}

	analysisID := id.New()
	now := time.Now()

	result := &AnalysisResult{
		ID:           analysisID,
		Name:         req.Name,
		AnalysisType: req.AnalysisType,
		State:        StateDraft,
		CreatedAt:    now,
	}

	// Transition to queued and publish.
	err := s.queue.Publish(ctx, "analysis.tasks", []byte(analysisID))
	if err != nil {
		return nil, fmt.Errorf("analysis: failed to enqueue: %w", err)
	}

	return result, nil
}
