package v1

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/business/report"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// RegisterReportRoutes mounts the report endpoints on the live report store.
func RegisterReportRoutes(r *gin.RouterGroup, svcs *Services) {
	reports := r.Group("/reports")
	reports.GET("", middleware.RequirePermission("reports:read"), svcs.handleListReports)
	reports.GET("/:id", middleware.RequirePermission("reports:read"), svcs.handleGetReport)
	reports.GET("/:id/download", middleware.RequirePermission("reports:download"), svcs.handleDownloadReport)
	reports.GET("/templates", middleware.RequirePermission("reports:read"), svcs.handleListTemplates)
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

// handleDownloadReport exports an immutable published snapshot.
// Word conversion never reruns analysis or calls a model.
func (s *Services) handleDownloadReport(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	format := c.DefaultQuery("format", "html")
	if unverifiedAPIKeyOwner(c) && format != "html" {
		respondError(c, pkgerrors.Wrap(pkgerrors.ErrAPIKeyOwnerUnverified, "API Key 创建者无法核验，请撤销后重新签发。"))
		return
	}
	reportID := c.Param("id")

	// Verify report exists + tenant owns it + plan allows format
	_, err := s.Report.DownloadURL(c.Request.Context(), p.TenantID, reportID, format)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "report not found")
			return
		}
		respondError(c, err)
		return
	}

	// Get report to find analysis_id
	r, err := s.Report.Get(c.Request.Context(), p.TenantID, reportID)
	if err != nil {
		respondError(c, err)
		return
	}

	if r.Status != "completed" || r.ContentHTML == "" {
		respondError(c, pkgerrors.Wrap(pkgerrors.ErrConflict, "该报告没有可核验的版本正文，不能使用其他版本替代。"))
		return
	}
	switch format {
	case "html":
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="report-`+reportID+`.html"`)
		c.String(http.StatusOK, r.ContentHTML)
	case "docx":
		if s.ReportEngine == nil {
			respondError(c, pkgerrors.Wrap(pkgerrors.ErrInternal, "report export engine not configured"))
			return
		}
		stream, err := s.ReportEngine.ExportStream(c.Request.Context(), r.ContentHTML)
		if err != nil {
			respondError(c, err)
			return
		}
		defer stream.Close()
		c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		c.Header("Content-Disposition", `attachment; filename="report-`+reportID+`.docx"`)
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, stream)

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported format: " + format})
	}
}

// handleListTemplates lists the built-in report templates (static catalog).
func (s *Services) handleListTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"templates": []gin.H{
		{"id": "daily", "name": "日报模板"},
		{"id": "weekly", "name": "周报模板"},
		{"id": "event", "name": "事件分析模板"},
	}})
}
