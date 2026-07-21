package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func TestGameRepoUpsertAndGet(t *testing.T) {
	ctx := context.Background()
	repo := NewGameRepo(testPool)
	commence := time.Date(2030, 1, 15, 19, 0, 0, 0, time.UTC)

	seedGame(t, "gametest-upsert", commence)

	game, err := repo.GetGame(ctx, "gametest-upsert")
	if err != nil {
		t.Fatalf("GetGame failed: %v", err)
	}
	if game.League != model.LeagueNBA || game.HomeTeam != "Los Angeles Lakers" || !game.CommenceTime.Equal(commence) {
		t.Errorf("game = %+v, want the seeded row", game)
	}
	if game.ClosingCapturedAt != nil {
		t.Error("new games must not have closing captured")
	}
	if game.FirstSeenAt.IsZero() || game.UpdatedAt.IsZero() {
		t.Error("timestamps should be populated")
	}

	// Re-upserting the same external id updates teams and commence time
	// rather than inserting a second row.
	newCommence := commence.Add(24 * time.Hour)
	err = repo.UpsertGames(ctx, []model.Game{{
		GameExternalID: "gametest-upsert",
		League:         model.LeagueNBA,
		HomeTeam:       "LA Lakers",
		AwayTeam:       "Boston Celtics",
		CommenceTime:   newCommence,
	}})
	if err != nil {
		t.Fatalf("re-upsert failed: %v", err)
	}
	game, err = repo.GetGame(ctx, "gametest-upsert")
	if err != nil {
		t.Fatalf("GetGame after update failed: %v", err)
	}
	if game.HomeTeam != "LA Lakers" || !game.CommenceTime.Equal(newCommence) {
		t.Errorf("game = %+v, want updated team and commence time", game)
	}
}

func TestGameRepoUpsertEmptyIsNoop(t *testing.T) {
	if err := NewGameRepo(testPool).UpsertGames(context.Background(), nil); err != nil {
		t.Fatalf("empty upsert should be a no-op, got %v", err)
	}
}

func TestGameRepoGetGameMissing(t *testing.T) {
	if _, err := NewGameRepo(testPool).GetGame(context.Background(), "gametest-missing"); err == nil {
		t.Error("GetGame should fail for an unknown id")
	}
}

func TestGameRepoGetGames(t *testing.T) {
	ctx := context.Background()
	repo := NewGameRepo(testPool)
	commence := time.Date(2030, 2, 1, 19, 0, 0, 0, time.UTC)

	seedGame(t, "gametest-multi-1", commence)
	seedGame(t, "gametest-multi-2", commence)

	games, err := repo.GetGames(ctx, []string{"gametest-multi-1", "gametest-multi-2", "gametest-multi-missing"})
	if err != nil {
		t.Fatalf("GetGames failed: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %v, want the 2 existing ids only", games)
	}
	if _, ok := games["gametest-multi-1"]; !ok {
		t.Error("gametest-multi-1 missing from result map")
	}

	empty, err := repo.GetGames(ctx, nil)
	if err != nil {
		t.Fatalf("GetGames(nil) failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("GetGames(nil) = %v, want empty map without querying", empty)
	}
}

func TestGameRepoClosingSweepLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := NewGameRepo(testPool)
	now := time.Now().UTC()

	seedGame(t, "gametest-due", now.Add(-2*time.Hour))        // started, sweep pending
	seedGame(t, "gametest-future", now.Add(6*time.Hour))      // not started
	seedGame(t, "gametest-ancient", now.Add(-8*24*time.Hour)) // beyond the 7-day lower bound

	due, err := repo.GetGamesDueForClosing(ctx, now)
	if err != nil {
		t.Fatalf("GetGamesDueForClosing failed: %v", err)
	}
	found := make(map[string]bool, len(due))
	for _, g := range due {
		found[g.GameExternalID] = true
	}
	if !found["gametest-due"] {
		t.Error("started game should be due for closing")
	}
	if found["gametest-future"] {
		t.Error("future game must not be due")
	}
	if found["gametest-ancient"] {
		t.Error("games older than 7 days must not be replayed")
	}

	// Marking the game removes it from the due set and stamps the game row.
	capturedAt := now.Truncate(time.Second)
	if err := repo.MarkClosingCaptured(ctx, "gametest-due", capturedAt); err != nil {
		t.Fatalf("MarkClosingCaptured failed: %v", err)
	}

	due, err = repo.GetGamesDueForClosing(ctx, now)
	if err != nil {
		t.Fatalf("GetGamesDueForClosing after mark failed: %v", err)
	}
	for _, g := range due {
		if g.GameExternalID == "gametest-due" {
			t.Error("marked game must not be due again")
		}
	}

	game, err := repo.GetGame(ctx, "gametest-due")
	if err != nil {
		t.Fatalf("GetGame failed: %v", err)
	}
	if game.ClosingCapturedAt == nil || !game.ClosingCapturedAt.Equal(capturedAt) {
		t.Errorf("closing_captured_at = %v, want %v", game.ClosingCapturedAt, capturedAt)
	}
}
