package v1

import (
	"crypto/subtle"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/platform/usage"
	"io"
	"net"
	"net/http"
	"os"
)

func RegisterInternalBillingRoutes(router *gin.Engine, s *Services) {
	group := router.Group("/internal/v1/billing", func(c *gin.Context) {
		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		ip := net.ParseIP(host)
		expected := os.Getenv("YUQING_BILLING_SERVICE_TOKEN")
		actual := c.GetHeader("X-Billing-Service-Token")
		if err != nil || ip == nil || !ip.IsLoopback() || expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": "FORBIDDEN"})
			return
		}
		if s.LLMCalls == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"code": "SERVICE_UNAVAILABLE"})
			return
		}
		c.Next()
	})
	decode := func(c *gin.Context, dst any) bool {
		d := json.NewDecoder(io.LimitReader(c.Request.Body, 16385))
		d.DisallowUnknownFields()
		if err := d.Decode(dst); err != nil {
			badRequest(c, "invalid billing event")
			return false
		}
		if err := d.Decode(&struct{}{}); err != io.EOF {
			badRequest(c, "invalid billing event")
			return false
		}
		return true
	}
	group.POST("/llm-authorizations", func(c *gin.Context) {
		var request usage.CallAuthorizationRequest
		if !decode(c, &request) {
			return
		}
		result, err := s.LLMCalls.Authorize(c.Request.Context(), request)
		if err != nil {
			respondError(c, err)
			return
		}
		status := http.StatusCreated
		if result.Duplicate {
			status = http.StatusOK
		}
		c.JSON(status, result)
	})
	group.POST("/usage-events", func(c *gin.Context) {
		var event usage.ProviderUsageEvent
		if !decode(c, &event) {
			return
		}
		duplicate, err := s.LLMCalls.Record(c.Request.Context(), event, c.GetHeader("X-LLM-Call-Permit"))
		if err != nil {
			respondError(c, err)
			return
		}
		status := http.StatusCreated
		if duplicate {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"committed": true, "duplicate": duplicate})
	})
}
