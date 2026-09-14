package v1

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/apikey"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/payment"
	"github.com/yuqing/platform/internal/platform/settings"
	"github.com/yuqing/platform/internal/platform/tenant"
	"github.com/yuqing/platform/internal/platform/usage"

	"github.com/yuqing/platform/internal/business/alert"
	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/business/dashboard"
	"github.com/yuqing/platform/internal/business/report"
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
	Settings  settings.Store
	APIKey    *apikey.Service
	Usage     usage.PlatformMeter

	// 收费体系（方案 B）：额度与支付。
	Credits        *credit.Service
	Payment        *payment.Service
	PaymentRegistry *payment.Registry // 可空：nil 时购买页看不到可用渠道

	// SSEPollInterval is how often /analyses/:id/events re-reads the state
	// machine while streaming. Zero selects the default (1s); tests shrink it.
	SSEPollInterval time.Duration
}

// requestID reads the request_id propagated by middleware.RequestID.
func requestID(c *gin.Context) string {
	return c.GetString(string(middleware.CtxRequestID))
}

// respondError maps a typed sentinel error to the API error envelope.
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