package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/observ"
	"github.com/yuqing/platform/internal/pkg/queue"
)

func main() {
	configPath := os.Getenv("YUGING_CONFIG")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	logger := observ.New(os.Stdout, slog.LevelInfo)

	// Build queue driver.
	var q queue.Queue
	switch cfg.Queue.Driver {
	case "redis":
		logger.Warn("redis queue driver not yet implemented, falling back to memory")
		q = queue.NewMemory()
	case "rabbitmq":
		logger.Warn("rabbitmq queue driver not yet implemented, falling back to memory")
		q = queue.NewMemory()
	default:
		q = queue.NewMemory()
	}
	defer q.Close()

	// Create analysis service (orchestrates task lifecycle).
	svc := analysis.NewService(q, 4)

	// Subscribe to analysis tasks.
	_ = q.Subscribe(context.Background(), "analysis.tasks", func(ctx context.Context, msg queue.Message) error {
		taskID := string(msg.Body)
		logger.Info("worker received analysis task", slog.String("task_id", taskID))
		// TODO: Full pipeline — acquire budget → fetch → analyze → report.
		// For now, mark as queued acknowledgment.
		_ = svc
		return nil
	})

	// Subscribe to usage events for async rollup.
	_ = q.Subscribe(context.Background(), "usage.events", func(ctx context.Context, msg queue.Message) error {
		logger.Info("worker received usage event", slog.String("body", string(msg.Body)))
		// TODO: Buffer + flush to usage_daily rollup.
		return nil
	})

	logger.Info("worker started, listening for tasks...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("worker shutting down...")
}
