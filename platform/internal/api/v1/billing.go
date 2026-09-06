package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterBillingRoutes mounts the billing endpoints. /plans serves the real
// plan catalog (frontend Plan fields price_monthly_cny/token_quota_m live on
// billing.Plan); subscription/usage/invoices stay placeholders until the
// subscription store and the shared usage meter are wired.
func RegisterBillingRoutes(r *gin.RouterGroup, svcs *Services) {
	billingGroup := r.Group("/billing")
	billingGroup.GET("/plans", handleListPlans)
	billingGroup.GET("/subscription", handleGetSubscription)
	billingGroup.POST("/subscribe", handleSubscribe)
	billingGroup.GET("/usage", handleGetUsage)
	billingGroup.GET("/invoices", handleListInvoices)
	billingGroup.GET("/invoices/:id/download", handleDownloadInvoice)
}

// handleListPlans serves the four real tiers from billing.DefaultPlans.
func handleListPlans(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plans": sortedPlans()})
}

// handleGetSubscription: placeholder until the subscription store exists —
// every tenant renders as the free plan.
func handleGetSubscription(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plan": "free", "status": "active"})
}

// handleSubscribe: placeholder until plan changes are transactional.
func handleSubscribe(c *gin.Context) {
	var req struct {
		PlanCode string `json:"plan_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "request body must be JSON {plan_code}")
		return
	}
	if req.PlanCode == "" {
		badRequest(c, "plan_code is required")
		return
	}
	c.JSON(http.StatusOK, gin.H{"subscription": gin.H{"plan": req.PlanCode, "status": "active"}})
}

// handleGetUsage: placeholder until the shared usage meter is wired to the
// tenant's token counter.
func handleGetUsage(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tokens_used":    0,
		"tokens_quota":   1000000,
		"analyses_used":  0,
		"analyses_quota": 5,
	})
}

// handleListInvoices: placeholder until invoice generation (worker) persists.
func handleListInvoices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"invoices": []gin.H{}, "total": 0})
}

// handleDownloadInvoice: invoice file storage is pending — JSON envelope.
func handleDownloadInvoice(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"code":       "NOT_IMPLEMENTED",
		"message":    "invoice download not implemented (invoice file storage pending)",
		"request_id": requestID(c),
	})
}
