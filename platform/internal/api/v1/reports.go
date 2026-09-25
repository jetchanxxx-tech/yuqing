package v1

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/business/report"
	"github.com/yuqing/platform/internal/engine"
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

// handleDownloadReport streams the report file (html from analyses.report_content,
// docx proxied from the Python report engine). Plan gating is checked via DownloadURL.
func (s *Services) handleDownloadReport(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	format := c.DefaultQuery("format", "html")
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

	switch format {
	case "html":
		// HTML stored in analyses.report_content
		a, err := s.Analysis.Get(c.Request.Context(), p.TenantID, r.AnalysisID)
		if err != nil {
			respondError(c, err)
			return
		}
		if a.ReportContent == "" {
			notFound(c, "report content not generated")
			return
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Header("Content-Disposition", `attachment; filename="report-`+reportID+`.html"`)
		c.String(http.StatusOK, a.ReportContent)

	case "docx":
		// docx proxied from Python report engine
		if s.ReportEngine == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "report engine not configured"})
			return
		}

		// Get analysis to build GenerateReq
		a, err := s.Analysis.Get(c.Request.Context(), p.TenantID, r.AnalysisID)
		if err != nil {
			respondError(c, err)
			return
		}

		// Convert analysis.Topic to engine.TopicResult
		topics := make([]engine.TopicResult, len(a.Topics))
		for i, t := range a.Topics {
			topics[i] = engine.TopicResult{
				ID:       t.ID,
				Name:     t.Name,
				Keywords: t.Keywords,
				DocCount: t.DocCount,
				Trend:    t.Trend,
			}
		}

		// Convert analysis.Sentiment to engine.SentimentResult
		sentiments := make([]engine.SentimentResult, len(a.Sentiments))
		for i, s := range a.Sentiments {
			sentiments[i] = engine.SentimentResult{
				DocumentID: s.DocumentID,
				Sentiment:  s.Sentiment,
				Level:      s.Level,
				Confidence: s.Confidence,
				Score:      s.Score,
			}
		}

		// Build request for Python engine
		title := a.Name
		if len(a.Keywords) > 0 {
			title = a.Keywords[0]
		}
		req := &engine.ReportGenerateReq{
			Title:      title,
			AnalysisID: r.AnalysisID,
			Topics:     topics,
			Sentiments: sentiments,
			Format:     "docx",
		}

		stream, err := s.ReportEngine.GenerateStream(c.Request.Context(), req, "docx")
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
