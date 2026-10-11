package v1

// 用户中心 P0 API：修改密码 / 邮箱验证（方案 B 试用 1 次）/ 个人资料 / 手机号绑定。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
)

// RegisterUserCenterRoutes mounts authenticated user-center endpoints.
func RegisterUserCenterRoutes(r *gin.RouterGroup, svcs *Services) {
	r.GET("/user/account-closure/preview", svcs.handleClosurePreview)
	r.GET("/user/account-closure/status", svcs.handleClosureStatus)
	r.POST("/user/account-closure", svcs.handleClosureRequest)
	r.POST("/user/account-closure/cancel", svcs.handleClosureCancel)
	// 账户安全
	r.PUT("/auth/password", svcs.handleChangePassword)
	r.POST("/auth/send-verification-email", svcs.handleSendVerificationEmail)

	r.POST("/user/email-change/request", svcs.handleEmailChangeRequest)
	r.POST("/user/email-change/confirm", svcs.handleEmailChangeConfirm)

	// 个人资料
	r.POST("/user/avatar", svcs.handleUploadAvatar)
	r.DELETE("/user/avatar", svcs.handleRemoveAvatar)
	r.GET("/user/profile", svcs.handleGetProfile)
	r.PUT("/user/profile", svcs.handleUpdateProfile)

	// 手机号绑定
	r.POST("/user/phone/send-code", svcs.handleSendPhoneCode)
	r.POST("/user/phone/bind", svcs.handleBindPhone)
	r.POST("/user/phone/unbind", svcs.handleUnbindPhone)
}

// RegisterUserCenterPublicRoutes mounts the email-link verification endpoint
// (public: 用户点击邮件里的链接时未携带 Authorization 头).
func RegisterUserCenterPublicRoutes(r *gin.RouterGroup, svcs *Services) {
	r.POST("/verify-email", svcs.handleVerifyEmail)
	r.GET("/verify-email", func(c *gin.Context) {
		badRequest(c, "legacy verification links expired; request a new verification email")
	})
}

// principalUserID 返回当前登录用户 ID，缺principal时返回空串（已写401）。
func principalUserID(c *gin.Context) string {
	p := middleware.GetPrincipal(c)
	if p == nil {
		return ""
	}
	return p.UserID
}

func requireUserID(c *gin.Context) (string, bool) {
	id := principalUserID(c)
	if id == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "UNAUTHORIZED", "message": "authentication required", "request_id": requestID(c)})
		return "", false
	}
	return id, true
}

// handleChangePassword PUT /auth/password {old_password, new_password}
// 成功后客户端必须丢弃本地 token 重新登录（MVP 无服务端会话表可撤销）。
func (s *Services) handleChangePassword(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OldPassword == "" || req.NewPassword == "" {
		badRequest(c, "request body must be JSON {old_password, new_password}")
		return
	}
	if err := s.Auth.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword, middleware.GetPrincipal(c).TokenVersion); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password changed, please log in again"})
}

// handleSendVerificationEmail POST /auth/send-verification-email
// 验证链接基地址优先取组合根注入的 YUQING_PUBLIC_BASE_URL（防 Host 头伪造）；
// 未配置时失败，不使用请求 Host 或代理头。
func (s *Services) handleSendVerificationEmail(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}

	if err := s.Auth.SendVerificationEmail(c.Request.Context(), userID, s.Auth.VerifyBaseURL(), middleware.GetPrincipal(c).TokenVersion); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "verification email accepted; delivery unconfirmed"})
}

// handleVerifyEmail accepts bearer credentials only in the POST body.
func (s *Services) handleVerifyEmail(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		badRequest(c, "verification token is required")
		return
	}
	token := req.Token
	if err := s.Auth.VerifyEmail(c.Request.Context(), token); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "email verified successfully"})
}

// handleGetProfile GET /user/profile
func (s *Services) handleGetProfile(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	profile, err := s.Auth.GetProfile(c.Request.Context(), userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, profile)
}

// handleUpdateProfile PUT /user/profile {name, timezone}
func (s *Services) handleUpdateProfile(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "request body must be JSON {name, timezone}")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		badRequest(c, "name is required")
		return
	}
	if err := s.Auth.UpdateProfile(c.Request.Context(), userID, req.Name, req.Timezone, middleware.GetPrincipal(c).TokenVersion); err != nil {
		respondError(c, err)
		return
	}
	profile, err := s.Auth.GetProfile(c.Request.Context(), userID)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, profile)
}

// handleSendPhoneCode POST /user/phone/send-code {phone}
func (s *Services) handleSendPhoneCode(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Phone) == "" {
		badRequest(c, "request body must be JSON {phone}")
		return
	}
	if err := s.Auth.SendPhoneCodeWithPassword(c.Request.Context(), userID, strings.TrimSpace(req.Phone), req.Password, middleware.GetPrincipal(c).TokenVersion); err != nil {
		respondIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "verification code accepted; delivery unconfirmed", "expires_in": 300})
}

// handleBindPhone POST /user/phone/bind {phone, code}
func (s *Services) handleBindPhone(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Phone == "" || req.Code == "" {
		badRequest(c, "request body must be JSON {phone, code}")
		return
	}
	if err := s.Auth.BindPhone(c.Request.Context(), userID, req.Phone, req.Code, middleware.GetPrincipal(c).TokenVersion); err != nil {
		respondIdentityError(c, err)
		return
	}
	profile, err := s.Auth.GetProfile(c.Request.Context(), userID)
	if err != nil {
		respondIdentityError(c, err)
		return
	}
	profile["requires_relogin"] = true
	c.JSON(http.StatusOK, profile)
}

// handleUnbindPhone POST /user/phone/unbind {password}
func (s *Services) handleUnbindPhone(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		badRequest(c, "request body must be JSON {password}")
		return
	}
	if err := s.Auth.UnbindPhone(c.Request.Context(), userID, req.Password, middleware.GetPrincipal(c).TokenVersion); err != nil {
		respondIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "phone unbound; please log in again"})
}
