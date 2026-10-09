package email

import (
	"context"
	"github.com/resendlabs/resend-go"
	"io"
	"net/http"
	"strings"
	"testing"
)

type resendSandboxTransport func(*http.Request) (*http.Response, error)

func (f resendSandboxTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestResendSandboxMissingReceiptAndSanitizedError(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{{"missing", `{}`, 200}, {"nil", `null`, 200}, {"malformed", `broken`, 200}, {"rejected", `{"message":"credential fake-key code 123456"}`, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := NewResendProvider(Config{ResendAPIKey: "fake-key", FromAddress: "sender@example.invalid"})
			p.client = resend.NewCustomClient(&http.Client{Transport: resendSandboxTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}, "fake-key")
			err := p.SendRaw(context.Background(), "recipient@example.invalid", "sandbox", "123456")
			if err == nil {
				t.Fatal("response without acceptance receipt treated as success")
			}
			if strings.Contains(err.Error(), "fake-key") || strings.Contains(err.Error(), "123456") {
				t.Fatal("supplier echo leaked in application error")
			}
		})
	}
}
func TestResendSandboxCancelledContextCannotSend(t *testing.T) {
	p, _ := NewResendProvider(Config{ResendAPIKey: "fake-key", FromAddress: "sender@example.invalid"})
	calls := 0
	p.client = resend.NewCustomClient(&http.Client{Transport: resendSandboxTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"sandbox-email-1"}`))}, nil
	})}, "fake-key")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.SendRaw(ctx, "recipient@example.invalid", "sandbox", "isolated"); err == nil || calls != 0 {
		t.Fatal("canceled request reached supplier transport")
	}
}
