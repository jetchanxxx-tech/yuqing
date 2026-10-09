package v1

import (
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

func resolveBillingActor(c *gin.Context) (billingpolicy.Actor, error) {
	p := middleware.GetPrincipal(c)
	if p == nil {
		return billingpolicy.Actor{}, pkgerrors.ErrUnauthorized
	}
	if p.AuthType == "api_key" {
		key := middleware.GetAPIKey(c)
		if key == nil || key.CreatorUserID == "" {
			return billingpolicy.Actor{}, pkgerrors.ErrAPIKeyOwnerUnverified
		}
		return billingpolicy.Actor{UserID: key.CreatorUserID, APIKeyID: key.ID}, nil
	}
	if p.AuthType != "jwt" {
		return billingpolicy.Actor{}, pkgerrors.ErrUnauthorized
	}
	version := p.TokenVersion
	return billingpolicy.Actor{UserID: p.UserID, TokenVersion: &version}, nil
}
