package auth

import (
	"context"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/notification"
)

// ResetPassword consumes only the public reset purposes. Activation remains the
// separate K9 SetPassword contract and cannot be reached by recovery.
func (s *Service) ResetPassword(ctx context.Context, token, phone, code, password string, sourceIP ...string) error {
	purpose, target, value := PasswordReset, "", token
	if phone != "" {
		purpose, target, value = PhoneReset, phone, code
	}
	_, err := s.ConfirmVerification(ctx, purpose, target, value, password, nil, sourceIP...)
	if purpose == PasswordReset {
		return s.identityLinkError(ctx, purpose, value, "", err)
	}
	return err
}
func (s *Service) ConfirmEmailChange(ctx context.Context, actor Principal, token string) (*User, error) {
	u, err := s.ConfirmVerification(ctx, EmailChange, "", token, "", &actor)
	if err != nil {
		return nil, s.identityLinkError(ctx, EmailChange, token, actor.UserID, err)
	}
	// The outbox already committed. Supplier availability never changes this
	// result; the server dispatcher sends and records retries outside the request.
	return u, nil
}

// Link states are safe only for a presented high-entropy email bearer and, for
// email change, its authenticated owner. Public SMS errors remain uniform.
func (s *Service) identityLinkError(ctx context.Context, purpose, value, uid string, err error) error {
	if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
		return err
	}
	state := "invalid"
	if store, ok := s.verifications.(interface {
		IdentityLinkState(context.Context, string, string, string) (string, error)
	}); ok && value != "" && len(value) <= 256 {
		var readErr error
		state, readErr = store.IdentityLinkState(ctx, purpose, verificationHash(s.secret, purpose, "", value), uid)
		if readErr != nil {
			return readErr
		}
	}
	return pkgerrors.WithDetails(pkgerrors.Wrap(pkgerrors.ErrBadRequest, "verification link is unavailable"), map[string]string{"state": state})
}

type IdentityNotice struct {
	ID, Recipient string
	Attempt       int
}
type identityNoticeStore interface {
	ClaimIdentityNotice(context.Context) (*IdentityNotice, error)
	CompleteIdentityNotice(context.Context, IdentityNotice, notification.Receipt) error
}

// DispatchIdentityNotices is the server/K6b retry port. Accepted events are
// terminal; failed sends retry after a durable delay, leased claims serialize
// workers. A crash after external acceptance but before receipt commit remains
// ambiguous delivery and may retry (no exactly-once supplier guarantee).
func (s *Service) DispatchIdentityNotices(ctx context.Context, limit int) error {
	store, ok := s.verifications.(identityNoticeStore)
	if !ok || s.emailSender == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	if limit < 1 || limit > 25 {
		limit = 25
	}
	for i := 0; i < limit; i++ {
		n, err := store.ClaimIdentityNotice(ctx)
		if err != nil {
			return err
		}
		if n == nil {
			return nil
		}
		receipt := notification.Receipt{Provider: "custom", State: "rejected"}
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		const subject = "账户邮箱已更换 - 盘古舆情"
		const body = "<p>您的账户登录邮箱已更换。如果不是您本人操作，请立即联系管理员处理账户安全问题。</p>"
		if sender, ok := s.emailSender.(interface {
			SendReceipt(context.Context, string, string, string) (notification.Receipt, error)
		}); ok {
			receipt, err = sender.SendReceipt(sendCtx, n.Recipient, subject, body)
		} else {
			err = s.emailSender.Send(sendCtx, n.Recipient, subject, body)
			if err == nil {
				receipt = notification.Receipt{Provider: "custom", State: "accepted", AcceptedAt: time.Now().UTC()}
			}
		}
		cancel()
		if err != nil {
			receipt = notification.Receipt{Provider: "custom", State: "rejected"}
		}
		receipt, normErr := notification.Normalize(receipt, "email_changed_notice")
		if normErr != nil {
			receipt, _ = notification.Normalize(notification.Receipt{Provider: "custom", State: "rejected"}, "email_changed_notice")
		}
		// A canceled send must still durably record its retry. No supplier response,
		// recipient or credential is copied into an error or receipt.
		saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		saveErr := store.CompleteIdentityNotice(saveCtx, *n, receipt)
		saveCancel()
		if saveErr != nil {
			return saveErr
		}
		if err != nil || normErr != nil {
			return pkgerrors.ErrServiceUnavailable
		}
	}
	return nil
}
