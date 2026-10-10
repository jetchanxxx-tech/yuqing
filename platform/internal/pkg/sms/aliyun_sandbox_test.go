package sms

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yuqing/platform/internal/pkg/notification"
	"github.com/yuqing/platform/internal/testsupport/notificationsandbox"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type aliyunSandboxClient func(*http.Request) (*http.Response, error)

func (f aliyunSandboxClient) Call(r *http.Request, _ *http.Transport) (*http.Response, error) {
	return f(r)
}
func TestAliyunSandboxJSONEncoding(t *testing.T) {
	params := map[string]string{"code": "12\"34\\56\n", "name": "沙箱"}
	var got map[string]string
	if err := json.Unmarshal([]byte(mapToJSON(params)), &got); err != nil || got["code"] != params["code"] {
		t.Fatal("SMS parameters are not valid lossless JSON")
	}
}
func TestAliyunSandboxMissingReceiptAndSanitizedError(t *testing.T) {
	for _, tc := range []struct{ name, body string }{{"missing", `{"Code":"OK"}`}, {"nil", `null`}, {"no_code", `{"BizId":"sandbox-1"}`}, {"rejected", `{"Code":"Invalid","Message":"fake-secret 123456"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewAliyunProvider("fake-id", "fake-secret", "sandbox")
			if err != nil {
				t.Fatal(err)
			}
			p.client.HttpClient = aliyunSandboxClient(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			defer func() {
				if recover() != nil {
					t.Error("nil supplier response panicked")
				}
			}()
			err = p.Send(context.Background(), "13800000000", "sandbox-bind", map[string]string{"code": "123456"})
			if err == nil {
				t.Fatal("missing/rejected receipt treated as success")
			}
			if strings.Contains(err.Error(), "fake-secret") || strings.Contains(err.Error(), "123456") {
				t.Fatal("provider error echoes secret payload")
			}
		})
	}
}

func TestAliyunSandboxRequestReceiptAndSimulatedDelivery(t *testing.T) {
	var inbox notificationsandbox.Inbox
	p, err := NewAliyunProvider("fake-id", "fake-secret", "sandbox-sign")
	if err != nil {
		t.Fatal(err)
	}
	p.client.HttpClient = aliyunSandboxClient(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("PhoneNumbers") != "13800000000" || r.Form.Get("SignName") != "sandbox-sign" || r.Form.Get("TemplateCode") != "sandbox-login" {
			t.Fatalf("SMS request routing fields differ: method=%s", r.Method)
		}
		var params map[string]string
		if json.Unmarshal([]byte(r.Form.Get("TemplateParam")), &params) != nil || params["code"] != "12\"34\\56\n" {
			t.Fatal("SDK body changed template JSON")
		}
		if r.Header.Get("Authorization") == "" && r.Form.Get("Signature") == "" {
			t.Fatal("SDK request is unsigned")
		}
		inbox.Accept(r.Form.Get("PhoneNumbers"), "phone_login", params["code"])
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"Code":"OK","BizId":"sandbox-sms-1","RequestId":"sandbox-request-1"}`))}, nil
	})
	receipt, err := p.SendReceipt(context.Background(), "13800000000", "sandbox-login", map[string]string{"code": "12\"34\\56\n"})
	if err != nil || receipt.State != "accepted" || receipt.Provider != "aliyun" || receipt.ProviderID != notification.Accepted("aliyun", "sandbox-sms-1").ProviderID {
		t.Fatalf("receipt absent: %v", err)
	}
	if _, err = inbox.Delivered(0); err == nil {
		t.Fatal("acceptance implies delivery")
	}
	if inbox.Deliver(0) != nil {
		t.Fatal("delivery simulation failed")
	}
	if _, err = inbox.Delivered(0); err != nil {
		t.Fatal(err)
	}
}
func TestAliyunSandboxHTTPFailureCancellationAndDeadline(t *testing.T) {
	for _, kind := range []string{"http", "malformed", "network", "cancel", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := NewAliyunProvider("fake-id", "fake-secret", "sandbox")
			calls := 0
			p.client.HttpClient = aliyunSandboxClient(func(r *http.Request) (*http.Response, error) {
				calls++
				if kind == "deadline" {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				if kind == "network" {
					return nil, errors.New("fake-secret 123456")
				}
				status := 200
				if kind == "http" {
					status = 503
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fake-secret 123456"))}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			_, err := p.SendReceipt(ctx, "13800000000", "sandbox", map[string]string{"code": "123456"})
			if err == nil || strings.Contains(err.Error(), "fake-secret") || strings.Contains(err.Error(), "123456") {
				t.Fatal("failed delivery succeeded or leaked")
			}
			if kind == "cancel" && calls != 0 {
				t.Fatal("cancelled request reached transport")
			}
			if kind == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("deadline not preserved")
			}
		})
	}
}
