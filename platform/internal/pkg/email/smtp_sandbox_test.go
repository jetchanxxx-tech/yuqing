package email

import (
	"context"
	"errors"
	"testing"
)

func TestSMTPCancelledContextCannotDial(t *testing.T) {
	p, _ := NewSMTPProvider(Config{SMTPHost: "smtp.example.invalid", SMTPPort: 465})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.SendRawReceipt(ctx, "recipient@example.invalid", "sandbox", "isolated"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not retained")
	}
}
