package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

// queryLineRepo is a richer LineRepository fake for the query-service paths:
// it counts reads and can fail each method independently.
type queryLineRepo struct {
	repository.LineRepository

	mu         sync.Mutex
	current    []model.LineSnapshot
	hasMore    bool
	currentErr error

	snapshot    *model.LineSnapshot
	snapshotErr error

	movement    []model.LineSnapshot
	movementErr error

	closing    []model.ClosingLine
	closingErr error

	gameLinesCalls int
}

func (f *queryLineRepo) GetCurrentLines(_ context.Context, _ repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	return f.current, f.hasMore, f.currentErr
}

func (f *queryLineRepo) GetGameLines(_ context.Context, _ string, _ repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	f.mu.Lock()
	f.gameLinesCalls++
	f.mu.Unlock()
	return f.current, f.hasMore, f.currentErr
}

func (f *queryLineRepo) gameLinesCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gameLinesCalls
}

func (f *queryLineRepo) GetLineByID(_ context.Context, _ string) (*model.LineSnapshot, error) {
	return f.snapshot, f.snapshotErr
}

func (f *queryLineRepo) GetLineMovement(_ context.Context, _ string, _ repository.MovementFilters) ([]model.LineSnapshot, error) {
	return f.movement, f.movementErr
}

func (f *queryLineRepo) GetClosingLines(_ context.Context, _ string, _ repository.ClosingLineFilters) ([]model.ClosingLine, error) {
	return f.closing, f.closingErr
}

type queryGameRepo struct {
	repository.GameRepository
	games    map[string]model.Game
	gameErr  error
	gamesErr error
}

func (f *queryGameRepo) GetGame(_ context.Context, id string) (*model.Game, error) {
	if f.gameErr != nil {
		return nil, f.gameErr
	}
	if g, ok := f.games[id]; ok {
		return &g, nil
	}
	return nil, pgx.ErrNoRows
}

func (f *queryGameRepo) GetGames(_ context.Context, ids []string) (map[string]model.Game, error) {
	if f.gamesErr != nil {
		return nil, f.gamesErr
	}
	out := make(map[string]model.Game)
	for _, id := range ids {
		if g, ok := f.games[id]; ok {
			out[id] = g
		}
	}
	return out, nil
}

func querySnap(id, selection string, odds int) model.LineSnapshot {
	return model.LineSnapshot{
		ID:             id,
		GameExternalID: "g1",
		SportsbookID:   "sb-1",
		SportsbookKey:  "draftkings",
		MarketType:     model.MarketMoneyline,
		Selection:      selection,
		OddsAmerican:   odds,
		OddsDecimal:    1.91,
		CapturedAt:     ts(0),
	}
}

func TestCurrentLinesEnrichesAndPaginates(t *testing.T) {
	lineRepo := &queryLineRepo{
		current: []model.LineSnapshot{querySnap("l1", "Los Angeles Lakers", -110)},
		hasMore: true,
	}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}
	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, nil)

	lines, hasMore, cursor, err := svc.CurrentLines(context.Background(), repository.CurrentLineFilters{Limit: 1})
	if err != nil {
		t.Fatalf("CurrentLines failed: %v", err)
	}
	if len(lines) != 1 || !hasMore || cursor == "" {
		t.Fatalf("result = (%d lines, %v, %q), want 1/true/cursor", len(lines), hasMore, cursor)
	}
	if lines[0].Side != "HOME" || lines[0].ImpliedProb == 0 {
		t.Errorf("line = %+v, want side and implied probability enriched", lines[0])
	}

	// The cursor decodes back to the last row's key.
	key, err := repository.DecodeCursor(cursor)
	if err != nil || key.GameExternalID != "g1" || key.Selection != "Los Angeles Lakers" {
		t.Errorf("cursor = %+v (%v), want the last line key", key, err)
	}
}

func TestCurrentLinesErrors(t *testing.T) {
	t.Run("repo failure", func(t *testing.T) {
		svc := NewLineQueryService(&queryLineRepo{currentErr: errors.New("db down")}, &queryGameRepo{}, &fakeSBRepo{}, nil)
		if _, _, _, err := svc.CurrentLines(context.Background(), repository.CurrentLineFilters{}); err == nil {
			t.Fatal("expected repo error to propagate")
		}
	})

	t.Run("enrich game lookup failure", func(t *testing.T) {
		lineRepo := &queryLineRepo{current: []model.LineSnapshot{querySnap("l1", "Los Angeles Lakers", -110)}}
		svc := NewLineQueryService(lineRepo, &queryGameRepo{gamesErr: errors.New("db down")}, &fakeSBRepo{}, nil)
		if _, _, _, err := svc.CurrentLines(context.Background(), repository.CurrentLineFilters{}); err == nil {
			t.Fatal("expected enrichment error to propagate")
		}
	})

	t.Run("empty result skips enrichment", func(t *testing.T) {
		svc := NewLineQueryService(&queryLineRepo{}, &queryGameRepo{gamesErr: errors.New("must not be called")}, &fakeSBRepo{}, nil)
		lines, hasMore, cursor, err := svc.CurrentLines(context.Background(), repository.CurrentLineFilters{})
		if err != nil || len(lines) != 0 || hasMore || cursor != "" {
			t.Errorf("empty result = (%v, %v, %q, %v), want clean empty response", lines, hasMore, cursor, err)
		}
	})
}

func TestGameLinesCacheLifecycle(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()

	lineRepo := &queryLineRepo{current: []model.LineSnapshot{querySnap("l1", "Los Angeles Lakers", -110)}}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}
	lineCache := cache.NewLineCache(rdb)
	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, lineCache)

	// First default query misses the cache, reads the repo, and writes back.
	lines, hasMore, _, err := svc.GameLines(ctx, "g1", repository.CurrentLineFilters{Limit: 50}, "")
	if err != nil {
		t.Fatalf("GameLines failed: %v", err)
	}
	if len(lines) != 1 || hasMore {
		t.Fatalf("result = (%d, %v), want one line, no more", len(lines), hasMore)
	}
	if lineRepo.gameLinesCallCount() != 1 {
		t.Fatalf("repo calls = %d, want 1", lineRepo.gameLinesCallCount())
	}
	cached, err := lineCache.GetCurrentLines(ctx, "g1")
	if err != nil || len(cached) != 1 {
		t.Fatalf("cache after read = (%v, %v), want the written-back line", cached, err)
	}

	// Second default query is served from cache: repo call count is stable.
	lines, _, _, err = svc.GameLines(ctx, "g1", repository.CurrentLineFilters{Limit: 50}, "")
	if err != nil {
		t.Fatalf("cached GameLines failed: %v", err)
	}
	if len(lines) != 1 || lineRepo.gameLinesCallCount() != 1 {
		t.Errorf("repo calls = %d after cache hit, want still 1", lineRepo.gameLinesCallCount())
	}

	// Filtered queries bypass the cache entirely.
	if _, _, _, err := svc.GameLines(ctx, "g1", repository.CurrentLineFilters{Limit: 50, MarketTypes: []string{"SPREAD"}}, ""); err != nil {
		t.Fatalf("filtered GameLines failed: %v", err)
	}
	if lineRepo.gameLinesCallCount() != 2 {
		t.Errorf("repo calls = %d, want 2 (filters skip cache)", lineRepo.gameLinesCallCount())
	}
}

func TestGameLinesPaginatesCachedResults(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()

	// Pre-seed a cache entry larger than the page size.
	lineCache := cache.NewLineCache(rdb)
	cachedLines := []model.LineSnapshot{
		querySnap("l1", "Boston Celtics", 105),
		querySnap("l2", "Los Angeles Lakers", -110),
	}
	if err := lineCache.SetCurrentLines(ctx, "g1", cachedLines); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	lineRepo := &queryLineRepo{currentErr: errors.New("repo must not be hit on a cache hit")}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}
	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, lineCache)

	lines, hasMore, cursor, err := svc.GameLines(ctx, "g1", repository.CurrentLineFilters{Limit: 1}, "")
	if err != nil {
		t.Fatalf("GameLines failed: %v", err)
	}
	if len(lines) != 1 || !hasMore || cursor == "" {
		t.Errorf("cached page = (%d, %v, %q), want 1 line with a next cursor", len(lines), hasMore, cursor)
	}

	// A page that fits returns everything with no cursor. Limit 0 exercises
	// the default-limit branch.
	lines, hasMore, cursor, err = svc.GameLines(ctx, "g1", repository.CurrentLineFilters{}, "")
	if err != nil {
		t.Fatalf("GameLines failed: %v", err)
	}
	if len(lines) != 2 || hasMore || cursor != "" {
		t.Errorf("cached full page = (%d, %v, %q), want all lines, no cursor", len(lines), hasMore, cursor)
	}
}

func TestGameLinesSideFilterAndErrors(t *testing.T) {
	lineRepo := &queryLineRepo{current: []model.LineSnapshot{
		querySnap("l1", "Los Angeles Lakers", -110),
		querySnap("l2", "Boston Celtics", 105),
	}}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}
	// Dead-Redis cache: read/write failures are tolerated with a warning.
	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, newDeadLineCache())

	lines, _, _, err := svc.GameLines(context.Background(), "g1", repository.CurrentLineFilters{Limit: 50}, "away")
	if err != nil {
		t.Fatalf("GameLines failed: %v", err)
	}
	if len(lines) != 1 || lines[0].Side != "AWAY" {
		t.Errorf("lines = %+v, want only the AWAY side", lines)
	}

	if _, _, _, err := svc.GameLines(context.Background(), "missing", repository.CurrentLineFilters{}, ""); !errors.Is(err, ErrGameNotFound) {
		t.Errorf("err = %v, want ErrGameNotFound", err)
	}

	lineRepo.currentErr = errors.New("db down")
	if _, _, _, err := svc.GameLines(context.Background(), "g1", repository.CurrentLineFilters{Limit: 50}, ""); err == nil {
		t.Error("expected repo error to propagate")
	}
}

func TestSnapshotFlags(t *testing.T) {
	opening := querySnap("l1", "Los Angeles Lakers", -110)
	later := querySnap("l2", "Los Angeles Lakers", -115)
	later.CapturedAt = ts(30)

	closingLine := model.ClosingLine{
		SportsbookID: "sb-1",
		Selection:    "Los Angeles Lakers",
		OddsAmerican: -115,
		CapturedAt:   ts(30),
	}

	lineRepo := &queryLineRepo{
		snapshot: &later,
		movement: []model.LineSnapshot{opening, later},
		closing:  []model.ClosingLine{closingLine},
	}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}
	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, nil)

	snap, err := svc.Snapshot(context.Background(), "l2")
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snap.IsOpening {
		t.Error("later snapshot must not be flagged opening")
	}
	if !snap.IsClosing {
		t.Error("snapshot matching the materialized closing line must be flagged closing")
	}
	if snap.Side != "HOME" || snap.ImpliedProb == 0 {
		t.Errorf("snapshot = %+v, want derived fields populated", snap)
	}

	// The first snapshot in history is the opening line.
	lineRepo.snapshot = &opening
	snap, err = svc.Snapshot(context.Background(), "l1")
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if !snap.IsOpening {
		t.Error("first snapshot in history must be flagged opening")
	}
	if snap.IsClosing {
		t.Error("non-closing snapshot must not be flagged closing")
	}
}

func TestSnapshotErrors(t *testing.T) {
	base := querySnap("l1", "Los Angeles Lakers", -110)

	t.Run("not found", func(t *testing.T) {
		lineRepo := &queryLineRepo{snapshotErr: fmt.Errorf("get line by id: %w", pgx.ErrNoRows)}
		svc := NewLineQueryService(lineRepo, &queryGameRepo{}, &fakeSBRepo{}, nil)
		if _, err := svc.Snapshot(context.Background(), "missing"); !errors.Is(err, ErrSnapshotNotFound) {
			t.Errorf("err = %v, want ErrSnapshotNotFound", err)
		}
	})

	t.Run("generic lookup failure", func(t *testing.T) {
		lineRepo := &queryLineRepo{snapshotErr: errors.New("db down")}
		svc := NewLineQueryService(lineRepo, &queryGameRepo{}, &fakeSBRepo{}, nil)
		if _, err := svc.Snapshot(context.Background(), "l1"); err == nil || errors.Is(err, ErrSnapshotNotFound) {
			t.Errorf("err = %v, want the raw error", err)
		}
	})

	t.Run("game lookup failure", func(t *testing.T) {
		lineRepo := &queryLineRepo{snapshot: &base}
		svc := NewLineQueryService(lineRepo, &queryGameRepo{gameErr: errors.New("db down")}, &fakeSBRepo{}, nil)
		if _, err := svc.Snapshot(context.Background(), "l1"); err == nil {
			t.Error("non-ErrNoRows game errors must propagate")
		}
	})

	t.Run("movement failure", func(t *testing.T) {
		lineRepo := &queryLineRepo{snapshot: &base, movementErr: errors.New("db down")}
		svc := NewLineQueryService(lineRepo, &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}, &fakeSBRepo{}, nil)
		if _, err := svc.Snapshot(context.Background(), "l1"); err == nil {
			t.Error("movement errors must propagate")
		}
	})

	t.Run("closing failure", func(t *testing.T) {
		lineRepo := &queryLineRepo{snapshot: &base, closingErr: errors.New("db down")}
		svc := NewLineQueryService(lineRepo, &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}, &fakeSBRepo{}, nil)
		if _, err := svc.Snapshot(context.Background(), "l1"); err == nil {
			t.Error("closing errors must propagate")
		}
	})
}

func TestMovementOddsOnlyLines(t *testing.T) {
	// Moneyline movement has no line values: total movement derives from the
	// odds delta instead.
	first := querySnap("l1", "Los Angeles Lakers", -110)
	second := querySnap("l2", "Los Angeles Lakers", -120)
	second.CapturedAt = ts(30)

	lineRepo := &queryLineRepo{movement: []model.LineSnapshot{first, second}}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}
	svc := NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, nil)

	movements, err := svc.Movement(context.Background(), "g1", repository.MovementFilters{MarketType: "MONEYLINE"})
	if err != nil {
		t.Fatalf("Movement failed: %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("movements = %d, want 1 group", len(movements))
	}
	m := movements[0]
	if m.TotalMovement == nil || *m.TotalMovement != -10 {
		t.Errorf("total movement = %v, want odds delta -10", m.TotalMovement)
	}

	// Unchanged odds leave total movement unset.
	lineRepo.movement = []model.LineSnapshot{first, first}
	movements, err = svc.Movement(context.Background(), "g1", repository.MovementFilters{MarketType: "MONEYLINE"})
	if err != nil {
		t.Fatalf("Movement failed: %v", err)
	}
	if movements[0].TotalMovement != nil {
		t.Errorf("total movement = %v, want nil for unchanged odds", movements[0].TotalMovement)
	}
}

func TestMovementRepoErrors(t *testing.T) {
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}

	if _, err := NewLineQueryService(&queryLineRepo{movementErr: errors.New("db down")}, gameRepo, &fakeSBRepo{}, nil).
		Movement(context.Background(), "g1", repository.MovementFilters{}); err == nil {
		t.Error("movement errors must propagate")
	}
	if _, err := NewLineQueryService(&queryLineRepo{closingErr: errors.New("db down")}, gameRepo, &fakeSBRepo{}, nil).
		Movement(context.Background(), "g1", repository.MovementFilters{}); err == nil {
		t.Error("closing errors must propagate")
	}
}

func TestBestSelectionFilters(t *testing.T) {
	lines := []model.LineSnapshot{
		querySnap("l1", "Los Angeles Lakers", -110),
		querySnap("l2", "Boston Celtics", 105),
	}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}

	t.Run("side keyword", func(t *testing.T) {
		svc := NewLineQueryService(&queryLineRepo{current: lines}, gameRepo, &fakeSBRepo{}, nil)
		best, err := svc.Best(context.Background(), "g1", "", "home")
		if err != nil {
			t.Fatalf("Best failed: %v", err)
		}
		if len(best) != 1 || best[0].Side != "HOME" {
			t.Errorf("best = %+v, want the HOME group only", best)
		}
	})

	t.Run("exact selection", func(t *testing.T) {
		svc := NewLineQueryService(&queryLineRepo{current: lines}, gameRepo, &fakeSBRepo{}, nil)
		best, err := svc.Best(context.Background(), "g1", "", "Boston Celtics")
		if err != nil {
			t.Fatalf("Best failed: %v", err)
		}
		if len(best) != 1 || best[0].Selection != "Boston Celtics" {
			t.Errorf("best = %+v, want the Celtics selection only", best)
		}
	})

	t.Run("lower odds do not displace the best price", func(t *testing.T) {
		worse := querySnap("l3", "Los Angeles Lakers", -120)
		worse.SportsbookID = "sb-2"
		worse.OddsDecimal = 1.83
		svc := NewLineQueryService(&queryLineRepo{current: []model.LineSnapshot{lines[0], worse}}, gameRepo, &fakeSBRepo{}, nil)
		best, err := svc.Best(context.Background(), "g1", "", "")
		if err != nil {
			t.Fatalf("Best failed: %v", err)
		}
		if len(best) != 1 || best[0].SportsbookID != "sb-1" || best[0].BestOddsDecimal != 1.91 {
			t.Errorf("best = %+v, want sb-1 at 1.91 kept", best)
		}
	})

	t.Run("errors propagate", func(t *testing.T) {
		if _, err := NewLineQueryService(&queryLineRepo{currentErr: errors.New("db down")}, gameRepo, &fakeSBRepo{}, nil).
			Best(context.Background(), "g1", "", ""); err == nil {
			t.Error("repo errors must propagate")
		}
		if _, err := NewLineQueryService(&queryLineRepo{current: lines}, gameRepo, &erroringSBRepo{}, nil).
			Best(context.Background(), "g1", "", ""); err == nil || !strings.Contains(err.Error(), "fetch sportsbooks") {
			t.Error("sportsbook name lookup errors must propagate")
		}
		if _, err := NewLineQueryService(&queryLineRepo{current: lines}, &queryGameRepo{}, &fakeSBRepo{}, nil).
			Best(context.Background(), "missing", "", ""); !errors.Is(err, ErrGameNotFound) {
			t.Error("unknown games must map to ErrGameNotFound")
		}
	})
}

type erroringSBRepo struct {
	repository.SportsbookRepository
}

func (erroringSBRepo) GetAll(_ context.Context, _, _ *bool) ([]model.Sportsbook, error) {
	return nil, errors.New("db down")
}

func TestClosingQueries(t *testing.T) {
	closing := []model.ClosingLine{{
		GameExternalID: "g1",
		SportsbookID:   "sb-1",
		MarketType:     model.MarketSpread,
		Selection:      "Los Angeles Lakers -3.5",
		OddsAmerican:   -110,
	}}
	gameRepo := &queryGameRepo{games: map[string]model.Game{"g1": testGame()}}
	svc := NewLineQueryService(&queryLineRepo{closing: closing}, gameRepo, &fakeSBRepo{}, nil)

	got, err := svc.Closing(context.Background(), "g1", repository.ClosingLineFilters{})
	if err != nil {
		t.Fatalf("Closing failed: %v", err)
	}
	if len(got) != 1 || got[0].Selection != "Los Angeles Lakers -3.5" {
		t.Errorf("closing = %+v, want the stored closing line", got)
	}

	if _, err := svc.Closing(context.Background(), "missing", repository.ClosingLineFilters{}); !errors.Is(err, ErrGameNotFound) {
		t.Errorf("err = %v, want ErrGameNotFound", err)
	}
}

func TestGetGameWrapsGenericErrors(t *testing.T) {
	svc := NewLineQueryService(&queryLineRepo{}, &queryGameRepo{gameErr: errors.New("db down")}, &fakeSBRepo{}, nil)
	_, err := svc.Movement(context.Background(), "g1", repository.MovementFilters{})
	if err == nil || errors.Is(err, ErrGameNotFound) || !strings.Contains(err.Error(), "lookup game") {
		t.Errorf("err = %v, want wrapped lookup error", err)
	}
}

func TestIsSideKeyword(t *testing.T) {
	for _, keyword := range []string{"home", "AWAY", "Draw", "over", "UNDER", "yes", "no"} {
		if !isSideKeyword(keyword) {
			t.Errorf("isSideKeyword(%q) = false, want true", keyword)
		}
	}
	for _, other := range []string{"", "Boston Celtics", "middle"} {
		if isSideKeyword(other) {
			t.Errorf("isSideKeyword(%q) = true, want false", other)
		}
	}
}

func TestNextCursorEdgeCases(t *testing.T) {
	if got := nextCursor(nil, true); got != "" {
		t.Errorf("nextCursor(nil, true) = %q, want empty", got)
	}
	if got := nextCursor([]model.LineSnapshot{querySnap("l1", "x", -110)}, false); got != "" {
		t.Errorf("nextCursor(_, false) = %q, want empty", got)
	}
}
