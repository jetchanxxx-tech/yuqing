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

func handleListReports(c *gin.Context)    { c.JSON(http.StatusNotImplemented, notImpl("reports:list")) }
func handleGetReport(c *gin.Context)      { c.JSON(http.StatusNotImplemented, notImpl("reports:get")) }
func handleDownloadReport(c *gin.Context) { c.JSON(http.StatusNotImplemented, notImpl("reports:download")) }
func handleListTemplates(c *gin.Context)  { c.JSON(http.StatusNotImplemented, notImpl("reports:templates")) }
