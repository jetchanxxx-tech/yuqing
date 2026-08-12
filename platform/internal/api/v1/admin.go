package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterAdminRoutes(r *gin.RouterGroup) {
	admin := r.Group("/admin")
	admin.GET("/tenants", handleListTenants)
	admin.POST("/tenants/:id/suspend", handleSuspendTenant)
	admin.POST("/tenants/:id/resume", handleResumeTenant)
	admin.GET("/usage", handlePlatformUsage)
	admin.GET("/plans", handleAdminListPlans)
	admin.POST("/plans", handleAdminCreatePlan)
}

func handleListTenants(c *gin.Context)    { c.JSON(http.StatusNotImplemented, notImpl("admin:tenants:list")) }
func handleSuspendTenant(c *gin.Context)   { c.JSON(http.StatusNotImplemented, notImpl("admin:tenants:suspend")) }
func handleResumeTenant(c *gin.Context)   { c.JSON(http.StatusNotImplemented, notImpl("admin:tenants:resume")) }
func handlePlatformUsage(c *gin.Context)  { c.JSON(http.StatusNotImplemented, notImpl("admin:usage")) }
func handleAdminListPlans(c *gin.Context) { c.JSON(http.StatusNotImplemented, notImpl("admin:plans:list")) }
func handleAdminCreatePlan(c *gin.Context) { c.JSON(http.StatusNotImplemented, notImpl("admin:plans:create")) }
