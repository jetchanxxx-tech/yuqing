package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/yuqing/platform/internal/pkg/notification"
	"net/http"
	"time"

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

// contextAliyunClient attaches cancellation to the pinned SDK's HTTP seam.
type contextAliyunClient struct {
	ctx    context.Context
	client dara.HttpClient
}

func (c contextAliyunClient) Call(r *http.Request, transport *http.Transport) (*http.Response, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	r = r.WithContext(c.ctx)
	if c.client != nil {
		return c.client.Call(r, transport)
	}
	return (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(r)
}
func (p *AliyunProvider) Send(ctx context.Context, to, template string, params map[string]string) error {
	_, err := p.SendReceipt(ctx, to, template, params)
	return err
}
func (p *AliyunProvider) SendReceipt(ctx context.Context, to, template string, params map[string]string) (notification.Receipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return notification.Receipt{}, err
	}
	req := &dysmsapi.SendSmsRequest{PhoneNumbers: tea.String(to), SignName: tea.String(p.signName), TemplateCode: tea.String(template), TemplateParam: tea.String(mapToJSON(params))}
	// Clone both structs: dynamic senders and concurrent calls never replace a
	// shared SDK transport with a different caller's context.
	client := *p.client
	client.HttpClient = contextAliyunClient{ctx: ctx, client: p.client.HttpClient}
	resp, err := client.SendSms(req)
	if err != nil {
		if ctx.Err() != nil {
			return notification.Receipt{}, ctx.Err()
		}
		return notification.Receipt{}, fmt.Errorf("aliyun delivery was not accepted")
	}
	if resp == nil || resp.Body == nil || tea.StringValue(resp.Body.Code) != "OK" || tea.StringValue(resp.Body.BizId) == "" || tea.StringValue(resp.Body.RequestId) == "" {
		return notification.Receipt{}, fmt.Errorf("aliyun acceptance receipt missing or rejected")
	}
	return notification.Accepted("aliyun", tea.StringValue(resp.Body.BizId)), nil
}
func mapToJSON(m map[string]string) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}
