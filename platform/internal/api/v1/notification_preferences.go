package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/accountnotify"
	"net/http"
)

func (s *Services) handleNotificationPreferences(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil || p.AuthType != "jwt" {
		respondError(c, pkgerrors.ErrForbidden)
		return
	}
	if s.TaskNotifications == nil {
		respondError(c, pkgerrors.ErrServiceUnavailable)
		return
	}
	prefs, err := s.TaskNotifications.Preferences(c.Request.Context(), *p)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, notificationPreferenceResponse(prefs))
}
func notificationPreferenceResponse(prefs accountnotify.Preferences) gin.H {
	return gin.H{"preferences": prefs, "supported": []string{"task_completed"}, "unavailable": []string{"alert_triggered", "billing_reminder", "product_updates"}}
}
func (s *Services) handleSetNotificationPreferences(c *gin.Context) {
	var req struct {
		TaskCompleted *bool `json:"task_completed"`
	}
	if !decodeAdmin(c, &req) {
		return
	}
	if req.TaskCompleted == nil {
		adminBadRequest(c)
		return
	}
	p := middleware.GetPrincipal(c)
	if p == nil || p.AuthType != "jwt" {
		respondError(c, pkgerrors.ErrForbidden)
		return
	}
	if s.TaskNotifications == nil {
		respondError(c, pkgerrors.ErrServiceUnavailable)
		return
	}
	prefs := accountnotify.Preferences{TaskCompleted: *req.TaskCompleted}
	if err := s.TaskNotifications.SetPreferences(c.Request.Context(), *p, prefs); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, notificationPreferenceResponse(prefs))
}
