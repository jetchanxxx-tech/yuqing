package v1

import (
	"crypto/subtle"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/business/analysis"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/usage"
	"io"
	"net"
	"net/http"
	"os"
)

func RegisterInternalBillingRoutes(router *gin.Engine, s *Services) {
	authenticate := func(c *gin.Context) {
		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		ip := net.ParseIP(host)
		expected := os.Getenv("YUQING_BILLING_SERVICE_TOKEN")
		actual := c.GetHeader("X-Billing-Service-Token")
		if err != nil || ip == nil || !ip.IsLoopback() || expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, pkgerrors.ToEnvelope(pkgerrors.ErrForbidden, requestID(c)))
			return
		}
		if s.LLMCalls == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, pkgerrors.ToEnvelope(pkgerrors.ErrServiceUnavailable, requestID(c)))
			return
		}
		c.Next()
	}
	group := router.Group("/internal/v1/billing", authenticate)
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
	analyses := router.Group("/internal/v1/analyses", authenticate)
	decodeEvidence := func(c *gin.Context, dst any, limit int64) bool {
		decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(dst); err != nil {
			badRequest(c, "invalid research evidence request")
			return false
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			badRequest(c, "invalid research evidence request")
			return false
		}
		return true
	}
	analyses.POST("/:id/research-intent", func(c *gin.Context) {
		var request analysis.ResearchIntentRequest
		if !decodeEvidence(c, &request, 64<<10) {
			return
		}
		result, err := s.Analysis.SaveResearchIntent(c.Request.Context(), c.Param("id"), request)
		if err != nil {
			respondError(c, err)
			return
		}
		status := http.StatusCreated
		if result.Reused {
			status = http.StatusOK
		}
		c.JSON(status, result)
	})
	analyses.POST("/:id/retrieval-audit", func(c *gin.Context) {
		var request analysis.RetrievalAuditRequest
		if !decodeEvidence(c, &request, 80<<20) {
			return
		}
		if err := s.Analysis.SaveRetrievalAudit(c.Request.Context(), c.Param("id"), request); err != nil {
			respondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"committed": true})
	})

}
