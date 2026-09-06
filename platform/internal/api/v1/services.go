package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/yuging/platform/internal/api/middleware"
	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/platform/auth"
	"github.com/yuging/platform/internal/platform/tenant"

	"github.com/yuging/platform/internal/business/alert"
	"github.com/yuging/platform/internal/business/analysis"
	"github.com/yuging/platform/internal/business/dashboard"
	"github.com/yuging/platform/internal/business/report"
)

// Services bundles the service dependencies v1 handlers call. It is wired
// once in internal/app/container.go (hand-rolled DI, see CLAUDE.md).
type Services struct {
	Auth      *auth.Service
	Analysis  *analysis.Service
	Dashboard *dashboard.Service
	Report    *report.Service
	Tenant    *tenant.Service
	Alert     *alert.Service
}

// requestID reads the request_id propagated by middleware.RequestID.
func requestID(c *gin.Context) string {
	return c.GetString(string(middleware.CtxRequestID))
}

// respondError maps a typed sentinel error to the API error envelope.
// Unwrapped errors map to INTERNAL/500 via pkgerrors.CodeFor.
func respondError(c *gin.Context, err error) {
	code, status := pkgerrors.CodeFor(err)
	if code == "" {
		code = "INTERNAL"
		status = http.StatusInternalServerError
	}
	c.JSON(status, gin.H{
		"code":       code,
		"message":    err.Error(),
		"request_id": requestID(c),
	})
}

// tenantID returns the authenticated principal's tenant ID.
func tenantID(c *gin.Context) (string, bool) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		return "", false
	}
	return p.TenantID, true
}

// unauthorized writes the standard 401 envelope (principal absent).
func unauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{
		"code":       "UNAUTHORIZED",
		"message":    "authentication required",
		"request_id": requestID(c),
	})
}
