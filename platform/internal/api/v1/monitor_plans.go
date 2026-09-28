package v1

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/business/monitorplan"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// These endpoints manage drafts only. No run/enable route exists.
func RegisterMonitorPlanRoutes(r *gin.RouterGroup, services *Services) {
	plans := r.Group("/monitor-plans")
	plans.GET("/templates", middleware.RequirePermission("analyses:list"), services.listMonitorTemplates)
	plans.POST("/preview", middleware.RequirePermission("analyses:create"), services.previewMonitorPlan)
	plans.POST("", middleware.RequirePermission("analyses:create"), services.createMonitorPlan)
	plans.GET("", middleware.RequirePermission("analyses:list"), services.listMonitorPlans)
	plans.GET("/:id", middleware.RequirePermission("analyses:read"), services.getMonitorPlan)
	plans.PATCH("/:id", middleware.RequirePermission("analyses:create"), services.patchMonitorPlan)
	plans.DELETE("/:id", middleware.RequirePermission("analyses:create"), services.deleteMonitorPlan)
}

func monitorBadRequest(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"code": "BAD_REQUEST", "message": message, "details": nil, "request_id": requestID(c)})
}

func monitorRespondError(c *gin.Context, err error) {
	_, status := pkgerrors.CodeFor(err)
	envelope := pkgerrors.ToEnvelope(err, requestID(c))
	c.JSON(status, gin.H{"code": envelope.Code, "message": envelope.Message, "details": envelope.Details, "request_id": envelope.RequestID})
}

func monitorOwnerForbidden(c *gin.Context) {
	c.JSON(http.StatusForbidden, gin.H{"code": "FORBIDDEN", "message": "draft owner required", "details": nil, "request_id": requestID(c)})
}

func (s *Services) listMonitorTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"templates": monitorplan.ListTemplates()})
}

func (s *Services) previewMonitorPlan(c *gin.Context) {
	principal := middleware.GetPrincipal(c)
	var req monitorplan.PreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		monitorBadRequest(c, "invalid preview JSON")
		return
	}
	req.TenantID, req.PlanCode = principal.TenantID, principal.PlanCode
	preview, err := s.MonitorPlans.Preview(c.Request.Context(), req)
	if err != nil {
		monitorBadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, preview)
}

type monitorDraftBody struct {
	Name            string                           `json:"name"`
	TemplateID      string                           `json:"template_id"`
	TemplateVersion int                              `json:"template_version"`
	AnalysisType    string                           `json:"analysis_type"`
	Inputs          map[string]string                `json:"inputs"`
	Config          map[string]monitorplan.Candidate `json:"config"`
	State           string                           `json:"state"`
}

func (s *Services) createMonitorPlan(c *gin.Context) {
	principal := middleware.GetPrincipal(c)
	if principal.UserID == "" {
		c.JSON(http.StatusForbidden, gin.H{"code": "FORBIDDEN", "message": "user identity required", "details": nil, "request_id": requestID(c)})
		return
	}
	var body monitorDraftBody
	if err := c.ShouldBindJSON(&body); err != nil {
		monitorBadRequest(c, "invalid draft JSON")
		return
	}
	if body.State != "" && body.State != "draft" {
		monitorBadRequest(c, "only draft state is supported")
		return
	}
	preview, err := s.MonitorPlans.Preview(c.Request.Context(), monitorplan.PreviewRequest{
		TenantID: principal.TenantID, PlanCode: principal.PlanCode, TemplateID: body.TemplateID,
		TemplateVersion: body.TemplateVersion, AnalysisType: body.AnalysisType, Inputs: body.Inputs,
	})
	if err != nil {
		monitorBadRequest(c, err.Error())
		return
	}
	if err := validateMonitorConfig(body.Config, preview.Config); err != nil {
		monitorBadRequest(c, err.Error())
		return
	}
	plan, err := s.MonitorPlans.Create(c.Request.Context(), monitorplan.Plan{
		TenantID: principal.TenantID, OwnerID: principal.UserID, Name: body.Name,
		TemplateID: body.TemplateID, TemplateVersion: body.TemplateVersion, AnalysisType: preview.AnalysisType.AnalysisType,
		Inputs: body.Inputs, Config: body.Config,
	})
	if err != nil {
		monitorBadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusCreated, plan)
}

func validateMonitorConfig(config, preview map[string]monitorplan.Candidate) error {
	if len(config) != len(preview) {
		return fmt.Errorf("seven configuration candidates required")
	}
	for key, candidate := range config {
		base, ok := preview[key]
		if !ok || candidate.State != "proposed" && candidate.State != "unavailable" || candidate.Source == "" || base.State == "unavailable" && candidate.State != "unavailable" {
			return fmt.Errorf("unsupported candidate state or field: %s", key)
		}
		switch key {
		case "keywords":
			words, valid := monitorStringList(candidate.Value)
			if candidate.State != "proposed" || !valid || len(words) == 0 {
				return fmt.Errorf("nonempty keywords required")
			}
			for _, word := range words {
				if strings.TrimSpace(word) == "" {
					return fmt.Errorf("nonempty keywords required")
				}
			}
		case "sources":
			if candidate.Source != base.Source {
				return fmt.Errorf("source provenance must match preview")
			}
			if base.State == "unavailable" {
				if candidate.Value != nil || candidate.Reason != base.Reason {
					return fmt.Errorf("unavailable sources must match preview")
				}
				continue
			}
			authorized, valid := monitorStringList(base.Value)
			selected, selectedValid := monitorStringList(candidate.Value)
			if !valid || !selectedValid || len(selected) == 0 {
				return fmt.Errorf("authorized sources required")
			}
			for _, source := range selected {
				found := false
				for _, allowed := range authorized {
					if source == allowed {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("source not authorized by preview: %s", source)
				}
			}
		}
	}
	return nil
}

func monitorStringList(value any) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return items, true
	case []any:
		words := make([]string, 0, len(items))
		for _, item := range items {
			word, ok := item.(string)
			if !ok {
				return nil, false
			}
			words = append(words, word)
		}
		return words, true
	default:
		return nil, false
	}
}

func monitorPaging(c *gin.Context) (int, int, bool) {
	limit, offset := 20, 0
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			monitorBadRequest(c, "limit must be 1..100")
			return 0, 0, false
		}
		limit = value
	}
	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			monitorBadRequest(c, "offset must be nonnegative")
			return 0, 0, false
		}
		offset = value
	}
	return limit, offset, true
}

func (s *Services) listMonitorPlans(c *gin.Context) {
	limit, offset, ok := monitorPaging(c)
	if !ok {
		return
	}
	plans, err := s.MonitorPlans.List(c.Request.Context(), middleware.GetPrincipal(c).TenantID, limit, offset)
	if err != nil {
		monitorRespondError(c, err)
		return
	}
	if plans == nil {
		plans = []monitorplan.Plan{}
	}
	c.JSON(http.StatusOK, gin.H{"plans": plans, "page_count": len(plans), "limit": limit, "offset": offset})
}

func (s *Services) getMonitorPlan(c *gin.Context) {
	plan, err := s.MonitorPlans.Get(c.Request.Context(), middleware.GetPrincipal(c).TenantID, c.Param("id"))
	if err != nil {
		monitorRespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, plan)
}

func (s *Services) patchMonitorPlan(c *gin.Context) {
	principal := middleware.GetPrincipal(c)
	plan, err := s.MonitorPlans.Get(c.Request.Context(), principal.TenantID, c.Param("id"))
	if err != nil {
		monitorRespondError(c, err)
		return
	}
	if plan.OwnerID != principal.UserID {
		monitorOwnerForbidden(c)
		return
	}
	var body struct {
		Revision     *int                             `json:"revision"`
		Name         *string                          `json:"name"`
		AnalysisType *string                          `json:"analysis_type"`
		Inputs       map[string]string                `json:"inputs"`
		Config       map[string]monitorplan.Candidate `json:"config"`
		State        string                           `json:"state"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		monitorBadRequest(c, "invalid patch JSON")
		return
	}
	if body.Revision == nil || *body.Revision < 1 {
		monitorBadRequest(c, "revision required")
		return
	}
	if body.State != "" && body.State != "draft" {
		monitorBadRequest(c, "only draft state is supported")
		return
	}
	if body.Name != nil {
		plan.Name = strings.TrimSpace(*body.Name)
	}
	if body.AnalysisType != nil {
		plan.AnalysisType = *body.AnalysisType
	}
	if body.Inputs != nil {
		plan.Inputs = body.Inputs
	}
	if body.Config != nil {
		plan.Config = body.Config
	}
	preview, err := s.MonitorPlans.Preview(c.Request.Context(), monitorplan.PreviewRequest{
		TenantID: principal.TenantID, PlanCode: principal.PlanCode, TemplateID: plan.TemplateID,
		TemplateVersion: plan.TemplateVersion, AnalysisType: plan.AnalysisType, Inputs: plan.Inputs,
	})
	if err != nil {
		monitorBadRequest(c, err.Error())
		return
	}
	if err := validateMonitorConfig(plan.Config, preview.Config); err != nil {
		monitorBadRequest(c, err.Error())
		return
	}
	plan.AnalysisType = preview.AnalysisType.AnalysisType
	plan.Revision = *body.Revision
	updated, err := s.MonitorPlans.Update(c.Request.Context(), principal.TenantID, *plan)
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrConflict) || pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			monitorRespondError(c, err)
			return
		}
		monitorBadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Services) deleteMonitorPlan(c *gin.Context) {
	principal := middleware.GetPrincipal(c)
	plan, err := s.MonitorPlans.Get(c.Request.Context(), principal.TenantID, c.Param("id"))
	if err != nil {
		monitorRespondError(c, err)
		return
	}
	if plan.OwnerID != principal.UserID {
		monitorOwnerForbidden(c)
		return
	}
	var body struct {
		Revision *int `json:"revision"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Revision == nil || *body.Revision < 1 {
		monitorBadRequest(c, "revision required")
		return
	}
	if err := s.MonitorPlans.Delete(c.Request.Context(), principal.TenantID, principal.UserID, plan.ID, *body.Revision); err != nil {
		monitorRespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
