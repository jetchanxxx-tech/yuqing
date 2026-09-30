package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yuqing/platform/internal/app"
	"github.com/yuqing/platform/internal/config"
)

func main() {
	configPath := os.Getenv("YUQING_CONFIG")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if err := runWorker(cfg); err != nil {
		log.Fatalf("worker: %v", err)
	}
}

func runWorker(cfg *config.Config) error {
	if cfg.Store.Driver == "postgres" && cfg.Queue.Driver != "postgres" {
		return fmt.Errorf("PostgreSQL store requires persistent PostgreSQL queue; refusing memory fallback")
	}
	if cfg.Queue.Driver != "" && cfg.Queue.Driver != "memory" && cfg.Queue.Driver != "postgres" {
		return fmt.Errorf("unsupported queue driver %q; refusing memory fallback", cfg.Queue.Driver)
	}
	if cfg.Queue.Driver == "postgres" && cfg.Store.Driver != "postgres" {
		return fmt.Errorf("PostgreSQL queue requires PostgreSQL store")
	}
	if cfg.Store.Driver != "postgres" {
		return fmt.Errorf("independent worker requires PostgreSQL store and queue")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.RunPGWorker(ctx, cfg, nil)
}
