package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/api/middleware"
	"github.com/yuging/platform/internal/platform/auth"
)

// RegisterAuthRoutes mounts unauthenticated auth endpoints.
func RegisterAuthRoutes(r *gin.RouterGroup, cfg middleware.AuthConfig) {
	r.POST("/register", handleRegister)
	r.POST("/login", handleLogin)
	r.POST("/refresh", handleRefresh)
	r.POST("/logout", handleLogout)
}

func handleRegister(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "register"})
}

func handleLogin(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "login"})
}

func handleRefresh(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "refresh"})
}

func handleLogout(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "logout"})
}

// helper: respond with JWT token pair.
func respondTokenPair(c *gin.Context, pair *auth.TokenPair) {
	c.JSON(http.StatusOK, pair)
}
