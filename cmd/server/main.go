package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/config"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/database"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository/postgres"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/server"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
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

	lineRepo := postgres.NewLineRepo(db)
	gameRepo := postgres.NewGameRepo(db)
	sbRepo := postgres.NewSportsbookRepo(db)
	rawRepo := postgres.NewRawResponseRepo(db)
	lineCache := cache.NewLineCache(rdb)
	publisher := pubsub.NewPublisher(rdb)

	oddsClient := oddsapi.NewClient(cfg.OddsAPIKey, "")
	ingestion := service.NewIngestionService(oddsClient, lineRepo, gameRepo, sbRepo, rawRepo, lineCache, publisher)
	closing := service.NewClosingLineService(lineRepo, gameRepo)
	query := service.NewLineQueryService(lineRepo, gameRepo, sbRepo, lineCache)

	ingestionEnabled := cfg.OddsAPIKey != ""
	if ingestionEnabled {
		scheduler := service.NewScheduler(ingestion, closing, cfg.OddsAPIPollInterval, cfg.OddsAPISports)
		go scheduler.Start(ctx)
	} else {
		slog.Warn("ODDS_API_KEY not set; ingestion scheduler disabled, read API only")
	}

	e := server.New(server.Deps{
		DB:               db,
		Redis:            rdb,
		Query:            query,
		Ingestion:        ingestion,
		SportsbookRepo:   sbRepo,
		SportKeys:        cfg.OddsAPISports,
		IngestionEnabled: ingestionEnabled,
	})

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
	cancel() // stop the ingestion scheduler before the HTTP server drains

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
