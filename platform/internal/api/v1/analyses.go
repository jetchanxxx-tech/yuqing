package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterAnalysisRoutes(r *gin.RouterGroup) {
	analyses := r.Group("/analyses")
	analyses.POST("", handleCreateAnalysis)
	analyses.GET("", handleListAnalyses)
	analyses.GET("/:id", handleGetAnalysis)
	analyses.GET("/:id/result", handleGetAnalysisResult)
	analyses.POST("/:id/cancel", handleCancelAnalysis)
	analyses.POST("/:id/rerun", handleRerunAnalysis)
	analyses.GET("/:id/events", handleAnalysisEvents) // SSE
}

func handleCreateAnalysis(c *gin.Context)   { c.JSON(http.StatusNotImplemented, notImpl("create")) }
func handleListAnalyses(c *gin.Context)     { c.JSON(http.StatusNotImplemented, notImpl("list")) }
func handleGetAnalysis(c *gin.Context)      { c.JSON(http.StatusNotImplemented, notImpl("get")) }
func handleGetAnalysisResult(c *gin.Context) { c.JSON(http.StatusNotImplemented, notImpl("result")) }
func handleCancelAnalysis(c *gin.Context)   { c.JSON(http.StatusNotImplemented, notImpl("cancel")) }
func handleRerunAnalysis(c *gin.Context)    { c.JSON(http.StatusNotImplemented, notImpl("rerun")) }
func handleAnalysisEvents(c *gin.Context)   { c.JSON(http.StatusNotImplemented, notImpl("events")) }

func notImpl(action string) gin.H {
	return gin.H{"code": "NOT_IMPLEMENTED", "message": "analysis:" + action}
}
