package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterTrendsRoutes mounts the hot-topics endpoints（F21 热榜聚合页）.
func RegisterTrendsRoutes(r *gin.RouterGroup, svcs *Services) {
	trendsGroup := r.Group("/trends")
	trendsGroup.GET("", svcs.handleGetTrends)
}

// handleGetTrends 返回全部平台的热榜快照（纯内存，无租户维度）。
// Trends 服务未配置（未设 rsshub_base）→ 503；RSSHub 挂了 → 仍 200，
// 各平台以 stale/error 态表达（错误不是服务器故障，是数据源状态）。
func (s *Services) handleGetTrends(c *gin.Context) {
	if s.Trends == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":       "TRENDS_UNAVAILABLE",
			"message":    "热榜服务未配置（rsshub_base 为空）",
			"request_id": requestID(c),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"platforms": s.Trends.GetAll(c.Request.Context())})
}
