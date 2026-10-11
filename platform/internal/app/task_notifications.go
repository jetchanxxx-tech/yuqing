package app

import (
	"context"
	"github.com/yuqing/platform/internal/platform/accountnotify"
	"log/slog"
	"time"
)

func RunTaskNotifications(ctx context.Context, service *accountnotify.Service, logger *slog.Logger) {
	if service == nil {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := service.Dispatch(ctx, 25); err != nil && ctx.Err() == nil {
			logger.Warn("task notification awaiting retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
