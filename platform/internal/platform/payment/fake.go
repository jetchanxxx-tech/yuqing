package payment

import (
	"context"
)

// FakeProvider 是测试替身（也用于「渠道未配置」时的占位——绝不 Configured）。
// 预设回调/查单结果，验证订单核验的全部防线。
type FakeProvider struct {
	ChannelName    string
	ConfiguredFlag bool

	// CreateResp 预下单返回；CreateErr 非空则 CreatePayment 报错。
	CreateResp *CreatePaymentResp
	CreateErr  error

	// CallbackResult 预设验签结果；CallbackErr 非空模拟验签失败。
	CallbackResult *CallbackResult
	CallbackErr    error

	// QueryResult 预设查单结果；QueryErr 非空模拟查单失败。
	QueryResult *QueryResult
	QueryErr    error

	Created []*CreatePaymentReq
}

// NewFakeProvider 构造一个已配置的假渠道。
func NewFakeProvider(channel string) *FakeProvider {
	return &FakeProvider{
		ChannelName:    channel,
		ConfiguredFlag: true,
		CreateResp:     &CreatePaymentResp{QRCodeURL: "https://qr.example/" + channel, ProviderTxnID: "prov-" + channel},
	}
}

func (f *FakeProvider) Channel() string      { return f.ChannelName }
func (f *FakeProvider) Configured() bool     { return f.ConfiguredFlag }

func (f *FakeProvider) CreatePayment(_ context.Context, req *CreatePaymentReq) (*CreatePaymentResp, error) {
	if f.CreateErr != nil {
		return nil, f.CreateErr
	}
	f.Created = append(f.Created, req)
	return f.CreateResp, nil
}

func (f *FakeProvider) VerifyCallback(_ context.Context, _ *CallbackInput) (*CallbackResult, error) {
	if f.CallbackErr != nil {
		return nil, f.CallbackErr
	}
	return f.CallbackResult, nil
}

func (f *FakeProvider) QueryOrder(_ context.Context, _ string) (*QueryResult, error) {
	if f.QueryErr != nil {
		return nil, f.QueryErr
	}
	return f.QueryResult, nil
}

func (f *FakeProvider) Ack() (int, string, string) { return 200, "text/plain", "success" }

// 编译期契约检查。
var _ Provider = (*FakeProvider)(nil)
