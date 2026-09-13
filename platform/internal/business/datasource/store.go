// Package datasource manages platform-configured data sources.
package datasource

import "context"

// Store defines the data source persistence contract.
type Store interface {
	List(ctx context.Context, tenantID string) ([]Source, error)
	Get(ctx context.Context, tenantID, sourceID string) (*Source, error)
}

// Source represents a platform-managed data source.
type Source struct {
	ID         string `json:"id"`
	Type       string `json:"type"` // weibo, wechat, news, xiaohongshu, bilibili, rss
	Name       string `json:"name"`
	ConfigJSON string `json:"config_json"`
	Enabled    bool   `json:"enabled"`
}

// Types is the list of supported data source types.
var Types = []string{"weibo", "wechat", "news", "xiaohongshu", "bilibili", "douyin", "kuaishou", "zhihu", "rss", "custom_web"}
