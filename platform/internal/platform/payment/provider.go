// Package payment 实现收费体系的支付层：多渠道二维码扫码支付（支付宝/微信/银联）
// + 订单核验。资金安全三防（用户明确要求的验收口径）：
//
//  1. 防重复入账 —— 回调验签 + provider_txn_id 全局唯一 + pending→paid 原子跃迁，
//     额度发放按 order_id 幂等（credit 层唯一索引兜底）。
//  2. 防付款未到账 —— 回调丢失时前端轮询触发主动查单（Reconcile），
//     渠道侧已支付的订单走同一条核验入账路径自愈。
//  3. 防回调空挡刷单 —— 所有入账只信「验签通过 + 金额一致 + 状态跃迁成功」，
//     绝不信前端或未验签报文；渠道流水号冲突的订单拒绝入账。
package payment

import (
	"context"
	"errors"
	"time"
)

// 支持的支付渠道标识。
const (
	ChannelAlipay   = "alipay"
	ChannelWechat   = "wechat"
	ChannelUnionPay = "unionpay"
)

// OrderState 订单状态机：pending → paid | closed | refund_needed。
const (
	StatePending      = "pending"
	StatePaid         = "paid"
	StateClosed       = "closed"        // 渠道侧关闭/用户放弃
	StateRefundNeeded = "refund_needed" // 钱已付但订单无法正常发放（人工介入退款）
)

// Order 是一笔支付订单。
type Order struct {
	ID            string     `json:"id"` // 商户订单号（out_trade_no）
	TenantID      string     `json:"tenant_id"`
	SKUCode       string     `json:"sku_code"`
	Kind          string     `json:"kind"` // plan | addon
	Credits       int        `json:"credits"`
	AmountCents   int        `json:"amount_cents"`
	Channel       string     `json:"channel"`
	State         string     `json:"state"`
	ProviderTxnID string     `json:"provider_txn_id,omitempty"`
	QRCodeURL     string     `json:"qr_code_url,omitempty"` // 二维码内容（前端渲染成图）
	TxnTime       string     `json:"-"`                     // 银联查单必需（下单时间 yyMMddHHmmss）
	Granted       bool       `json:"granted"`
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CreatePaymentReq 是发起扫码支付的入参。
type CreatePaymentReq struct {
	OrderID     string // 商户订单号
	Subject     string // 商品名（账单展示）
	AmountCents int
	NotifyURL   string // 回调地址（公网可达）
}

// CreatePaymentResp 是渠道预下单结果：二维码内容 + 渠道单号。
type CreatePaymentResp struct {
	QRCodeURL     string
	ProviderTxnID string
	// TxnTime 银联专用（下单时间，查单必带）；其他渠道为空。
	TxnTime string
}

// CallbackInput 是渠道回调的原始报文（验签前的全部素材）。
type CallbackInput struct {
	Body   []byte            // 原始 body（微信 JSON / 银联表单）
	Form   map[string]string // 表单参数（支付宝 notify）
	Header map[string]string // 回执头（微信验签字段）
	Query  map[string]string // URL 参数
}

// CallbackResult 是验签后的回调语义。
type CallbackResult struct {
	OrderID       string // 商户订单号
	ProviderTxnID string // 渠道流水号
	PaidCents     int    // 渠道侧实付金额（分）
	Success       bool   // 交易是否成功（false = 中间态通知，ACK 后忽略）
}

// QueryResult 是主动查单结果。
type QueryResult struct {
	TradeState    string // StatePaid | StatePending | StateClosed
	ProviderTxnID string
	PaidCents     int
}

// Provider 是支付渠道契约。三个实现：alipay / wechat / unionpay；
// fake.go 提供测试替身。验签失败必须返回 error —— 这是防线 1。
type Provider interface {
	Channel() string
	// Configured 报告渠道配置是否就绪（未配置的渠道不出现在可购列表）。
	Configured() bool
	CreatePayment(ctx context.Context, req *CreatePaymentReq) (*CreatePaymentResp, error)
	VerifyCallback(ctx context.Context, in *CallbackInput) (*CallbackResult, error)
	QueryOrder(ctx context.Context, orderID string) (*QueryResult, error)
	// Ack 返回给渠道的应答（支付宝 "success" 纯文本 / 微信 JSON / 银联同支付宝）。
	Ack() (status int, contentType, body string)
}

// ErrNotConfigured 渠道未配置。
var ErrNotConfigured = errors.New("payment: 渠道未配置")

// ErrNotFound 订单不存在（API 层映射 404）。
var ErrNotFound = errors.New("payment: 订单不存在")

// ErrBadSignature 验签失败（防线 1 触发）。
var ErrBadSignature = errors.New("payment: 回调验签失败")

// ErrAmountMismatch 金额不符（防线 2 触发，绝不入账）。
var ErrAmountMismatch = errors.New("payment: 回调金额与订单不符")
