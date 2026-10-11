package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/accountadmin"
	"net/http"
)

func (s *Services) handleAdminPlanChange(c *gin.Context) {
	var req struct {
		PlanCode        string `json:"plan_code"`
		Reason          string `json:"reason"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if !decodeAdmin(c, &req) {
		return
	}
	if req.ExpectedVersion == nil {
		adminBadRequest(c)
		return
	}
	p := middleware.GetPrincipal(c)
	if p == nil || p.AuthType != "jwt" {
		respondError(c, pkgerrors.ErrForbidden)
		return
	}
	if s.AccountAdmin == nil {
		respondError(c, pkgerrors.ErrServiceUnavailable)
		return
	}
	result, err := s.AccountAdmin.ChangePlan(c.Request.Context(), accountadmin.PlanChange{TenantID: c.Param("id"), ActorID: p.UserID, ActorTokenVersion: p.TokenVersion, PlanCode: req.PlanCode, Reason: req.Reason, RequestID: requestID(c), ExpectedVersion: *req.ExpectedVersion})
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
