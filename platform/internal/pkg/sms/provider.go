// Package sms 提供短信发送抽象与实现。
package sms

import (
	"context"
	"fmt"
)

// Provider 定义短信发送接口。
type Provider interface {
	Send(ctx context.Context, to, templateCode string, params map[string]string) error
}

// Config 短信服务配置。
type Config struct {
	Provider string // "aliyun" | "tencent"
	// 阿里云
	AliyunAccessKeyID     string
	AliyunAccessKeySecret string
	AliyunSignName        string
	// 腾讯云
	TencentSecretID  string
	TencentSecretKey string
	TencentSDKAppID  string
	TencentSignName  string
}

// NewProvider 根据配置创建 Provider。
func NewProvider(cfg Config) (Provider, error) {
	switch cfg.Provider {
	case "aliyun":
		return NewAliyunProvider(cfg.AliyunAccessKeyID, cfg.AliyunAccessKeySecret, cfg.AliyunSignName)
	case "tencent":
		return NewTencentProvider(cfg.TencentSecretID, cfg.TencentSecretKey, cfg.TencentSDKAppID, cfg.TencentSignName)
	default:
		return nil, fmt.Errorf("unsupported sms provider: %s", cfg.Provider)
	}
}
