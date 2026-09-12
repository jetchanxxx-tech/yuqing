package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/business/report"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// RegisterReportRoutes mounts the report endpoints on the live report store.
func RegisterReportRoutes(r *gin.RouterGroup, svcs *Services) {
	reports := r.Group("/reports")
	reports.GET("", svcs.handleListReports)
	reports.GET("/:id", svcs.handleGetReport)
	reports.GET("/:id/download", svcs.handleDownloadReport)
	reports.GET("/templates", svcs.handleListTemplates)
}

// handleListReports returns the tenant's reports. Unknown/missing reports are
// simply absent — no canned rows.
func (s *Services) handleListReports(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	items, err := s.Report.List(c.Request.Context(), p.TenantID)
	if err != nil {
		respondError(c, err)
		return
	}
	if items == nil {
		items = []report.Report{}
	}
	c.JSON(http.StatusOK, gin.H{"reports": items, "total": len(items)})
}

// handleGetReport returns one report scoped to the tenant; missing ids 404.
func (s *Services) handleGetReport(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	r, err := s.Report.Get(c.Request.Context(), p.TenantID, c.Param("id"))
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "report not found")
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, r)
}

// handleDownloadReport validates the report exists and that the tenant's plan
// includes its format, then returns the API-relative download URL (file bytes
// wait for the storage driver — the URL keeps the client contract intact).
func (s *Services) handleDownloadReport(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	format := c.DefaultQuery("format", "")
	_, err := s.Report.DownloadURL(c.Request.Context(), p.TenantID, c.Param("id"), format)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "report not found")
			return
		}
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"download_url": "/reports/" + c.Param("id") + "/download?format=" + format,
	})
}

// handleListTemplates lists the built-in report templates (static catalog).
func (s *Services) handleListTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"templates": []gin.H{
		{"id": "daily", "name": "日报模板"},
		{"id": "weekly", "name": "周报模板"},
		{"id": "event", "name": "事件分析模板"},
	}})
}
