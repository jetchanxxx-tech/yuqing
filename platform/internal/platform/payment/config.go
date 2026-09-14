package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

// 平台设置表中的支付渠道配置键（JSON 文本，admin 后台「支付渠道」页写入）。
const (
	SettingsKeyAlipay   = "payment_alipay"
	SettingsKeyWechat   = "payment_wechat"
	SettingsKeyUnionPay = "payment_unionpay"
)

// AlipayConfig 支付宝当面付（扫码）配置。
type AlipayConfig struct {
	AppID           string `json:"app_id"`
	PrivateKey      string `json:"private_key"`        // 应用私钥（PKCS8/PKCS1 文本）
	AlipayPublicKey string `json:"alipay_public_key"`  // 支付宝公钥（回调验签）
	NotifyURL       string `json:"notify_url"`         // 公网回调地址
	Sandbox         bool   `json:"sandbox"`            // true = 沙箱网关
	Enabled         bool   `json:"enabled"`
}

// WechatConfig 微信 Native 扫码支付（v3）配置。
type WechatConfig struct {
	AppID        string `json:"appid"`
	MchID        string `json:"mch_id"`
	MchSerialNo  string `json:"mch_serial_no"`  // 商户 API 证书序列号
	PrivateKey   string `json:"private_key"`    // 商户 API 私钥 PEM 文本
	APIV3Key     string `json:"api_v3_key"`     // APIv3 密钥（回调解密）
	NotifyURL    string `json:"notify_url"`
	Enabled      bool   `json:"enabled"`
}

// UnionPayConfig 银联全渠道网关支付配置（收银台 URL 编成二维码供手机扫描）。
type UnionPayConfig struct {
	MerID            string `json:"mer_id"`
	SignCertPFX      string `json:"sign_cert_pfx"`      // 签名证书 .pfx 的 base64
	SignCertPassword string `json:"sign_cert_password"` // 证书密码
	NotifyURL        string `json:"notify_url"`         // 后台通知地址
	FrontURL         string `json:"front_url"`          // 前台跳转地址（支付完成后浏览器回跳）
	Sandbox          bool   `json:"sandbox"`
	Enabled          bool   `json:"enabled"`
}

// SettingsGetter 是平台设置的只读视图（生产为 settings.Store.Get）。
type SettingsGetter func(ctx context.Context, key string) (string, error)

// Registry 按配置懒构建渠道 provider，并以配置内容哈希做缓存 ——
// admin 在后台改配置（哈希变化）即自动重建，零重启生效。
type Registry struct {
	getSettings SettingsGetter

	mu    sync.Mutex
	cache map[string]cachedProvider
}

type cachedProvider struct {
	configHash string
	provider   Provider
	buildErr   error
}

// NewRegistry 构建渠道注册表。
func NewRegistry(getSettings SettingsGetter) *Registry {
	return &Registry{getSettings: getSettings, cache: map[string]cachedProvider{}}
}

// Resolve 返回当前已配置且启用的渠道集合（未启用的渠道不在其中）。
// 构建失败的渠道跳过并携带错误日志（不阻断其他渠道）。
func (r *Registry) Resolve(ctx context.Context) (map[string]Provider, error) {
	out := map[string]Provider{}
	for channel, key := range map[string]string{
		ChannelAlipay:   SettingsKeyAlipay,
		ChannelWechat:   SettingsKeyWechat,
		ChannelUnionPay: SettingsKeyUnionPay,
	} {
		p, err := r.get(ctx, channel, key)
		if err != nil {
			continue // 配置缺失/无效：渠道不可用（admin 页面会显示原因）
		}
		if p.Configured() {
			out[channel] = p
		}
	}
	return out, nil
}

func (r *Registry) get(ctx context.Context, channel, settingsKey string) (Provider, error) {
	raw, err := r.getSettings(ctx, settingsKey)
	if err != nil || raw == "" {
		return nil, fmt.Errorf("payment: %s 配置为空", channel)
	}
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:8])

	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cache[channel]; ok && c.configHash == hash {
		return c.provider, c.buildErr
	}
	p, buildErr := buildProvider(channel, raw)
	r.cache[channel] = cachedProvider{configHash: hash, provider: p, buildErr: buildErr}
	return p, buildErr
}

// buildProvider 按渠道把配置 JSON 构建成 provider 实例。
func buildProvider(channel, raw string) (Provider, error) {
	switch channel {
	case ChannelAlipay:
		var cfg AlipayConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("payment: alipay 配置解析失败: %w", err)
		}
		return NewAlipayProvider(cfg)
	case ChannelWechat:
		var cfg WechatConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("payment: wechat 配置解析失败: %w", err)
		}
		return NewWechatProvider(cfg)
	case ChannelUnionPay:
		var cfg UnionPayConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("payment: unionpay 配置解析失败: %w", err)
		}
		return NewUnionPayProvider(cfg)
	default:
		return nil, fmt.Errorf("payment: 未知渠道 %q", channel)
	}
}
