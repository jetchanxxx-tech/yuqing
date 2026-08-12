package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterDashboardRoutes(r *gin.RouterGroup) {
	dash := r.Group("/dashboard")
	dash.GET("/overview", handleOverview)
	dash.GET("/trend", handleTrend)
	dash.GET("/sources", handleSourceBreakdown)
	dash.GET("/topics", handleTopTopics)
	dash.GET("/alerts", handleAlerts)
}

func handleOverview(c *gin.Context)       { c.JSON(http.StatusNotImplemented, notImpl("dashboard:overview")) }
func handleTrend(c *gin.Context)          { c.JSON(http.StatusNotImplemented, notImpl("dashboard:trend")) }
func handleSourceBreakdown(c *gin.Context) { c.JSON(http.StatusNotImplemented, notImpl("dashboard:sources")) }
func handleTopTopics(c *gin.Context)      { c.JSON(http.StatusNotImplemented, notImpl("dashboard:topics")) }
func handleAlerts(c *gin.Context)         { c.JSON(http.StatusNotImplemented, notImpl("dashboard:alerts")) }
