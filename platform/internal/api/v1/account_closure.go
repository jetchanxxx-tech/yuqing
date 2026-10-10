package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/accountclosure"
	"net/http"
)

func (s *Services) closureAvailable(c *gin.Context) bool {
	if s.Closure == nil {
		respondError(c, pkgerrors.ErrServiceUnavailable)
		return false
	}
	return true
}
func (s *Services) handleClosurePreview(c *gin.Context) {
	if !s.closureAvailable(c) {
		return
	}
	p := middleware.GetPrincipal(c)
	value, err := s.Closure.Preview(c.Request.Context(), p.UserID, p.TokenVersion)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}
func (s *Services) handleClosureStatus(c *gin.Context) {
	if !s.closureAvailable(c) {
		return
	}
	p := middleware.GetPrincipal(c)
	value, err := s.Closure.Status(c.Request.Context(), p.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, value)
}
func (s *Services) mutateClosure(c *gin.Context, cancel bool) {
	if !s.closureAvailable(c) {
		return
	}
	var input struct {
		Password  string   `json:"password"`
		Confirmed bool     `json:"confirmed"`
		Tenants   []string `json:"close_tenant_ids"`
	}
	if !decodeAdmin(c, &input) {
		return
	}
	if input.Password == "" || !cancel && !input.Confirmed {
		adminBadRequest(c)
		return
	}
	p := middleware.GetPrincipal(c)
	u, err := s.Auth.ClosureCredentialSnapshot(c.Request.Context(), *p, input.Password)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			c.JSON(http.StatusUnauthorized, gin.H{"code": "IDENTITY_CHECK_FAILED", "message": "password verification failed", "request_id": requestID(c)})
			return
		}
		respondError(c, err)
		return
	}
	actor := accountclosure.Actor{UserID: p.UserID, Version: p.TokenVersion, PasswordHash: u.PasswordHash, RequestID: requestID(c)}
	var value *accountclosure.Status
	if cancel {
		value, err = s.Closure.Cancel(c.Request.Context(), actor)
	} else {
		value, err = s.Closure.Request(c.Request.Context(), actor, input.Tenants)
	}
	if err != nil {
		respondError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	status := http.StatusAccepted
	if cancel {
		status = http.StatusOK
	}
	c.JSON(status, value)
}
func (s *Services) handleClosureRequest(c *gin.Context) { s.mutateClosure(c, false) }
func (s *Services) handleClosureCancel(c *gin.Context)  { s.mutateClosure(c, true) }
