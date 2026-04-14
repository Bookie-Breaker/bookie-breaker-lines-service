package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/config"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/database"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/server"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/telemetry"
)

func main() {
	cfg := config.Load()

	setupLogger(cfg.LogLevel)
	slog.Info("starting lines-service", "port", cfg.Port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	otelShutdown, err := telemetry.Init(ctx, cfg.OTELServiceName, cfg.OTELExporterEndpoint)
	if err != nil {
		slog.Warn("failed to init telemetry, continuing without it", "error", err)
	}

	db, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	rdb, err := cache.NewClient(ctx, cfg.RedisURL)
	if err != nil {
		slog.Error("failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	defer func() { _ = rdb.Close() }()

	e := server.New(db, rdb)

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		if err := e.Start(addr); err != nil {
			slog.Info("server stopped", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down gracefully")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	if otelShutdown != nil {
		if err := otelShutdown(shutdownCtx); err != nil {
			slog.Error("telemetry shutdown error", "error", err)
		}
	}
}

func setupLogger(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))
}
