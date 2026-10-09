package sms

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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
