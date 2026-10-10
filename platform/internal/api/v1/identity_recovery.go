package v1

import (
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/auth"
)

var identityPhone = regexp.MustCompile(`^1[3-9]\d{9}$`)

// Public admission uses only the validated socket peer. No trusted proxy is
// configured: forwarded headers and body fields must never select a gate key.
func identityPeer(c *gin.Context) (string, bool) {
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil {
		badRequest(c, "valid socket peer required")
		return "", false
	}
	return ip.String(), true
}
func identityAccepted(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, gin.H{"message": "request accepted; if the account is eligible, a verification message may follow; delivery unconfirmed"})
}
func (s *Services) handlePasswordResetRequest(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Email string `json:"email"`
		Phone string `json:"phone"`
	}
	if c.ShouldBindJSON(&req) != nil {
		badRequest(c, "email or verified phone required")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Phone = strings.TrimSpace(req.Phone)
	purpose, target := auth.PasswordReset, req.Email
	if req.Phone != "" {
		purpose, target = auth.PhoneReset, req.Phone
	}
	if (req.Email == "") == (req.Phone == "") || (req.Email != "" && !emailRe.MatchString(req.Email)) || (req.Phone != "" && !identityPhone.MatchString(req.Phone)) {
		badRequest(c, "provide one valid email or phone")
		return
	}
	ip, ok := identityPeer(c)
	if !ok {
		return
	}
	if err := s.Auth.RequestPublicVerification(c.Request.Context(), purpose, target, ip); err != nil {
		respondError(c, err)
		return
	}
	identityAccepted(c)
}
func (s *Services) handlePasswordResetConfirm(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Token       string `json:"token"`
		Phone       string `json:"phone"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if c.ShouldBindJSON(&req) != nil {
		badRequest(c, "reset credential and new_password required")
		return
	}
	if (req.Token == "") == (req.Phone == "" && req.Code == "") || (req.Token == "" && (!identityPhone.MatchString(req.Phone) || req.Code == "")) {
		badRequest(c, "provide one reset credential")
		return
	}
	ip, ok := identityPeer(c)
	if !ok {
		return
	}
	if err := s.Auth.ResetPassword(c.Request.Context(), req.Token, req.Phone, req.Code, req.NewPassword, ip); err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password reset; please log in again", "requires_relogin": true})
}
func (s *Services) handlePhoneLoginCode(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Phone string `json:"phone"`
	}
	if c.ShouldBindJSON(&req) != nil || !identityPhone.MatchString(strings.TrimSpace(req.Phone)) {
		badRequest(c, "valid phone required")
		return
	}
	ip, ok := identityPeer(c)
	if !ok {
		return
	}
	if err := s.Auth.RequestPublicVerification(c.Request.Context(), auth.PhoneLogin, strings.TrimSpace(req.Phone), ip); err != nil {
		respondError(c, err)
		return
	}
	identityAccepted(c)
}
func (s *Services) handlePhoneLogin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if c.ShouldBindJSON(&req) != nil || !identityPhone.MatchString(req.Phone) || req.Code == "" {
		badRequest(c, "phone and code required")
		return
	}
	p, pair, err := s.Auth.LoginWithPhoneCode(c.Request.Context(), req.Phone, req.Code)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, authResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, User: userFromPrincipal(p)})
}
func (s *Services) handleEmailChangeRequest(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Password string `json:"password"`
		NewEmail string `json:"new_email"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Password == "" || !emailRe.MatchString(strings.ToLower(strings.TrimSpace(req.NewEmail))) {
		badRequest(c, "password and new_email required")
		return
	}
	p := middleware.GetPrincipal(c)
	if p == nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	if err := s.Auth.RequestEmailChange(c.Request.Context(), *p, req.Password, req.NewEmail); err != nil {
		respondIdentityError(c, err)
		return
	}
	identityAccepted(c)
}
func (s *Services) handleEmailChangeConfirm(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Token string `json:"token"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Token == "" {
		badRequest(c, "confirmation token required")
		return
	}
	p := middleware.GetPrincipal(c)
	if p == nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	u, err := s.Auth.ConfirmEmailChange(c.Request.Context(), *p, req.Token)
	if err != nil {
		respondIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": u.Email, "requires_relogin": true, "message": "email changed; please log in again"})
}

// An authenticated password/code rejection is not an expired bearer token.
// Keeping it distinct prevents automatic refresh from replaying a mutation or
// spending a second code attempt. Middleware failures never use this code.
func respondIdentityError(c *gin.Context, err error) {
	if middleware.GetPrincipal(c) != nil && pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "IDENTITY_CHECK_FAILED", "message": "identity check rejected; check the password or verification credential, or log in again", "request_id": requestID(c)})
		return
	}
	respondError(c, err)
}
