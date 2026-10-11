package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/yuqing/platform/internal/platform/auth"
)

// RunIdentityNotifications drains only committed identity notice intents.
// Errors remain generic; receipts and retry state are persisted by the store.
func RunIdentityNotifications(ctx context.Context, service *auth.Service, logger *slog.Logger) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := service.DispatchIdentityNotices(ctx, 25); err != nil && ctx.Err() == nil {
			logger.Warn("identity notification delivery pending retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
