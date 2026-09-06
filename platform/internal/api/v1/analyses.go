package v1

import (
	"net/http"
	"strings"

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
	analyses.GET("/:id/events", svcs.handleAnalysisEvents)
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

// handleAnalysisEvents is the future SSE progress channel. The contract is
// not defined yet; answer the JSON envelope until then.
func (s *Services) handleAnalysisEvents(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"code":       "NOT_IMPLEMENTED",
		"message":    "analysis events (SSE) not implemented",
		"request_id": requestID(c),
	})
}

// notFound writes the standard 404 envelope.
func notFound(c *gin.Context, message string) {
	c.JSON(http.StatusNotFound, gin.H{
		"code":       "NOT_FOUND",
		"message":    message,
		"request_id": requestID(c),
	})
}
