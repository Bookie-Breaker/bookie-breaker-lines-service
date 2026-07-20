package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/sharpapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

const (
	liveBackoffInitial          = 500 * time.Millisecond
	liveBackoffMax              = 30 * time.Second
	defaultLiveFailureThreshold = 5
)

// LiveStreamer is the streaming contract the consumer needs from the
// SharpAPI client.
type LiveStreamer interface {
	Stream(ctx context.Context) (<-chan sharpapi.Frame, <-chan error)
}

// LiveConsumer consumes the SharpAPI SSE stream and persists live line
// snapshots through the same dedup+insert+publish path as REST polling
// ingestion. It reconnects with capped exponential backoff; the REST polling
// scheduler keeps running regardless, so a downed stream degrades to polling
// freshness rather than losing coverage.
type LiveConsumer struct {
	stream           LiveStreamer
	ingestion        *IngestionService
	gameRepo         repository.GameRepository
	sbRepo           repository.SportsbookRepository
	failureThreshold int

	// sbMap caches sportsbook key -> UUID; refreshed when a frame references
	// an unknown key. Only touched from the Run goroutine.
	sbMap map[string]string
}

// NewLiveConsumer creates a live line consumer. failureThreshold is the
// number of consecutive connection failures before warning that REST polling
// is covering; <=0 uses the default of 5.
func NewLiveConsumer(
	stream LiveStreamer,
	ingestion *IngestionService,
	gameRepo repository.GameRepository,
	sbRepo repository.SportsbookRepository,
	failureThreshold int,
) *LiveConsumer {
	if failureThreshold <= 0 {
		failureThreshold = defaultLiveFailureThreshold
	}
	return &LiveConsumer{
		stream:           stream,
		ingestion:        ingestion,
		gameRepo:         gameRepo,
		sbRepo:           sbRepo,
		failureThreshold: failureThreshold,
	}
}

// Run connects, consumes, and reconnects until ctx is canceled. It blocks.
func (c *LiveConsumer) Run(ctx context.Context) {
	slog.Info("starting sharpapi live consumer", "failure_threshold", c.failureThreshold)

	backoff := liveBackoffInitial
	failures := 0

	for ctx.Err() == nil {
		gotFrame := c.consumeStream(ctx)
		if ctx.Err() != nil {
			break
		}

		if gotFrame {
			// Successful frames reset the backoff and the failure counter.
			failures = 0
			backoff = liveBackoffInitial
		} else {
			failures++
			if failures == c.failureThreshold {
				slog.Warn("sharpapi stream unavailable; REST polling scheduler is covering line ingestion",
					"consecutive_failures", failures)
			}
		}

		wait := jitter(backoff)
		slog.Debug("reconnecting to sharpapi stream", "wait", wait.String(), "consecutive_failures", failures)
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		backoff = nextBackoff(backoff)
	}

	slog.Info("sharpapi live consumer stopped")
}

// consumeStream opens one stream and processes frames until it ends.
// It reports whether at least one frame was processed successfully.
func (c *LiveConsumer) consumeStream(ctx context.Context) bool {
	frames, errs := c.stream.Stream(ctx)

	gotFrame := false
	for frame := range frames {
		if err := c.handleFrame(ctx, frame); err != nil {
			slog.Warn("failed to process live frame",
				"event_id", frame.EventID, "sport_key", frame.SportKey, "error", err)
			continue
		}
		gotFrame = true
	}

	if err := <-errs; err != nil && ctx.Err() == nil {
		slog.Warn("sharpapi stream error", "error", err)
	}
	return gotFrame
}

func (c *LiveConsumer) handleFrame(ctx context.Context, frame sharpapi.Frame) error {
	sbMap, err := c.sportsbooks(ctx, false)
	if err != nil {
		return err
	}

	snapshots, game, err := sharpapi.Normalize(frame, sbMap)
	if errors.Is(err, sharpapi.ErrUnknownSportsbook) {
		// The book may have been added since the map was cached.
		if sbMap, err = c.sportsbooks(ctx, true); err != nil {
			return err
		}
		snapshots, game, err = sharpapi.Normalize(frame, sbMap)
	}
	if err != nil {
		return err
	}

	if game != nil {
		if upsertErr := c.gameRepo.UpsertGames(ctx, []model.Game{*game}); upsertErr != nil {
			return fmt.Errorf("upsert game: %w", upsertErr)
		}
	}

	if _, _, err := c.ingestion.persistSnapshots(ctx, snapshots, sbMap); err != nil {
		return err
	}
	return nil
}

func (c *LiveConsumer) sportsbooks(ctx context.Context, refresh bool) (map[string]string, error) {
	if c.sbMap != nil && !refresh {
		return c.sbMap, nil
	}
	sportsbooks, err := c.sbRepo.GetAll(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch sportsbooks: %w", err)
	}
	m := make(map[string]string, len(sportsbooks))
	for _, sb := range sportsbooks {
		m[sb.Key] = sb.ID
	}
	c.sbMap = m
	return m, nil
}

// nextBackoff doubles the delay up to the cap.
func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > liveBackoffMax {
		next = liveBackoffMax
	}
	return next
}

// jitter returns a random duration in [d/2, d] so reconnecting consumers do
// not thunder in lockstep.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}
