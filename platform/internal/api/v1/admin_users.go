package v1

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/platform/accountadmin"
	"github.com/yuqing/platform/internal/platform/billing"
	"github.com/yuqing/platform/internal/platform/tenant"
)

func adminBadRequest(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"code": "BAD_REQUEST", "message": "invalid administrative input", "request_id": requestID(c)})
}

func adminQuery(c *gin.Context, users bool) (accountadmin.Query, bool) {
	q := accountadmin.Query{Page: 1, PageSize: 20, Q: strings.TrimSpace(c.Query("q")), Status: c.Query("status"), PlatformRole: c.Query("platform_role"), Verified: c.Query("verified"), PlanCode: c.Query("plan_code")}
	for name, target := range map[string]*int{"page": &q.Page, "page_size": &q.PageSize} {
		if values, present := c.Request.URL.Query()[name]; present {
			if len(values) != 1 {
				adminBadRequest(c)
				return q, false
			}
			v, err := strconv.Atoi(values[0])
			if err != nil || v < 1 {
				adminBadRequest(c)
				return q, false
			}
			*target = v
		}
	}
	if q.PageSize > 100 {
		adminBadRequest(c)
		return q, false
	}
	valid := true
	if users {
		if q.Status != "" {
			switch q.Status {
			case "active", "disabled", "pending_activation", "closure_pending", "closed":
			default:
				valid = false
			}
		}
		if q.PlatformRole != "" && q.PlatformRole != "platform_admin" {
			valid = false
		}
		if q.Verified != "" && q.Verified != "email" && q.Verified != "phone" && q.Verified != "none" {
			valid = false
		}
	} else {
		if q.Status != "" && !tenant.ValidStatus(q.Status) {
			valid = false
		}
		if q.PlanCode != "" && billing.DefaultPlans()[q.PlanCode] == nil {
			valid = false
		}
	}
	if !valid {
		adminBadRequest(c)
		return q, false
	}
	return q, true
}

func (s *Services) handleListAdminUsers(c *gin.Context) {
	q, ok := adminQuery(c, true)
	if !ok {
		return
	}
	items, total, err := s.AccountAdmin.ListUsers(c.Request.Context(), q)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": q.Page, "page_size": q.PageSize})
}
func (s *Services) handleAdminUser(c *gin.Context) {
	result, err := s.AccountAdmin.User(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func (s *Services) handleAdminTenant(c *gin.Context) {
	result, err := s.AccountAdmin.Tenant(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

type adminStatusRequest struct {
	Reason          string `json:"reason"`
	ExpectedVersion *int64 `json:"expected_version"`
}
type adminPlatformRoleRequest struct {
	adminStatusRequest
	PlatformAdmin *bool `json:"platform_admin"`
}
type adminMemberRoleRequest struct {
	adminStatusRequest
	Role string `json:"role"`
}

// decodeAdmin accepts one JSON object, rejects unknown properties/trailing
// documents and limits the body. Pointer DTO fields distinguish absent/null
// intent from an explicitly supplied false or zero.
func decodeAdmin(c *gin.Context, req any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(req); err != nil {
		adminBadRequest(c)
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		adminBadRequest(c)
		return false
	}
	return true
}

func validAdminStatus(c *gin.Context, req adminStatusRequest) bool {
	if req.ExpectedVersion == nil || *req.ExpectedVersion < 0 || strings.TrimSpace(req.Reason) == "" {
		adminBadRequest(c)
		return false
	}
	return true
}

func adminMutation(c *gin.Context, req adminStatusRequest, action string) accountadmin.Mutation {
	p := middleware.GetPrincipal(c)
	return accountadmin.Mutation{ActorID: p.UserID, TargetID: c.Param("id"), Action: action, Reason: strings.TrimSpace(req.Reason), ExpectedVersion: *req.ExpectedVersion, RequestID: requestID(c)}
}
func (s *Services) respondAdminMutation(c *gin.Context, m accountadmin.Mutation) {
	result, err := s.AccountAdmin.Change(c.Request.Context(), m)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func (s *Services) changeAdminUserStatus(c *gin.Context, action string) {
	var req adminStatusRequest
	if !decodeAdmin(c, &req) || !validAdminStatus(c, req) {
		return
	}
	s.respondAdminMutation(c, adminMutation(c, req, action))
}
func (s *Services) handleDisableAdminUser(c *gin.Context) { s.changeAdminUserStatus(c, "user.disable") }
func (s *Services) handleEnableAdminUser(c *gin.Context)  { s.changeAdminUserStatus(c, "user.enable") }
func (s *Services) handleAdminPlatformRole(c *gin.Context) {
	var req adminPlatformRoleRequest
	if !decodeAdmin(c, &req) || !validAdminStatus(c, req.adminStatusRequest) {
		return
	}
	if req.PlatformAdmin == nil {
		adminBadRequest(c)
		return
	}
	m := adminMutation(c, req.adminStatusRequest, "user.platform_role")
	m.PlatformAdmin = *req.PlatformAdmin
	s.respondAdminMutation(c, m)
}
func (s *Services) handleAdminMemberRole(c *gin.Context) {
	var req adminMemberRoleRequest
	if !decodeAdmin(c, &req) || !validAdminStatus(c, req.adminStatusRequest) {
		return
	}
	if req.Role != "tenant_admin" && req.Role != "analyst" && req.Role != "viewer" {
		adminBadRequest(c)
		return
	}
	m := adminMutation(c, req.adminStatusRequest, "member.role")
	m.TenantID = c.Param("id")
	m.TargetID = c.Param("user_id")
	m.Role = req.Role
	s.respondAdminMutation(c, m)
}
