package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/api/middleware"
	"github.com/yuging/platform/internal/business/analysis"
	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
)

// RegisterAnalysisRoutes mounts the analysis lifecycle endpoints on the live
// analysis service (real create/get/list/cancel/rerun state machine).
func RegisterAnalysisRoutes(r *gin.RouterGroup, svcs *Services) {
	analyses := r.Group("/analyses")
	analyses.POST("", middleware.RequirePermission("analyses:create"), svcs.handleCreateAnalysis)
	analyses.GET("", middleware.RequirePermission("analyses:list"), svcs.handleListAnalyses)
	analyses.GET("/:id", svcs.handleGetAnalysis)
	analyses.GET("/:id/result", svcs.handleGetAnalysisResult)
	analyses.POST("/:id/cancel", svcs.handleCancelAnalysis)
	analyses.POST("/:id/rerun", svcs.handleRerunAnalysis)
	analyses.GET("/:id/events", middleware.RequirePermission("analyses:list"), svcs.handleAnalysisEvents)
}

// handleCreateAnalysis creates a queued analysis and publishes its task.
// Response shape: AnalysisSummary in web/src/api/analyses.ts.
func (s *Services) handleCreateAnalysis(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	var req analysis.CreateAnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	req.TenantID = p.TenantID
	req.UserID = p.UserID
	if strings.TrimSpace(req.Name) == "" {
		badRequest(c, "name is required")
		return
	}

	result, err := s.Analysis.Create(c.Request.Context(), req)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// handleListAnalyses returns the tenant's analyses with the paging envelope
// AnalysisListResponse.
func (s *Services) handleListAnalyses(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	items, err := s.Analysis.List(c.Request.Context(), p.TenantID)
	if err != nil {
		respondError(c, err)
		return
	}
	if items == nil {
		items = []analysis.AnalysisResult{}
	}
	c.JSON(http.StatusOK, gin.H{
		"analyses":  items,
		"tenant_id": p.TenantID,
		"total":     len(items),
	})
}

// handleGetAnalysis returns one analysis scoped to the principal's tenant.
// Missing ids (including other tenants' ids) answer 404 NOT_FOUND.
func (s *Services) handleGetAnalysis(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	a, err := s.Analysis.Get(c.Request.Context(), p.TenantID, c.Param("id"))
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "analysis not found")
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// handleGetAnalysisResult returns the analysis result envelope. Real
// documents/sentiments/topics aggregation waits for the documents store
// (engine pipeline); until then the arrays are empty and truthful.
func (s *Services) handleGetAnalysisResult(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	a, err := s.Analysis.Get(c.Request.Context(), p.TenantID, c.Param("id"))
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "analysis not found")
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":         a.ID,
		"state":      a.State,
		"documents":  []gin.H{},
		"sentiments": gin.H{"positive": 0, "negative": 0, "neutral": 0},
		"topics":     []gin.H{},
	})
}

// handleCancelAnalysis transitions an active analysis to canceled.
func (s *Services) handleCancelAnalysis(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	ctx := c.Request.Context()
	if err := s.Analysis.Cancel(ctx, p.TenantID, c.Param("id")); err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "analysis not found")
			return
		}
		respondError(c, err)
		return
	}
	a, err := s.Analysis.Get(ctx, p.TenantID, c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// handleRerunAnalysis requeues a terminal analysis as a fresh queued run.
func (s *Services) handleRerunAnalysis(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	ctx := c.Request.Context()
	if err := s.Analysis.Rerun(ctx, p.TenantID, c.Param("id")); err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "analysis not found")
			return
		}
		respondError(c, err)
		return
	}
	a, err := s.Analysis.Get(ctx, p.TenantID, c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// handleAnalysisEvents streams analysis progress over Server-Sent Events.
//
// Contract (GET /api/v1/analyses/:id/events, analyses:list):
//   - 200 + text/event-stream; frames are "event: <kind>\ndata: <json>\n\n"
//   - kind=progress while the state machine is active, one frame per change
//     ({"state","progress"}); kind=final on the terminal state, then close
//   - terminal analyses get the final frame immediately; a client that hangs
//     up (request context canceled) stops the poll loop
//
// The MVP has no in-process pub/sub — the API process and worker only share
// state through the store — so progress is detected by polling
// Analysis.Get on SSEPollInterval (default 1s). Redis pub/sub replaces this
// when the multi-instance deployment lands.
func (s *Services) handleAnalysisEvents(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	ctx := c.Request.Context()
	a, err := s.Analysis.Get(ctx, p.TenantID, c.Param("id"))
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "analysis not found")
			return
		}
		respondError(c, err)
		return
	}

	// SSE headers — written only after the analysis is confirmed visible.
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	frame := func(kind string, snap *analysis.AnalysisResult) error {
		payload, err := json.Marshal(gin.H{"state": snap.State, "progress": snap.Progress})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", kind, payload); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}

	if analysis.IsTerminal(string(a.State)) {
		_ = frame("final", a)
		return
	}
	if err := frame("progress", a); err != nil {
		return
	}

	interval := s.SSEPollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	last := *a
	for {
		select {
		case <-ctx.Done():
			return // client disconnected
		case <-ticker.C:
			cur, err := s.Analysis.Get(ctx, p.TenantID, c.Param("id"))
			if err != nil {
				return // analysis deleted or store failure: close
			}
			if cur.State == last.State && cur.Progress == last.Progress {
				continue
			}
			kind := "progress"
			if analysis.IsTerminal(string(cur.State)) {
				kind = "final"
			}
			if err := frame(kind, cur); err != nil {
				return
			}
			if kind == "final" {
				return
			}
			last = *cur
		}
	}
}

// notFound writes the standard 404 envelope.
func notFound(c *gin.Context, message string) {
	c.JSON(http.StatusNotFound, gin.H{
		"code":       "NOT_FOUND",
		"message":    message,
		"request_id": requestID(c),
	})
}
