package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/api/middleware"
	"github.com/yuging/platform/internal/pkg/id"
)

func RegisterAnalysisRoutes(r *gin.RouterGroup) {
	analyses := r.Group("/analyses")
	analyses.POST("", middleware.RequirePermission("analyses:create"), handleCreateAnalysis)
	analyses.GET("", middleware.RequirePermission("analyses:list"), handleListAnalyses)
	analyses.GET("/:id", handleGetAnalysis)
	analyses.GET("/:id/result", handleGetAnalysisResult)
	analyses.POST("/:id/cancel", handleCancelAnalysis)
	analyses.POST("/:id/rerun", handleRerunAnalysis)
	analyses.GET("/:id/events", handleAnalysisEvents)
}

func handleCreateAnalysis(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "UNAUTHORIZED"})
		return
	}
	var req struct {
		Name         string   `json:"name"`
		AnalysisType string   `json:"analysis_type"`
		Keywords     []string `json:"keywords"`
		Sources      []string `json:"sources"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "BAD_REQUEST", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":            id.New(),
		"name":          req.Name,
		"analysis_type": req.AnalysisType,
		"state":         "queued",
		"created_at":    "2026-08-12T00:00:00Z",
	})
}

func handleListAnalyses(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "UNAUTHORIZED"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"analyses":  []gin.H{},
		"tenant_id": p.TenantID,
		"total":     0,
	})
}

func handleGetAnalysis(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"id":    c.Param("id"),
		"state": "draft",
	})
}

func handleGetAnalysisResult(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"id":         c.Param("id"),
		"state":      "completed",
		"documents":  []gin.H{},
		"sentiments": gin.H{"positive": 0, "negative": 0, "neutral": 0},
	})
}

func handleCancelAnalysis(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"id":    c.Param("id"),
		"state": "canceled",
	})
}

func handleRerunAnalysis(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, notImpl("rerun"))
}

func handleAnalysisEvents(c *gin.Context) {
	// SSE endpoint — stub.
	c.JSON(http.StatusNotImplemented, notImpl("events"))
}

func notImpl(action string) gin.H {
	return gin.H{"code": "NOT_IMPLEMENTED", "message": action}
}
