package app

import (
	"context"
	"github.com/yuqing/platform/internal/platform/accountclosure"
	"log/slog"
	"time"
)

// Each tick discovers a bounded set of durable due/retry records. Per-user
// process claims serialize overlapping server/CLI instances; retained data and
// unsupported policies use persisted future eligibility/backoff, not a loop.
func RunAccountClosures(ctx context.Context, service *accountclosure.Service, logger *slog.Logger) {
	if service == nil {
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := service.AdvanceDue(ctx, 5, 100); err != nil && ctx.Err() == nil {
			logger.Warn("account closure progress pending retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
