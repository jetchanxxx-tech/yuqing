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

func handleListPlans(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plans": []gin.H{
		{"code": "free", "name": "体验版", "price": 0, "quota": "1M tokens"},
		{"code": "pro", "name": "专业版", "price": 9900, "quota": "10M tokens"},
		{"code": "business", "name": "企业版", "price": 49900, "quota": "100M tokens"},
		{"code": "enterprise", "name": "旗舰版", "price": 0, "quota": "unlimited"},
	}})
}

func handleGetSubscription(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plan": "free", "status": "active"})
}

func handleSubscribe(c *gin.Context) {
	var req struct {
		PlanCode string `json:"plan_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "BAD_REQUEST"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscription": gin.H{"plan": req.PlanCode, "status": "active"}})
}

func handleGetUsage(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tokens_used":   0,
		"tokens_quota":  1000000,
		"analyses_used": 0,
		"analyses_quota": 5,
	})
}

func handleListInvoices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"invoices": []gin.H{}, "total": 0})
}

func handleDownloadInvoice(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED"})
}
