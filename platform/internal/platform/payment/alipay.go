package payment

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	alipay "github.com/smartwalle/alipay/v3"
)

// AlipayProvider 支付宝当面付（扫码）：
//   - 下单 alipay.trade.precreate → qr_code（二维码内容）
//   - 回调 alipay 异步通知，SDK 验签（RSA2）
//   - 查单 alipay.trade.query
type AlipayProvider struct {
	client *alipay.Client
	cfg    AlipayConfig
}

// NewAlipayProvider 构建支付宝渠道。密钥无效时返回错误（admin 页面可见）。
func NewAlipayProvider(cfg AlipayConfig) (*AlipayProvider, error) {
	if !cfg.Enabled {
		return &AlipayProvider{cfg: cfg}, nil // 未启用：Configured()=false
	}
	if cfg.AppID == "" || cfg.PrivateKey == "" || cfg.AlipayPublicKey == "" {
		return nil, fmt.Errorf("payment: alipay 配置不完整（app_id/private_key/alipay_public_key）")
	}
	client, err := alipay.New(cfg.AppID, cfg.PrivateKey, !cfg.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("payment: alipay 客户端构建失败: %w", err)
	}
	if err := client.LoadAliPayPublicKey(cfg.AlipayPublicKey); err != nil {
		return nil, fmt.Errorf("payment: alipay 公钥加载失败: %w", err)
	}
	return &AlipayProvider{client: client, cfg: cfg}, nil
}

func (p *AlipayProvider) Channel() string  { return ChannelAlipay }
func (p *AlipayProvider) Configured() bool { return p.client != nil }

func (p *AlipayProvider) CreatePayment(ctx context.Context, req *CreatePaymentReq) (*CreatePaymentResp, error) {
	var param alipay.TradePreCreate
	param.OutTradeNo = req.OrderID
	param.Subject = req.Subject
	param.TotalAmount = centsToYuan(req.AmountCents)
	param.NotifyURL = p.cfg.NotifyURL

	rsp, err := p.client.TradePreCreate(ctx, param)
	if err != nil {
		return nil, err
	}
	if rsp.QRCode == "" {
		return nil, fmt.Errorf("payment: alipay 预下单未返回二维码（%v）", rsp.Error)
	}
	return &CreatePaymentResp{QRCodeURL: rsp.QRCode}, nil
}

func (p *AlipayProvider) VerifyCallback(_ context.Context, in *CallbackInput) (*CallbackResult, error) {
	// 支付宝异步通知是 form-encoded POST；SDK 的 GetTradeNotification
	// 完成验签（RSA2，公钥比对 sign）—— 验签失败返回 error。
	form := url.Values{}
	for k, v := range in.Form {
		form.Set(k, v)
	}
	if form.Get("sign") == "" && len(in.Body) > 0 {
		// 兜底：直接解析 body
		if q, err := url.ParseQuery(string(in.Body)); err == nil {
			form = q
		}
	}
	httpReq, err := http.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	notif, err := p.client.GetTradeNotification(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	res := &CallbackResult{
		OrderID:       notif.OutTradeNo,
		ProviderTxnID: notif.TradeNo,
		Success:       notif.TradeStatus == alipay.TradeStatusSuccess || notif.TradeStatus == alipay.TradeStatusFinished,
	}
	if res.Success {
		res.PaidCents, err = yuanToCents(notif.TotalAmount)
		if err != nil {
			return nil, fmt.Errorf("payment: alipay 通知金额解析失败: %w", err)
		}
	}
	return res, nil
}

func (p *AlipayProvider) QueryOrder(ctx context.Context, orderID string) (*QueryResult, error) {
	var param alipay.TradeQuery
	param.OutTradeNo = orderID

	rsp, err := p.client.TradeQuery(ctx, param)
	if err != nil {
		// 交易不存在（用户从未扫码）：支付宝报 ACQ.TRADE_NOT_EXIST → 视为进行中
		if strings.Contains(err.Error(), "ACQ.TRADE_NOT_EXIST") {
			return &QueryResult{TradeState: StatePending}, nil
		}
		return nil, err
	}
	switch rsp.TradeStatus {
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		cents, cerr := yuanToCents(rsp.TotalAmount)
		if cerr != nil {
			return nil, cerr
		}
		return &QueryResult{TradeState: StatePaid, ProviderTxnID: rsp.TradeNo, PaidCents: cents}, nil
	case alipay.TradeStatusClosed:
		return &QueryResult{TradeState: StateClosed}, nil
	default:
		return &QueryResult{TradeState: StatePending}, nil
	}
}

func (p *AlipayProvider) Ack() (int, string, string) {
	return http.StatusOK, "text/plain", "success"
}

// centsToYuan 99.00 格式（支付宝金额是元的字符串，两位小数）。
func centsToYuan(cents int) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

// yuanToCents "99.00" → 9900。
func yuanToCents(yuan string) (int, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(yuan), 64)
	if err != nil {
		return 0, err
	}
	return int(f*100 + 0.5), nil
}

// 编译期契约检查。
var _ Provider = (*AlipayProvider)(nil)
