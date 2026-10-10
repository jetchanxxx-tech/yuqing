package v1

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/platform/auth"
)

// emailRe is a deliberately simple structural email check — mirrors the
// service-side validation so malformed DTOs answer 400 before reaching it.
var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

// RegisterAuthRoutes mounts the unauthenticated auth endpoints (register,
// login, refresh). /me and /logout require an access token and are mounted
// inside the authenticated group via RegisterSessionRoutes.
func RegisterAuthRoutes(r *gin.RouterGroup, svcs *Services) {
	r.POST("/register", svcs.handleRegister)
	r.POST("/login", svcs.handleLogin)
	r.POST("/refresh", svcs.handleRefresh)
	r.POST("/password-reset/request", svcs.handlePasswordResetRequest)
	r.POST("/password-reset/confirm", svcs.handlePasswordResetConfirm)
	r.POST("/phone/send-code", svcs.handlePhoneLoginCode)
	r.POST("/phone/login", svcs.handlePhoneLogin)
}

// RegisterSessionRoutes mounts the authenticated session endpoints (GET /me,
// POST /logout) on a group already behind AuthRequired.
func RegisterSessionRoutes(r *gin.RouterGroup, svcs *Services) {
	r.GET("/me", svcs.handleMe)
	r.POST("/logout", svcs.handleLogout)
}

// userDTO is the wire shape frontend stores as the session principal
// (see Principal in web/src/stores/auth.tsx).
type userDTO struct {
	Name         string   `json:"name"`
	Timezone     string   `json:"timezone"`
	AvatarURL    string   `json:"avatar_url"`
	UserID       string   `json:"user_id"`
	TenantID     string   `json:"tenant_id"`
	Email        string   `json:"email"`
	Roles        []string `json:"roles"`
	PlanCode     string   `json:"plan_code"`
	PlanStatus   string   `json:"plan_status"`
	PlanSource   string   `json:"plan_source"`
	TenantStatus string   `json:"tenant_status"`
}

func userFromPrincipal(p *auth.Principal) userDTO {
	return userDTO{
		UserID:   p.UserID,
		TenantID: p.TenantID,
		Email:    p.Email,
		Roles:    p.Roles,
		PlanCode: p.PlanCode, PlanStatus: p.PlanStatus, PlanSource: p.PlanSource,
		TenantStatus: p.TenantStatus,
	}
}

// authResponse is the register/login payload: a token pair plus the session
// user, exactly what web/src/stores/auth.tsx destructures.
type authResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	User         userDTO `json:"user"`
}

// tokenResponse is the refresh payload: a fresh pair, no user (the frontend
// axios interceptor only reads the tokens).
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// handleRegister provisions a user + tenant + membership and issues a pair.
func (s *Services) handleRegister(c *gin.Context) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "request body must be JSON {email, password, name}")
		return
	}
	// Thin DTO validation — 400 before the service.
	if !emailRe.MatchString(strings.ToLower(strings.TrimSpace(req.Email))) {
		badRequest(c, "invalid email format")
		return
	}
	if len(req.Password) < 8 {
		badRequest(c, "password must be at least 8 characters")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		badRequest(c, "name is required")
		return
	}

	p, pair, err := s.Auth.Register(c.Request.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, authResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		User:         userFromPrincipal(p),
	})
}

// handleLogin verifies credentials and issues a fresh token pair.
func (s *Services) handleLogin(c *gin.Context) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "request body must be JSON {email, password}")
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		badRequest(c, "email and password are required")
		return
	}

	p, pair, err := s.Auth.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, authResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		User:         userFromPrincipal(p),
	})
}

// handleRefresh reissues a token pair from a valid refresh token.
func (s *Services) handleRefresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.RefreshToken) == "" {
		badRequest(c, "request body must be JSON {refresh_token}")
		return
	}

	pair, err := s.Auth.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
	})
}

// handleMe returns the session principal decoded from the access token.
// Mounted behind AuthRequired.
func (s *Services) handleMe(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "UNAUTHORIZED", "message": "authentication required"})
		return
	}
	profile, err := s.Auth.GetProfile(c.Request.Context(), p.UserID)
	if err != nil {
		respondError(c, err)
		return
	}
	result := userFromPrincipal(p)
	result.Name, _ = profile["name"].(string)
	result.Timezone, _ = profile["timezone"].(string)
	result.AvatarURL, _ = profile["avatar_url"].(string)
	c.JSON(http.StatusOK, result)
}

// handleLogout is a stateless-JWT no-op: the client discards its local token
// pair. A future session store can revoke tokens here. Mounted behind
// AuthRequired so a missing/invalid token is rejected with 401.
func (s *Services) handleLogout(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// badRequest writes the standard 400 envelope.
func badRequest(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"code":       "BAD_REQUEST",
		"message":    message,
		"request_id": requestID(c),
	})
}
