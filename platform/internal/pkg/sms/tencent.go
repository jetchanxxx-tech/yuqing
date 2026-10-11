package sms

import (
	"context"
	"fmt"
	"github.com/yuqing/platform/internal/pkg/notification"
	"time"

	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	sms "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"
)

// TencentProvider 腾讯云短信。
type TencentProvider struct {
	client   *sms.Client
	sdkAppID string
	signName string
}

// NewTencentProvider 创建腾讯云短信 Provider。
func NewTencentProvider(secretID, secretKey, sdkAppID, signName string) (*TencentProvider, error) {
	credential := common.NewCredential(secretID, secretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = "sms.tencentcloudapi.com"
	client, err := sms.NewClient(credential, "ap-guangzhou", cpf)
	if err != nil {
		return nil, fmt.Errorf("create tencent sms client: %w", err)
	}
	return &TencentProvider{client: client, sdkAppID: sdkAppID, signName: signName}, nil
}

// Send 发送短信。
func (p *TencentProvider) Send(ctx context.Context, to, templateID string, params map[string]string) error {
	_, err := p.SendReceipt(ctx, to, templateID, params)
	return err
}
func (p *TencentProvider) SendReceipt(ctx context.Context, to, templateID string, params map[string]string) (notification.Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return notification.Receipt{}, err
	}
	// All current verification templates have exactly one positional code.
	if len(params) != 1 || params["code"] == "" {
		return notification.Receipt{}, fmt.Errorf("invalid SMS template parameters")
	}
	req := sms.NewSendSmsRequest()
	req.SmsSdkAppId = common.StringPtr(p.sdkAppID)
	req.SignName = common.StringPtr(p.signName)
	req.TemplateId = common.StringPtr(templateID)
	req.PhoneNumberSet = common.StringPtrs([]string{to})
	req.TemplateParamSet = common.StringPtrs([]string{params["code"]})
	resp, err := p.client.SendSmsWithContext(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return notification.Receipt{}, ctx.Err()
		}
		return notification.Receipt{}, fmt.Errorf("tencent delivery was not accepted")
	}
	if resp == nil || resp.Response == nil || len(resp.Response.SendStatusSet) != 1 {
		return notification.Receipt{}, fmt.Errorf("tencent acceptance receipt missing")
	}
	st := resp.Response.SendStatusSet[0]
	if st == nil || st.Code == nil || *st.Code != "Ok" || st.SerialNo == nil || *st.SerialNo == "" {
		return notification.Receipt{}, fmt.Errorf("tencent acceptance receipt missing or rejected")
	}
	return notification.Accepted("tencent", *st.SerialNo), nil
}
