package sms

import (
	"context"
	"fmt"

	sms "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

// TencentProvider 腾讯云短信。
type TencentProvider struct {
	client    *sms.Client
	sdkAppID  string
	signName  string
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
	req := sms.NewSendSmsRequest()
	req.SmsSdkAppId = common.StringPtr(p.sdkAppID)
	req.SignName = common.StringPtr(p.signName)
	req.TemplateId = common.StringPtr(templateID)
	req.PhoneNumberSet = common.StringPtrs([]string{to})

	// 转换为参数数组
	paramArray := make([]string, 0, len(params))
	for _, v := range params {
		paramArray = append(paramArray, v)
	}
	req.TemplateParamSet = common.StringPtrs(paramArray)

	resp, err := p.client.SendSms(req)
	if err != nil {
		return fmt.Errorf("tencent sms send: %w", err)
	}
	if len(resp.Response.SendStatusSet) == 0 {
		return fmt.Errorf("tencent sms: empty response")
	}
	status := resp.Response.SendStatusSet[0]
	if status.Code != nil && *status.Code != "Ok" {
		return fmt.Errorf("tencent sms failed: %s - %s", *status.Code, *status.Message)
	}
	return nil
}
