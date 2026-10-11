package sms

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type tencentTransport func(*http.Request) (*http.Response, error)

func (f tencentTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTencentSandboxReceiptAndRejection(t *testing.T) {
	for _, body := range []string{`{"Response":{"RequestId":"sandbox-request","SendStatusSet":[{"Code":"Ok","SerialNo":"sandbox-serial"}]}}`, `{"Response":null}`, `{"Response":{"SendStatusSet":[{"Code":"Invalid","Message":"fake-secret 123456"}]}}`} {
		p, err := NewTencentProvider("fake-id", "fake-secret", "fake-app", "sandbox")
		if err != nil {
			t.Fatal(err)
		}
		p.client.WithHttpTransport(tencentTransport(func(r *http.Request) (*http.Response, error) {
			var request map[string]any
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Fatal("invalid SDK JSON")
			}
			if request["SmsSdkAppId"] != "fake-app" || request["TemplateId"] != "sandbox-template" || request["SignName"] != "sandbox" {
				t.Fatal("Tencent configuration missing")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		}))
		r, err := p.SendReceipt(context.Background(), "13800000000", "sandbox-template", map[string]string{"code": "123456"})
		if strings.Contains(body, "sandbox-serial") {
			if err != nil || r.State != "accepted" {
				t.Fatal("valid receipt rejected")
			}
		} else if err == nil || strings.Contains(err.Error(), "fake-secret") || strings.Contains(err.Error(), "123456") {
			t.Fatal("rejected receipt accepted or leaked")
		}
	}
}
