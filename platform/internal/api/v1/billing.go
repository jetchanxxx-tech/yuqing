package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RegisterBillingRoutes(r *gin.RouterGroup) {
	billing := r.Group("/billing")
	billing.GET("/plans", handleListPlans)
	billing.GET("/subscription", handleGetSubscription)
	billing.POST("/subscribe", handleSubscribe)
	billing.GET("/usage", handleGetUsage)
	billing.GET("/invoices", handleListInvoices)
	billing.GET("/invoices/:id/download", handleDownloadInvoice)
}

func handleListPlans(c *gin.Context)       { c.JSON(http.StatusNotImplemented, notImpl("plans:list")) }
func handleGetSubscription(c *gin.Context) { c.JSON(http.StatusNotImplemented, notImpl("subscription:get")) }
func handleSubscribe(c *gin.Context)       { c.JSON(http.StatusNotImplemented, notImpl("subscribe")) }
func handleGetUsage(c *gin.Context)        { c.JSON(http.StatusNotImplemented, notImpl("usage:get")) }
func handleListInvoices(c *gin.Context)    { c.JSON(http.StatusNotImplemented, notImpl("invoices:list")) }
func handleDownloadInvoice(c *gin.Context)  { c.JSON(http.StatusNotImplemented, notImpl("invoices:download")) }
