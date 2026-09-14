package payment

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/downloader"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

// WechatProvider 微信 Native 扫码支付（v3）：
//   - 下单 transactions/native → code_url（二维码内容）
//   - 回调 v3 通知：平台证书验签 + AES-GCM 解密 resource
//   - 查单 transactions/out-trade-no
type WechatProvider struct {
	client     *core.Client
	handler    *notify.Handler
	cfg        WechatConfig
	unconfigured bool
}

// NewWechatProvider 构建微信渠道。构建时会向微信下载平台证书（需外网），
// 失败返回错误 —— admin 保存配置时即能发现参数问题。
func NewWechatProvider(cfg WechatConfig) (*WechatProvider, error) {
	if !cfg.Enabled {
		return &WechatProvider{cfg: cfg, unconfigured: true}, nil
	}
	if cfg.AppID == "" || cfg.MchID == "" || cfg.MchSerialNo == "" || cfg.PrivateKey == "" || cfg.APIV3Key == "" {
		return nil, fmt.Errorf("payment: wechat 配置不完整（appid/mch_id/mch_serial_no/private_key/api_v3_key）")
	}
	privKey, err := utils.LoadPrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("payment: wechat 商户私钥无效: %w", err)
	}
	ctx := context.Background()
	client, err := core.NewClient(ctx,
		option.WithWechatPayAutoAuthCipher(cfg.MchID, cfg.MchSerialNo, privKey, cfg.APIV3Key))
	if err != nil {
		return nil, fmt.Errorf("payment: wechat 客户端构建失败: %w", err)
	}
	visitor := downloader.MgrInstance().GetCertificateVisitor(cfg.MchID)
	if visitor == nil {
		return nil, fmt.Errorf("payment: wechat 平台证书未就绪")
	}
	return &WechatProvider{
		client:  client,
		handler: notify.NewNotifyHandler(cfg.APIV3Key, verifiers.NewSHA256WithRSAVerifier(visitor)),
		cfg:     cfg,
	}, nil
}

func (p *WechatProvider) Channel() string  { return ChannelWechat }
func (p *WechatProvider) Configured() bool { return !p.unconfigured && p.client != nil }

func (p *WechatProvider) CreatePayment(ctx context.Context, req *CreatePaymentReq) (*CreatePaymentResp, error) {
	svc := &native.NativeApiService{Client: p.client}
	resp, _, err := svc.Prepay(ctx, native.PrepayRequest{
		Appid:       core.String(p.cfg.AppID),
		Mchid:       core.String(p.cfg.MchID),
		Description: core.String(req.Subject),
		OutTradeNo:  core.String(req.OrderID),
		NotifyUrl:   core.String(p.cfg.NotifyURL),
		Amount:      &native.Amount{Total: core.Int64(int64(req.AmountCents))},
	})
	if err != nil {
		return nil, err
	}
	if resp.CodeUrl == nil {
		return nil, fmt.Errorf("payment: wechat 预下单未返回 code_url")
	}
	return &CreatePaymentResp{QRCodeURL: *resp.CodeUrl}, nil
}

func (p *WechatProvider) VerifyCallback(ctx context.Context, in *CallbackInput) (*CallbackResult, error) {
	// v3 通知体是 JSON + Wechatpay-* 签名头。SDK 从 *http.Request 读头并验签，
	// 用 APIv3 密钥 AES-GCM 解密 resource。重放/伪造/密钥不符都会在验签/解密失败。
	httpReq, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(in.Body))
	if err != nil {
		return nil, err
	}
	for k, v := range in.Header {
		httpReq.Header.Set(k, v)
	}
	txn := new(payments.Transaction)
	if _, err := p.handler.ParseNotifyRequest(ctx, httpReq, txn); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}

	res := &CallbackResult{Success: false}
	if txn.OutTradeNo != nil {
		res.OrderID = *txn.OutTradeNo
	}
	if txn.TransactionId != nil {
		res.ProviderTxnID = *txn.TransactionId
	}
	if txn.TradeState != nil && *txn.TradeState == "SUCCESS" {
		res.Success = true
		if txn.Amount != nil && txn.Amount.PayerTotal != nil {
			// 核验口径用订单总额（ payer_total 含立减优惠时会小于 total）；
			// 商户实收以 total 为准。
			if txn.Amount.Total != nil {
				res.PaidCents = int(*txn.Amount.Total)
			}
		}
	}
	return res, nil
}

func (p *WechatProvider) QueryOrder(ctx context.Context, orderID string) (*QueryResult, error) {
	svc := &native.NativeApiService{Client: p.client}
	resp, _, err := svc.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: core.String(orderID),
		Mchid:      core.String(p.cfg.MchID),
	})
	if err != nil {
		// ORDER_NOT_EXIST：用户还没扫码 → 进行中
		if containsAny(err.Error(), "ORDER_NOT_EXIST", "ORDERNOTEXIST") {
			return &QueryResult{TradeState: StatePending}, nil
		}
		return nil, err
	}
	if resp == nil || resp.TradeState == nil {
		return &QueryResult{TradeState: StatePending}, nil
	}
	switch *resp.TradeState {
	case "SUCCESS":
		res := &QueryResult{TradeState: StatePaid}
		if resp.TransactionId != nil {
			res.ProviderTxnID = *resp.TransactionId
		}
		if resp.Amount != nil && resp.Amount.Total != nil {
			res.PaidCents = int(*resp.Amount.Total)
		}
		return res, nil
	case "CLOSED", "REVOKED", "PAYERROR":
		return &QueryResult{TradeState: StateClosed}, nil
	default:
		return &QueryResult{TradeState: StatePending}, nil
	}
}

func (p *WechatProvider) Ack() (int, string, string) {
	return http.StatusOK, "application/json", `{"code": "SUCCESS", "message": "成功"}`
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && bytes.Contains([]byte(s), []byte(sub)) {
			return true
		}
	}
	return false
}

// 编译期契约检查。
var _ Provider = (*WechatProvider)(nil)
