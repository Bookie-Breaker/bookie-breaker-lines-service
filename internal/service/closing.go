package service

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

var closingTracer = otel.Tracer("closing-line-service")

// ClosingLineService materializes closing lines for games that have started.
type ClosingLineService struct {
	lineRepo repository.LineRepository
	gameRepo repository.GameRepository
}

// NewClosingLineService creates a new closing-line service.
func NewClosingLineService(lineRepo repository.LineRepository, gameRepo repository.GameRepository) *ClosingLineService {
	return &ClosingLineService{lineRepo: lineRepo, gameRepo: gameRepo}
}

// CaptureDue captures closing lines for every game whose commence time has
// passed without a closing sweep. Each game is processed independently; a
// failure on one game is logged and does not block the others.
func (s *ClosingLineService) CaptureDue(ctx context.Context) {
	ctx, span := closingTracer.Start(ctx, "closing.CaptureDue")
	defer span.End()

	now := time.Now().UTC()
	games, err := s.gameRepo.GetGamesDueForClosing(ctx, now)
	if err != nil {
		slog.Error("failed to query games due for closing", "error", err)
		return
	}
	if len(games) == 0 {
		return
	}

	captured := 0
	for _, g := range games {
		if ctx.Err() != nil {
			return
		}
		rows, err := s.lineRepo.CaptureClosingLines(ctx, g.GameExternalID, g.CommenceTime)
		if err != nil {
			slog.Error("failed to capture closing lines", "game_id", g.GameExternalID, "error", err)
			continue
		}
		if err := s.gameRepo.MarkClosingCaptured(ctx, g.GameExternalID, now); err != nil {
			slog.Error("failed to mark closing captured", "game_id", g.GameExternalID, "error", err)
			continue
		}
		captured++
		slog.Info("closing lines captured", "game_id", g.GameExternalID, "lines", rows)
	}

	span.SetAttributes(
		attribute.Int("games.due", len(games)),
		attribute.Int("games.captured", captured),
	)
}
