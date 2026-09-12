package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yuging/platform/internal/api"
	"github.com/yuging/platform/internal/app"
	"github.com/yuging/platform/internal/config"
	"github.com/yuging/platform/internal/pkg/observ"
)

func main() {
	// Load configuration.
	configPath := os.Getenv("YUGING_CONFIG")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Build logger.
	logger := observ.New(os.Stdout, slog.LevelInfo)

	// Wire services (composition root) and build the router.
	deps := app.Build(cfg, logger)
	router := api.NewRouter(cfg, logger, deps)

	// Create HTTP server.
	srv := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown.
	go func() {
		logger.Info("server starting", slog.String("addr", cfg.Server.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", slog.Any("err", err))
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("forced shutdown", slog.Any("err", err))
	}
	logger.Info("server stopped")
}
