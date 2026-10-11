package email

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/resendlabs/resend-go"
	"github.com/yuqing/platform/internal/pkg/notification"
	"github.com/yuqing/platform/internal/testsupport/notificationsandbox"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestResendSandboxRequestReceiptAndSimulatedDelivery(t *testing.T) {
	var inbox notificationsandbox.Inbox
	p, _ := NewResendProvider(Config{ResendAPIKey: "fake-key", FromAddress: "sender@example.invalid", FromName: "Sandbox"})
	p.client = resend.NewCustomClient(&http.Client{Transport: resendSandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer fake-key" {
			t.Fatal("vendor request contract differs")
		}
		var request resend.SendEmailRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.From != "Sandbox <sender@example.invalid>" || len(request.To) != 1 || request.To[0] != "recipient@example.invalid" || request.Subject != "email_change" {
			t.Fatal("wrong sender/recipient/purpose")
		}
		inbox.Accept(request.To[0], request.Subject, request.Html)
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"sandbox-acceptance-1"}`))}, nil
	})}, "fake-key")
	receipt, err := p.SendRawReceipt(context.Background(), "recipient@example.invalid", "email_change", `<a href="https://example.invalid/email-change#token=isolated-link">confirm</a>`)
	if err != nil || receipt.State != "accepted" || receipt.Provider != "resend" || receipt.ProviderID != notification.Accepted("resend", "sandbox-acceptance-1").ProviderID || receipt.AcceptedAt.IsZero() {
		t.Fatal("acceptance receipt missing")
	}
	if _, err = inbox.Delivered(0); err == nil {
		t.Fatal("acceptance incorrectly implies delivery")
	}
	if err = inbox.Deliver(0); err != nil {
		t.Fatal(err)
	}
	delivered, err := inbox.Delivered(0)
	if err != nil || !strings.Contains(delivered.Payload, "#token=isolated-link") {
		t.Fatal("isolated delivered payload unavailable")
	}
}
func TestResendSandboxHTTPFailureAndDeadline(t *testing.T) {
	for _, kind := range []string{"http", "network", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := NewResendProvider(Config{ResendAPIKey: "fake-key"})
			p.client = resend.NewCustomClient(&http.Client{Transport: resendSandboxTransport(func(r *http.Request) (*http.Response, error) {
				if kind == "deadline" {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				if kind == "network" {
					return nil, errors.New("fake-key isolated-link")
				}
				return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fake-key isolated-link"))}, nil
			})}, "fake-key")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, err := p.SendRawReceipt(ctx, "recipient@example.invalid", "sandbox", "isolated-link")
			if err == nil || strings.Contains(err.Error(), "fake-key") || strings.Contains(err.Error(), "isolated-link") {
				t.Fatal("failed delivery succeeded or leaked")
			}
			if kind == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("deadline not retained")
			}
		})
	}
}
