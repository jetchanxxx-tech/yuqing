package accountnotify

import (
	"context"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/notification"
	"github.com/yuqing/platform/internal/platform/auth"
)

type Preferences struct {
	TaskCompleted bool `json:"task_completed"`
}
type Intent struct {
	ID, Recipient string
	Attempt       int
	Cancelled     bool
}
type Store interface {
	Preferences(context.Context, auth.Principal) (Preferences, error)
	SetPreferences(context.Context, auth.Principal, Preferences) error
	Claim(context.Context) (*Intent, error)
	Complete(context.Context, Intent, notification.Receipt) error
}
type Service struct {
	store  Store
	sender auth.MailSender
}

func NewService(store Store, sender auth.MailSender) *Service {
	return &Service{store: store, sender: sender}
}
func (s *Service) SetSender(sender auth.MailSender) { s.sender = sender }
func (s *Service) Preferences(ctx context.Context, actor auth.Principal) (Preferences, error) {
	if actor.AuthType != "jwt" || actor.UserID == "" {
		return Preferences{}, pkgerrors.ErrForbidden
	}
	return s.store.Preferences(ctx, actor)
}
func (s *Service) SetPreferences(ctx context.Context, actor auth.Principal, prefs Preferences) error {
	if actor.AuthType != "jwt" || actor.UserID == "" {
		return pkgerrors.ErrForbidden
	}
	return s.store.SetPreferences(ctx, actor, prefs)
}

func (s *Service) Dispatch(ctx context.Context, limit int) error {
	if limit < 1 || limit > 25 {
		return pkgerrors.ErrBadRequest
	}
	if s.sender == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.store.Claim(ctx)
		if err != nil {
			return err
		}
		if n == nil {
			return nil
		}
		if n.Cancelled {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		const subject = "分析任务已完成 - 盘古舆情"
		const body = "<p>一次分析任务执行已完成。请登录盘古舆情查看结果。</p>"
		r := notification.Receipt{Provider: "custom", State: "rejected"}
		if sender, ok := s.sender.(interface {
			SendReceipt(context.Context, string, string, string) (notification.Receipt, error)
		}); ok {
			r, err = sender.SendReceipt(sendCtx, n.Recipient, subject, body)
		} else {
			err = s.sender.Send(sendCtx, n.Recipient, subject, body)
			if err == nil {
				r = notification.Receipt{Provider: "custom", State: "accepted", AcceptedAt: time.Now().UTC()}
			}
		}
		cancel()
		if err != nil {
			r = notification.Receipt{Provider: "custom", State: "rejected"}
		}
		r, normErr := notification.Normalize(r, "task_completed")
		if normErr != nil {
			r, _ = notification.Normalize(notification.Receipt{Provider: "custom", State: "rejected"}, "task_completed")
		}
		saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		saveErr := s.store.Complete(saveCtx, *n, r)
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
