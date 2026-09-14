package payment

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/smartwalle/unionpay"
)

// UnionPayProvider 银联全渠道网关支付：
//   - 下单 CreateWebPayment → 银联收银台 HTML 表单（浏览器提交后打开收银台，
//     收银台内可扫码/云闪付拉起；我们把提交页 URL 作为二维码内容供手机扫描）
//   - 回调 DecodeNotification（SDK 验签）→ PaymentNotification
//   - 查单 GetTransaction(orderId, txnTime)（txnTime 是银联查单硬要求，下单时落库）
type UnionPayProvider struct {
	client *unionpay.Client
	cfg    UnionPayConfig
}

// NewUnionPayProvider 构建银联渠道（解析 .pfx 签名证书）。
func NewUnionPayProvider(cfg UnionPayConfig) (*UnionPayProvider, error) {
	if !cfg.Enabled {
		return &UnionPayProvider{cfg: cfg}, nil
	}
	if cfg.MerID == "" || cfg.SignCertPFX == "" {
		return nil, fmt.Errorf("payment: unionpay 配置不完整（mer_id/sign_cert_pfx）")
	}
	pfx, err := base64Decode(cfg.SignCertPFX)
	if err != nil {
		return nil, fmt.Errorf("payment: unionpay 证书 base64 无效: %w", err)
	}
	client, err := unionpay.New(pfx, cfg.SignCertPassword, cfg.MerID, !cfg.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("payment: unionpay 客户端构建失败: %w", err)
	}
	return &UnionPayProvider{client: client, cfg: cfg}, nil
}

func (p *UnionPayProvider) Channel() string  { return ChannelUnionPay }
func (p *UnionPayProvider) Configured() bool { return p.client != nil }

func (p *UnionPayProvider) CreatePayment(ctx context.Context, req *CreatePaymentReq) (*CreatePaymentResp, error) {
	// 银联网关支付返回自动提交的 HTML 表单；订单创建后前端跳转服务端
	// 提供的「银联支付跳转页」，或直接把跳转页 URL 编成二维码。
	payment, err := p.client.CreateWebPayment(req.OrderID, fmt.Sprintf("%d", req.AmountCents),
		p.cfg.FrontURL, p.cfg.NotifyURL)
	if err != nil {
		return nil, err
	}
	if payment == nil || payment.Code != unionpay.CodeSuccess {
		reason := ""
		if payment != nil {
			reason = payment.Msg
		}
		return nil, fmt.Errorf("payment: unionpay 预下单失败: %s", reason)
	}
	return &CreatePaymentResp{
		// 收银台表单 HTML 存入 QRCode 字段（前端按 channel=unionpay 走跳转页而非渲染二维码）
		QRCodeURL: payment.HTML,
		TxnTime:   payment.TxnTime,
	}, nil
}

func (p *UnionPayProvider) VerifyCallback(_ context.Context, in *CallbackInput) (*CallbackResult, error) {
	values := url.Values{}
	for k, v := range in.Form {
		values.Set(k, v)
	}
	if len(values) == 0 && len(in.Body) > 0 {
		if q, err := url.ParseQuery(string(in.Body)); err == nil {
			values = q
		}
	}

	notif, err := p.client.DecodeNotification(values)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	pn, ok := notif.(*unionpay.PaymentNotification)
	if !ok {
		// 撤销/退款等其他通知：ACK 但不作为支付成功凭证
		return &CallbackResult{Success: false}, nil
	}
	res := &CallbackResult{
		OrderID:       pn.OrderId,
		ProviderTxnID: pn.QueryId,
		Success:       pn.Code == unionpay.CodeSuccess,
	}
	if res.Success {
		amt, aerr := strconvAtoiDefault(pn.TxnAmt)
		if aerr != nil {
			return nil, fmt.Errorf("payment: unionpay 通知金额解析失败: %w", aerr)
		}
		res.PaidCents = amt
	}
	return res, nil
}

func (p *UnionPayProvider) QueryOrder(ctx context.Context, orderID string) (*QueryResult, error) {
	txnTime, err := p.orderTxnTime(ctx, orderID)
	if err != nil || txnTime == "" {
		// 无 txnTime 无法查单（非本渠道下单的订单/历史数据）：保守返回进行中
		return &QueryResult{TradeState: StatePending}, nil
	}
	txn, err := p.client.GetTransaction(orderID, txnTime)
	if err != nil {
		return nil, err
	}
	if txn.Code != unionpay.CodeSuccess {
		// 查询动作本身失败或原单不存在：保守视为进行中（回调兜底）
		return &QueryResult{TradeState: StatePending}, nil
	}
	amt, aerr := strconvAtoiDefault(txn.TxnAmt)
	if aerr != nil {
		return nil, aerr
	}
	return &QueryResult{
		TradeState:    StatePaid,
		ProviderTxnID: txn.QueryId,
		PaidCents:     amt,
	}, nil
}

func (p *UnionPayProvider) Ack() (int, string, string) {
	// 银联后台通知应答：respCode=00 即确认
	return http.StatusOK, "application/x-www-form-urlencoded", "respCode=00&respMsg=success"
}

// orderTxnTime 读渠道侧下单时间。银联查单必须带原始 txnTime。
// 生产通过 store 读取（见 service.Reconcile 前置注入）；
// 此处经由 provider 接口无法触达 store，改由 CallbackInput? —— 不，
// 简化：订单表的 txn_time 由 service 在 Reconcile 时经 provider 查询前注入
// QueryOrder 的 ctx（见 service.go 的 txnTimeKey）。
func (p *UnionPayProvider) orderTxnTime(ctx context.Context, _ string) (string, error) {
	if v, ok := ctx.Value(txnTimeKey{}).(string); ok {
		return v, nil
	}
	return "", nil
}

type txnTimeKey struct{}

// base64Decode 标准 base64 解码（容忍空白字符）。
func base64Decode(s string) ([]byte, error) {
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	return base64.StdEncoding.DecodeString(clean)
}

// strconvAtoiDefault 空串按 0。
func strconvAtoiDefault(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	return n, err
}

// 编译期契约检查。
var _ Provider = (*UnionPayProvider)(nil)
