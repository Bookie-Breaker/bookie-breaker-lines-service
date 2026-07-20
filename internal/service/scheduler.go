package service

import (
	"context"
	"log/slog"
	"time"
)

// Scheduler runs ingestion cycles on a configurable interval.
type Scheduler struct {
	ingestion *IngestionService
	closing   *ClosingLineService
	interval  time.Duration
	sportKeys []string
}

// NewScheduler creates a new polling scheduler.
func NewScheduler(ingestion *IngestionService, closing *ClosingLineService, interval time.Duration, sportKeys []string) *Scheduler {
	return &Scheduler{
		ingestion: ingestion,
		closing:   closing,
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

	if ctx.Err() == nil {
		s.closing.CaptureDue(ctx)
	}
}

// PropScheduler runs prop ingestion on its own (slower) cadence. Props cost
// one Odds API request per event, so they poll on PROP_POLL_INTERVAL over the
// PROP_SPORTS allow-list rather than riding the main ingestion ticker.
type PropScheduler struct {
	props     *PropIngestionService
	interval  time.Duration
	sportKeys []string
}

// NewPropScheduler creates the prop polling scheduler.
func NewPropScheduler(props *PropIngestionService, interval time.Duration, sportKeys []string) *PropScheduler {
	return &PropScheduler{
		props:     props,
		interval:  interval,
		sportKeys: sportKeys,
	}
}

// Start begins the prop polling loop. It blocks until ctx is canceled.
func (s *PropScheduler) Start(ctx context.Context) {
	slog.Info("starting prop ingestion scheduler",
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
			slog.Info("prop ingestion scheduler stopped")
			return
		case <-ticker.C:
			s.runCycle(ctx)
		}
	}
}

func (s *PropScheduler) runCycle(ctx context.Context) {
	for _, sport := range s.sportKeys {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.props.IngestProps(ctx, sport); err != nil {
			slog.Error("prop ingestion cycle failed", "sport", sport, "error", err)
		}
	}
}
