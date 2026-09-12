package v1

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuging/platform/internal/api/middleware"
	pkgerrors "github.com/yuging/platform/internal/pkg/errors"
	"github.com/yuging/platform/internal/platform/apikey"
)

// RegisterAPIKeyRoutes mounts the tenant API key lifecycle endpoints.
// Key management is an admin-grade capability (apikeys:manage); the created
// keys themselves authenticate via middleware.ApiKeyAuth / AuthAny.
func RegisterAPIKeyRoutes(r *gin.RouterGroup, svcs *Services) {
	keys := r.Group("/apikeys", middleware.RequirePermission("apikeys:manage"))
	keys.POST("", svcs.handleCreateAPIKey)
	keys.GET("", svcs.handleListAPIKeys)
	keys.DELETE("/:id", svcs.handleRevokeAPIKey)
}

// createAPIKeyRequest is the POST /apikeys body.
type createAPIKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

// createdAPIKeyResponse embeds the metadata and carries the raw key —
// the only response ever that contains it.
type createdAPIKeyResponse struct {
	*apikey.APIKey
	RawKey string `json:"api_key"`
}

// handleCreateAPIKey mints a tenant API key and returns the secret once.
func (s *Services) handleCreateAPIKey(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	var req createAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid JSON body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		badRequest(c, "name is required")
		return
	}
	key, raw, err := s.APIKey.CreateKey(c.Request.Context(), p.TenantID, req.Name, req.Scopes)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, createdAPIKeyResponse{APIKey: key, RawKey: raw})
}

// handleListAPIKeys lists the tenant's key metadata. Raw keys and hashes are
// structurally absent from apikey.APIKey's JSON, so nothing can leak here.
func (s *Services) handleListAPIKeys(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	keys, err := s.APIKey.ListKeys(c.Request.Context(), p.TenantID)
	if err != nil {
		respondError(c, err)
		return
	}
	if keys == nil {
		keys = []*apikey.APIKey{}
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys, "total": len(keys)})
}

// handleRevokeAPIKey permanently disables a key. Idempotent DELETE → 204.
// Foreign or unknown ids are 404 (tenant-scoped store).
func (s *Services) handleRevokeAPIKey(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	if err := s.APIKey.RevokeKey(c.Request.Context(), p.TenantID, c.Param("id")); err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			notFound(c, "api key not found")
			return
		}
		respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
