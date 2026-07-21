package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

type closingGameRepo struct {
	repository.GameRepository

	mu       sync.Mutex
	due      []model.Game
	dueErr   error
	markErr  error
	marked   []string
	dueCalls int
}

func (f *closingGameRepo) GetGamesDueForClosing(_ context.Context, _ time.Time) ([]model.Game, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dueCalls++
	if f.dueErr != nil {
		return nil, f.dueErr
	}
	return f.due, nil
}

func (f *closingGameRepo) MarkClosingCaptured(_ context.Context, gameID string, _ time.Time) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marked = append(f.marked, gameID)
	return nil
}

type closingLineRepo struct {
	repository.LineRepository

	mu         sync.Mutex
	captured   []string
	captureErr map[string]error
}

func (f *closingLineRepo) CaptureClosingLines(_ context.Context, gameID string, _ time.Time) (int, error) {
	if err := f.captureErr[gameID]; err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captured = append(f.captured, gameID)
	return 3, nil
}

func dueGame(id string) model.Game {
	return model.Game{
		GameExternalID: id,
		League:         model.LeagueNBA,
		CommenceTime:   time.Now().UTC().Add(-time.Hour),
	}
}

func TestCaptureDueCapturesAndMarks(t *testing.T) {
	gameRepo := &closingGameRepo{due: []model.Game{dueGame("g1"), dueGame("g2")}}
	lineRepo := &closingLineRepo{}
	svc := NewClosingLineService(lineRepo, gameRepo)

	svc.CaptureDue(context.Background())

	if len(lineRepo.captured) != 2 || lineRepo.captured[0] != "g1" || lineRepo.captured[1] != "g2" {
		t.Errorf("captured = %v, want [g1 g2]", lineRepo.captured)
	}
	if len(gameRepo.marked) != 2 {
		t.Errorf("marked = %v, want both games marked", gameRepo.marked)
	}
}

func TestCaptureDueNoGames(t *testing.T) {
	gameRepo := &closingGameRepo{}
	lineRepo := &closingLineRepo{}
	NewClosingLineService(lineRepo, gameRepo).CaptureDue(context.Background())

	if len(lineRepo.captured) != 0 || len(gameRepo.marked) != 0 {
		t.Error("nothing should be captured when no games are due")
	}
}

func TestCaptureDueQueryError(t *testing.T) {
	gameRepo := &closingGameRepo{dueErr: errors.New("db down")}
	lineRepo := &closingLineRepo{}
	NewClosingLineService(lineRepo, gameRepo).CaptureDue(context.Background())

	if len(lineRepo.captured) != 0 {
		t.Error("query failure must abort the sweep")
	}
}

func TestCaptureDueOneGameFailureDoesNotBlockOthers(t *testing.T) {
	gameRepo := &closingGameRepo{due: []model.Game{dueGame("bad"), dueGame("good")}}
	lineRepo := &closingLineRepo{captureErr: map[string]error{"bad": errors.New("boom")}}
	NewClosingLineService(lineRepo, gameRepo).CaptureDue(context.Background())

	if len(lineRepo.captured) != 1 || lineRepo.captured[0] != "good" {
		t.Errorf("captured = %v, want the healthy game only", lineRepo.captured)
	}
	if len(gameRepo.marked) != 1 || gameRepo.marked[0] != "good" {
		t.Errorf("marked = %v, want the failed game left unmarked for retry", gameRepo.marked)
	}
}

func TestCaptureDueMarkFailureContinues(t *testing.T) {
	gameRepo := &closingGameRepo{due: []model.Game{dueGame("g1"), dueGame("g2")}, markErr: errors.New("boom")}
	lineRepo := &closingLineRepo{}
	NewClosingLineService(lineRepo, gameRepo).CaptureDue(context.Background())

	// Capture still ran for both; neither was marked.
	if len(lineRepo.captured) != 2 {
		t.Errorf("captured = %v, want both games swept", lineRepo.captured)
	}
	if len(gameRepo.marked) != 0 {
		t.Errorf("marked = %v, want none marked on failure", gameRepo.marked)
	}
}

func TestCaptureDueStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	gameRepo := &closingGameRepo{due: []model.Game{dueGame("g1")}}
	lineRepo := &closingLineRepo{}
	NewClosingLineService(lineRepo, gameRepo).CaptureDue(ctx)

	if len(lineRepo.captured) != 0 {
		t.Error("canceled context must stop the per-game loop")
	}
}
