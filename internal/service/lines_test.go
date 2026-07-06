package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

type fakeLineRepo struct {
	repository.LineRepository
	movement []model.LineSnapshot
	current  []model.LineSnapshot
	closing  []model.ClosingLine
}

func (f *fakeLineRepo) GetLineMovement(_ context.Context, _ string, _ repository.MovementFilters) ([]model.LineSnapshot, error) {
	return f.movement, nil
}

func (f *fakeLineRepo) GetGameLines(_ context.Context, _ string, _ repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	return f.current, false, nil
}

func (f *fakeLineRepo) GetClosingLines(_ context.Context, _ string, _ repository.ClosingLineFilters) ([]model.ClosingLine, error) {
	return f.closing, nil
}

type fakeGameRepo struct {
	repository.GameRepository
	games map[string]model.Game
}

func (f *fakeGameRepo) GetGame(_ context.Context, id string) (*model.Game, error) {
	if g, ok := f.games[id]; ok {
		return &g, nil
	}
	return nil, pgx.ErrNoRows
}

func (f *fakeGameRepo) GetGames(_ context.Context, ids []string) (map[string]model.Game, error) {
	out := make(map[string]model.Game)
	for _, id := range ids {
		if g, ok := f.games[id]; ok {
			out[id] = g
		}
	}
	return out, nil
}

type fakeSBRepo struct {
	repository.SportsbookRepository
	books []model.Sportsbook
}

func (f *fakeSBRepo) GetAll(_ context.Context, _, _ *bool) ([]model.Sportsbook, error) {
	return f.books, nil
}

func ts(minute int) time.Time {
	return time.Date(2026, 7, 3, 12, minute, 0, 0, time.UTC)
}

func movementSnap(book, sel string, line float64, odds int, at time.Time) model.LineSnapshot {
	return model.LineSnapshot{
		ID:             "id-" + at.Format("150405"),
		GameExternalID: "g1",
		SportsbookID:   book,
		SportsbookKey:  book + "-key",
		MarketType:     model.MarketSpread,
		Selection:      sel,
		LineValue:      &line,
		OddsAmerican:   odds,
		OddsDecimal:    1.91,
		CapturedAt:     at,
	}
}

func testGame() model.Game {
	return model.Game{
		GameExternalID: "g1",
		League:         model.LeagueNBA,
		HomeTeam:       "Los Angeles Lakers",
		AwayTeam:       "Boston Celtics",
		CommenceTime:   ts(0).Add(6 * time.Hour),
	}
}

func TestMovementAggregation(t *testing.T) {
	lineRepo := &fakeLineRepo{
		movement: []model.LineSnapshot{
			movementSnap("b1", "Los Angeles Lakers -3.5", -3.5, -110, ts(0)),
			movementSnap("b1", "Los Angeles Lakers -3.5", -3.0, -110, ts(10)),
			movementSnap("b1", "Los Angeles Lakers -3.5", -2.5, -115, ts(20)),
		},
		closing: []model.ClosingLine{
			{
				SportsbookID: "b1",
				Selection:    "Los Angeles Lakers -3.5",
				LineValue:    ptr(-2.5),
				OddsAmerican: -115,
				CapturedAt:   ts(20),
			},
		},
	}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}

	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, nil)
	movements, err := svc.Movement(context.Background(), "g1", repository.MovementFilters{MarketType: "SPREAD"})
	if err != nil {
		t.Fatalf("Movement failed: %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("expected 1 movement group, got %d", len(movements))
	}

	m := movements[0]
	if *m.OpeningLine != -3.5 || *m.OpeningOdds != -110 {
		t.Errorf("opening = (%v, %v), want (-3.5, -110)", *m.OpeningLine, *m.OpeningOdds)
	}
	if *m.CurrentLine != -2.5 || m.CurrentOdds != -115 {
		t.Errorf("current = (%v, %v), want (-2.5, -115)", *m.CurrentLine, m.CurrentOdds)
	}
	if m.ClosingLine == nil || *m.ClosingLine != -2.5 || *m.ClosingOdds != -115 {
		t.Errorf("closing = (%v, %v), want (-2.5, -115)", m.ClosingLine, m.ClosingOdds)
	}
	if *m.TotalMovement != 1.0 {
		t.Errorf("total_movement = %v, want 1.0", *m.TotalMovement)
	}
	if len(m.Snapshots) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(m.Snapshots))
	}
	if !m.Snapshots[0].IsOpening || m.Snapshots[1].IsOpening {
		t.Error("only the first snapshot should be marked opening")
	}
	// Line moved up (+1.0) while odds moved down (-5): opposite directions,
	// which the heuristic treats as normal movement.
	if m.IsReverseMovement {
		t.Error("expected normal movement, got reverse")
	}
}

func TestMovementReverseHeuristic(t *testing.T) {
	lineRepo := &fakeLineRepo{
		movement: []model.LineSnapshot{
			movementSnap("b1", "Los Angeles Lakers -3.5", -3.5, -115, ts(0)),
			movementSnap("b1", "Los Angeles Lakers -3.5", -3.0, -105, ts(10)),
		},
	}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}

	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, nil)
	movements, err := svc.Movement(context.Background(), "g1", repository.MovementFilters{MarketType: "SPREAD"})
	if err != nil {
		t.Fatalf("Movement failed: %v", err)
	}
	// Line +0.5 and odds +10 moved in the same direction: flagged reverse.
	if !movements[0].IsReverseMovement {
		t.Error("expected reverse movement flag")
	}
}

func TestMovementUnknownGame(t *testing.T) {
	svc := NewLineQueryService(&fakeLineRepo{}, &fakeGameRepo{games: map[string]model.Game{}}, &fakeSBRepo{}, nil)
	if _, err := svc.Movement(context.Background(), "missing", repository.MovementFilters{}); err != ErrGameNotFound {
		t.Errorf("expected ErrGameNotFound, got %v", err)
	}
}

func TestBestLines(t *testing.T) {
	spread := func(book, sel string, line float64, odds int, decimal float64) model.LineSnapshot {
		return model.LineSnapshot{
			ID:             book + "-" + sel,
			GameExternalID: "g1",
			SportsbookID:   book,
			SportsbookKey:  book + "-key",
			MarketType:     model.MarketSpread,
			Selection:      sel,
			LineValue:      &line,
			OddsAmerican:   odds,
			OddsDecimal:    decimal,
			CapturedAt:     ts(0),
		}
	}

	lineRepo := &fakeLineRepo{
		current: []model.LineSnapshot{
			spread("b1", "Los Angeles Lakers -3.5", -3.5, -110, 1.91),
			spread("b2", "Los Angeles Lakers -3.5", -3.5, -105, 1.95),
			spread("b1", "Boston Celtics +3.5", 3.5, -110, 1.91),
			spread("b2", "Boston Celtics +3.5", 3.5, -115, 1.87),
		},
	}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}
	sbRepo := &fakeSBRepo{books: []model.Sportsbook{
		{ID: "b1", Name: "Book One", Key: "b1-key"},
		{ID: "b2", Name: "Book Two", Key: "b2-key"},
	}}

	svc := NewLineQueryService(lineRepo, gameRepo, sbRepo, nil)
	best, err := svc.Best(context.Background(), "g1", "SPREAD", "")
	if err != nil {
		t.Fatalf("Best failed: %v", err)
	}
	if len(best) != 2 {
		t.Fatalf("expected 2 best lines (home + away), got %d", len(best))
	}

	bySide := make(map[string]model.BestLine)
	for _, b := range best {
		bySide[b.Side] = b
	}
	if bySide["HOME"].SportsbookID != "b2" || bySide["HOME"].BestOddsDecimal != 1.95 {
		t.Errorf("best HOME = %+v, want book b2 at 1.95", bySide["HOME"])
	}
	if bySide["AWAY"].SportsbookID != "b1" || bySide["AWAY"].SportsbookName != "Book One" {
		t.Errorf("best AWAY = %+v, want book b1", bySide["AWAY"])
	}
	if bySide["HOME"].ImpliedProb == 0 {
		t.Error("implied probability should be populated")
	}
}

func TestDeriveSide(t *testing.T) {
	game := testGame()

	tests := []struct {
		market    model.MarketType
		selection string
		want      string
	}{
		{model.MarketTotal, "Over 220.5", "OVER"},
		{model.MarketTotal, "Under 220.5", "UNDER"},
		{model.MarketSpread, "Los Angeles Lakers -3.5", "HOME"},
		{model.MarketSpread, "Boston Celtics +3.5", "AWAY"},
		{model.MarketMoneyline, "Los Angeles Lakers", "HOME"},
		{model.MarketMoneyline, "Boston Celtics", "AWAY"},
		{model.MarketMoneyline, "Unknown Team", ""},
		{model.MarketMoneyline, "Draw", "DRAW"},
		{model.MarketSpread, "Draw", ""},
	}

	for _, tt := range tests {
		got := deriveSide(model.LineSnapshot{MarketType: tt.market, Selection: tt.selection}, &game)
		if got != tt.want {
			t.Errorf("deriveSide(%s, %q) = %q, want %q", tt.market, tt.selection, got, tt.want)
		}
	}

	if got := deriveSide(model.LineSnapshot{MarketType: model.MarketSpread, Selection: "Los Angeles Lakers -3.5"}, nil); got != "" {
		t.Errorf("deriveSide without game = %q, want empty", got)
	}
}
