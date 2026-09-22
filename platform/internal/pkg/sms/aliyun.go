package sms

import (
	"context"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	dysmsapi "github.com/alibabacloud-go/dysmsapi-20170525/v3/client"
	"github.com/alibabacloud-go/tea/tea"
)

// AliyunProvider 阿里云短信。
type AliyunProvider struct {
	client   *dysmsapi.Client
	signName string
}

// NewAliyunProvider 创建阿里云短信 Provider。
func NewAliyunProvider(accessKeyID, accessKeySecret, signName string) (*AliyunProvider, error) {
	config := &openapi.Config{
		AccessKeyId:     tea.String(accessKeyID),
		AccessKeySecret: tea.String(accessKeySecret),
		Endpoint:        tea.String("dysmsapi.aliyuncs.com"),
	}
	client, err := dysmsapi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create aliyun sms client: %w", err)
	}
	return &AliyunProvider{client: client, signName: signName}, nil
}

// Send 发送短信。
func (p *AliyunProvider) Send(ctx context.Context, to, templateCode string, params map[string]string) error {
	req := &dysmsapi.SendSmsRequest{
		PhoneNumbers:  tea.String(to),
		SignName:      tea.String(p.signName),
		TemplateCode:  tea.String(templateCode),
		TemplateParam: tea.String(mapToJSON(params)),
	}
	resp, err := p.client.SendSms(req)
	if err != nil {
		return fmt.Errorf("aliyun sms send: %w", err)
	}
	if resp.Body.Code != nil && *resp.Body.Code != "OK" {
		return fmt.Errorf("aliyun sms failed: %s - %s", tea.StringValue(resp.Body.Code), tea.StringValue(resp.Body.Message))
	}
	return nil
}

func mapToJSON(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	s := "{"
	first := true
	for k, v := range m {
		if !first {
			s += ","
		}
		s += fmt.Sprintf(`"%s":"%s"`, k, v)
		first = false
	}
	s += "}"
	return s
}
