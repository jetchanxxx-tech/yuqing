package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterDashboardRoutes mounts the dashboard endpoints on the live
// dashboard service.
func RegisterDashboardRoutes(r *gin.RouterGroup, svcs *Services) {
	dash := r.Group("/dashboard")
	dash.GET("/overview", svcs.handleOverview)
	dash.GET("/trend", svcs.handleTrend)
	dash.GET("/sources", svcs.handleSourceBreakdown)
	dash.GET("/topics", svcs.handleTopTopics)
	dash.GET("/alerts", svcs.handleAlerts)
}

// handleOverview returns the summary cards; field shapes are locked by
// DashboardOverview in web/src/api/dashboard.ts.
func (s *Services) handleOverview(c *gin.Context) {
	tid, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	ov, err := s.Dashboard.Overview(c.Request.Context(), tid)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, ov)
}

func (s *Services) handleTrend(c *gin.Context) {
	tid, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	tr, err := s.Dashboard.Trend(c.Request.Context(), tid)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, tr)
}

func (s *Services) handleSourceBreakdown(c *gin.Context) {
	tid, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	src, err := s.Dashboard.Sources(c.Request.Context(), tid)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, src)
}

func (s *Services) handleTopTopics(c *gin.Context) {
	tid, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	topics, err := s.Dashboard.Topics(c.Request.Context(), tid)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"topics": topics})
}

// handleAlerts lists the tenant's configured alert rules (threshold rules,
// not one-shot notifications). Kept on the dashboard group for MVP parity
// with the frontend's /dashboard/* calls.
func (s *Services) handleAlerts(c *gin.Context) {
	tid, ok := tenantID(c)
	if !ok {
		unauthorized(c)
		return
	}
	alerts, err := s.Alert.List(c.Request.Context(), tid)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}
