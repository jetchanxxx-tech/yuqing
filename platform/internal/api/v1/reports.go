package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterReportRoutes(r *gin.RouterGroup) {
	reports := r.Group("/reports")
	reports.GET("", handleListReports)
	reports.GET("/:id", handleGetReport)
	reports.GET("/:id/download", handleDownloadReport)
	reports.GET("/templates", handleListTemplates)
}

func handleListReports(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"reports": []gin.H{}, "total": 0})
}

func handleGetReport(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"id": c.Param("id"), "title": "日报", "format": "html", "status": "completed"})
}

func handleDownloadReport(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"download_url": "/api/v1/reports/" + c.Param("id") + "/download?format=html"})
}

func handleListTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"templates": []gin.H{
		{"id": "daily", "name": "日报模板"},
		{"id": "weekly", "name": "周报模板"},
		{"id": "event", "name": "事件分析模板"},
	}})
}
