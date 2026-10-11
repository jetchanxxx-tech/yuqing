package v1

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	"github.com/yuqing/platform/internal/pkg/storage"
)

func (s *Services) handleUploadAvatar(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	// Bound the whole request, including multipart headers. Streaming multipart
	// avoids temporary-file spooling and unbounded ParseMultipartForm allocations.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, storage.AvatarMaxBytes+(64<<10))
	reader, err := c.Request.MultipartReader()
	if err != nil {
		badRequest(c, "multipart avatar file required")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "avatar" || part.FileName() == "" {
		badRequest(c, "exactly one avatar file required")
		return
	}
	_, params, parseErr := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if parseErr != nil {
		badRequest(c, "invalid avatar filename")
		return
	}
	filename := params["filename"]
	if strings.ContainsAny(filename, "/\\") || strings.Contains(filename, "..") {
		badRequest(c, "invalid avatar filename")
		return
	}
	// Buffer only the bounded encoded upload so extra fields/files are rejected
	// before a successful DB mutation. The adapter independently enforces bounds.
	content, err := io.ReadAll(io.LimitReader(part, storage.AvatarMaxBytes+1))
	if err != nil || len(content) > storage.AvatarMaxBytes {
		badRequest(c, "avatar exceeds 2 MiB")
		return
	}
	if _, err = reader.NextPart(); err != io.EOF {
		badRequest(c, "exactly one avatar file required")
		return
	}
	if err = s.Auth.UpdateAvatar(c.Request.Context(), *p, bytes.NewReader(content)); err != nil {
		respondError(c, err)
		return
	}
	s.handleGetProfile(c)
}
func (s *Services) handleRemoveAvatar(c *gin.Context) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		unauthorized(c)
		return
	}
	// The URL/body cannot select a user or object: only the JWT's current image.
	if c.Request.URL.RawQuery != "" || c.Request.ContentLength > 0 {
		badRequest(c, "avatar removal accepts no target")
		return
	}
	if err := s.Auth.RemoveAvatar(c.Request.Context(), *p); err != nil {
		respondError(c, err)
		return
	}
	s.handleGetProfile(c)
}
func RegisterAvatarRoutes(r *gin.Engine, s *Services) {
	r.GET("/api/v1/avatars/:object", func(c *gin.Context) {
		reader, size, contentType, err := s.Auth.OpenAvatar(c.Request.Context(), storage.AvatarURLPrefix+c.Param("object"))
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer reader.Close()
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
		c.Header("Cache-Control", "no-cache")
		c.DataFromReader(http.StatusOK, size, contentType, reader, nil)
	})
}
