package service

import (
	"context"
	"log/slog"
	"time"
)

// Scheduler runs ingestion cycles on a configurable interval.
type Scheduler struct {
	ingestion *IngestionService
	interval  time.Duration
	sportKeys []string
}

// NewScheduler creates a new polling scheduler.
func NewScheduler(ingestion *IngestionService, interval time.Duration, sportKeys []string) *Scheduler {
	return &Scheduler{
		ingestion: ingestion,
		interval:  interval,
		sportKeys: sportKeys,
	}
}

// Start begins the polling loop. It blocks until ctx is canceled.
func (s *Scheduler) Start(ctx context.Context) {
	slog.Info("starting ingestion scheduler",
		"interval", s.interval.String(),
		"sports", s.sportKeys,
	)

	// Run immediately on startup
	s.runCycle(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("ingestion scheduler stopped")
			return
		case <-ticker.C:
			s.runCycle(ctx)
		}
	}
}

func (s *Scheduler) runCycle(ctx context.Context) {
	for _, sport := range s.sportKeys {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.ingestion.Ingest(ctx, sport); err != nil {
			slog.Error("ingestion cycle failed", "sport", sport, "error", err)
		}
	}
}
