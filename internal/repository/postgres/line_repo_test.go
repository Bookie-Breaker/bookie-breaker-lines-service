package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

func TestLineRepoInsertDedupAndLatestValues(t *testing.T) {
	ctx := context.Background()
	repo := NewLineRepo(testPool)
	sbID := seedSportsbook(t, "linetest-insert-book", "LineTest Insert", false, true)
	seedGame(t, "linetest-insert", time.Date(2030, 3, 1, 19, 0, 0, 0, time.UTC))

	t0 := time.Date(2030, 2, 28, 12, 0, 0, 0, time.UTC)
	first := []model.LineSnapshot{
		snap("linetest-insert", sbID, "Los Angeles Lakers", nil, -110, t0),
		snap("linetest-insert", sbID, "Boston Celtics", nil, 105, t0),
	}

	inserted, err := repo.InsertLineSnapshots(ctx, first)
	if err != nil {
		t.Fatalf("InsertLineSnapshots failed: %v", err)
	}
	if inserted != 2 {
		t.Errorf("inserted = %d, want 2", inserted)
	}

	// Re-inserting identical rows hits the composite unique index and is a
	// no-op: 0 rows affected, no error.
	inserted, err = repo.InsertLineSnapshots(ctx, first)
	if err != nil {
		t.Fatalf("duplicate insert failed: %v", err)
	}
	if inserted != 0 {
		t.Errorf("duplicate insert = %d rows, want 0", inserted)
	}

	// Empty input never touches the database.
	if n, err := repo.InsertLineSnapshots(ctx, nil); n != 0 || err != nil {
		t.Errorf("InsertLineSnapshots(nil) = (%d, %v), want (0, nil)", n, err)
	}

	// A newer Lakers price becomes the latest value for its key.
	t1 := t0.Add(30 * time.Minute)
	if _, err := repo.InsertLineSnapshots(ctx, []model.LineSnapshot{
		snap("linetest-insert", sbID, "Los Angeles Lakers", nil, -115, t1),
	}); err != nil {
		t.Fatalf("second insert failed: %v", err)
	}

	latest, err := repo.GetLatestLineValues(ctx, []string{"linetest-insert"})
	if err != nil {
		t.Fatalf("GetLatestLineValues failed: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest = %v, want 2 line keys", latest)
	}
	lakersKey := repository.LineKey{
		GameExternalID: "linetest-insert",
		SportsbookID:   sbID,
		MarketType:     model.MarketMoneyline,
		Selection:      "Los Angeles Lakers",
	}
	if v, ok := latest[lakersKey]; !ok || v.OddsAmerican != -115 {
		t.Errorf("latest Lakers = %+v, want the newer -115 snapshot", v)
	}

	// No game ids means an empty map without querying.
	empty, err := repo.GetLatestLineValues(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("GetLatestLineValues(nil) = (%v, %v), want empty", empty, err)
	}
}

func TestLineRepoCurrentLinesFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	repo := NewLineRepo(testPool)
	sbA := seedSportsbook(t, "linetest-cur-a", "LineTest Cur A", false, true)
	sbB := seedSportsbook(t, "linetest-cur-b", "LineTest Cur B", true, true)
	seedGame(t, "linetest-current", time.Date(2030, 3, 5, 19, 0, 0, 0, time.UTC))

	t0 := time.Date(2030, 3, 5, 12, 0, 0, 0, time.UTC)
	liveSnap := snap("linetest-current", sbB, "Boston Celtics", nil, 110, t0)
	liveSnap.IsLive = true
	liveSnap.Source = "sharpapi"
	seedSnaps := []model.LineSnapshot{
		snap("linetest-current", sbA, "Los Angeles Lakers", nil, -110, t0),
		snap("linetest-current", sbA, "Los Angeles Lakers -3.5", fptr(-3.5), -108, t0),
		liveSnap,
	}
	if _, err := repo.InsertLineSnapshots(ctx, seedSnaps); err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}
	// A newer snapshot for the same key: DISTINCT ON must return only it.
	if _, err := repo.InsertLineSnapshots(ctx, []model.LineSnapshot{
		snap("linetest-current", sbA, "Los Angeles Lakers", nil, -120, t0.Add(time.Hour)),
	}); err != nil {
		t.Fatalf("seed newer snapshot: %v", err)
	}

	scope := repository.CurrentLineFilters{GameIDs: []string{"linetest-current"}, Limit: 50}

	t.Run("latest per key", func(t *testing.T) {
		lines, hasMore, err := repo.GetCurrentLines(ctx, scope)
		if err != nil {
			t.Fatalf("GetCurrentLines failed: %v", err)
		}
		if hasMore {
			t.Error("hasMore = true, want false")
		}
		if len(lines) != 3 {
			t.Fatalf("lines = %d, want 3 distinct keys", len(lines))
		}
		for _, l := range lines {
			if l.Selection == "Los Angeles Lakers" && l.OddsAmerican != -120 {
				t.Errorf("Lakers odds = %d, want the newest -120", l.OddsAmerican)
			}
			if l.SportsbookID == sbA && l.SportsbookKey != "linetest-cur-a" {
				t.Errorf("sportsbook key = %q, want joined key", l.SportsbookKey)
			}
		}
	})

	t.Run("market type filter", func(t *testing.T) {
		f := scope
		f.MarketTypes = []string{"SPREAD"}
		lines, _, err := repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("GetCurrentLines failed: %v", err)
		}
		if len(lines) != 1 || lines[0].MarketType != model.MarketSpread {
			t.Errorf("lines = %+v, want the single spread", lines)
		}
		if lines[0].LineValue == nil || *lines[0].LineValue != -3.5 {
			t.Errorf("line value = %v, want -3.5", lines[0].LineValue)
		}
	})

	t.Run("sportsbook filter", func(t *testing.T) {
		f := scope
		f.Sportsbooks = []string{"linetest-cur-b"}
		lines, _, err := repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("GetCurrentLines failed: %v", err)
		}
		if len(lines) != 1 || lines[0].SportsbookID != sbB {
			t.Errorf("lines = %+v, want only book B", lines)
		}
	})

	t.Run("league filter excludes other leagues", func(t *testing.T) {
		f := scope
		f.Leagues = []string{"MLB"}
		lines, _, err := repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("GetCurrentLines failed: %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("lines = %+v, want none for MLB", lines)
		}
	})

	t.Run("is_live filter", func(t *testing.T) {
		live := true
		f := scope
		f.IsLive = &live
		lines, _, err := repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("GetCurrentLines failed: %v", err)
		}
		if len(lines) != 1 || !lines[0].IsLive || lines[0].Source != "sharpapi" {
			t.Errorf("lines = %+v, want the single live snapshot", lines)
		}
	})

	t.Run("date filter", func(t *testing.T) {
		f := scope
		f.Date = "2030-03-05"
		lines, _, err := repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("GetCurrentLines failed: %v", err)
		}
		if len(lines) != 3 {
			t.Errorf("lines = %d, want 3 for the game date", len(lines))
		}

		f.Date = "2029-01-01"
		lines, _, err = repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("GetCurrentLines failed: %v", err)
		}
		if len(lines) != 0 {
			t.Errorf("lines = %d, want none on another date", len(lines))
		}
	})

	t.Run("keyset pagination", func(t *testing.T) {
		f := scope
		f.Limit = 2
		page1, hasMore, err := repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("page 1 failed: %v", err)
		}
		if len(page1) != 2 || !hasMore {
			t.Fatalf("page 1 = %d lines hasMore=%v, want 2/true", len(page1), hasMore)
		}

		last := page1[len(page1)-1]
		f.Cursor = repository.EncodeCursor(repository.LineKey{
			GameExternalID: last.GameExternalID,
			SportsbookID:   last.SportsbookID,
			MarketType:     last.MarketType,
			Selection:      last.Selection,
		})
		page2, hasMore, err := repo.GetCurrentLines(ctx, f)
		if err != nil {
			t.Fatalf("page 2 failed: %v", err)
		}
		if len(page2) != 1 || hasMore {
			t.Fatalf("page 2 = %d lines hasMore=%v, want 1/false", len(page2), hasMore)
		}
		if page2[0].ID == page1[0].ID || page2[0].ID == page1[1].ID {
			t.Error("page 2 must not repeat page 1 rows")
		}
	})

	t.Run("invalid cursor", func(t *testing.T) {
		f := scope
		f.Cursor = "!!!not-a-cursor!!!"
		if _, _, err := repo.GetCurrentLines(ctx, f); !errors.Is(err, repository.ErrInvalidCursor) {
			t.Errorf("err = %v, want ErrInvalidCursor", err)
		}
	})

	t.Run("GetGameLines scopes to the game", func(t *testing.T) {
		lines, _, err := repo.GetGameLines(ctx, "linetest-current", repository.CurrentLineFilters{Limit: 50})
		if err != nil {
			t.Fatalf("GetGameLines failed: %v", err)
		}
		if len(lines) != 3 {
			t.Errorf("lines = %d, want 3", len(lines))
		}
		for _, l := range lines {
			if l.GameExternalID != "linetest-current" {
				t.Errorf("line for %q leaked into game scope", l.GameExternalID)
			}
		}
	})
}

func TestLineRepoGetLineByID(t *testing.T) {
	ctx := context.Background()
	repo := NewLineRepo(testPool)
	sbID := seedSportsbook(t, "linetest-byid-book", "LineTest ByID", false, true)
	seedGame(t, "linetest-byid", time.Date(2030, 4, 1, 19, 0, 0, 0, time.UTC))

	t0 := time.Date(2030, 4, 1, 12, 0, 0, 0, time.UTC)
	if _, err := repo.InsertLineSnapshots(ctx, []model.LineSnapshot{
		snap("linetest-byid", sbID, "Los Angeles Lakers -3.5", fptr(-3.5), -110, t0),
	}); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	lines, _, err := repo.GetGameLines(ctx, "linetest-byid", repository.CurrentLineFilters{})
	if err != nil || len(lines) != 1 {
		t.Fatalf("lookup inserted line: %v (%d lines)", err, len(lines))
	}

	got, err := repo.GetLineByID(ctx, lines[0].ID)
	if err != nil {
		t.Fatalf("GetLineByID failed: %v", err)
	}
	if got.Selection != "Los Angeles Lakers -3.5" || got.SportsbookKey != "linetest-byid-book" {
		t.Errorf("line = %+v, want the seeded spread", got)
	}
	if got.LineValue == nil || *got.LineValue != -3.5 || !got.CapturedAt.Equal(t0) {
		t.Errorf("line = %+v, want value -3.5 at t0", got)
	}

	if _, err := repo.GetLineByID(ctx, "00000000-0000-0000-0000-000000000000"); err == nil {
		t.Error("GetLineByID should fail for an unknown id")
	}
}

func TestLineRepoGetLineMovement(t *testing.T) {
	ctx := context.Background()
	repo := NewLineRepo(testPool)
	sbA := seedSportsbook(t, "linetest-mov-a", "LineTest Mov A", false, true)
	sbB := seedSportsbook(t, "linetest-mov-b", "LineTest Mov B", false, true)
	seedGame(t, "linetest-movement", time.Date(2030, 5, 1, 19, 0, 0, 0, time.UTC))

	t0 := time.Date(2030, 5, 1, 10, 0, 0, 0, time.UTC)
	if _, err := repo.InsertLineSnapshots(ctx, []model.LineSnapshot{
		snap("linetest-movement", sbA, "Los Angeles Lakers -3.5", fptr(-3.5), -110, t0),
		snap("linetest-movement", sbA, "Los Angeles Lakers -3.5", fptr(-3.0), -112, t0.Add(time.Hour)),
		snap("linetest-movement", sbA, "Los Angeles Lakers", nil, -150, t0),
		snap("linetest-movement", sbB, "Los Angeles Lakers -3.5", fptr(-4.0), -105, t0),
	}); err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}

	// Market + sportsbook + selection filters, chronological order.
	lines, err := repo.GetLineMovement(ctx, "linetest-movement", repository.MovementFilters{
		MarketType: "SPREAD",
		Sportsbook: "linetest-mov-a",
		Selection:  "Los Angeles Lakers -3.5",
	})
	if err != nil {
		t.Fatalf("GetLineMovement failed: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want the 2 book-A spread snapshots", len(lines))
	}
	if !lines[0].CapturedAt.Before(lines[1].CapturedAt) {
		t.Error("movement must be in chronological order")
	}
	if *lines[0].LineValue != -3.5 || *lines[1].LineValue != -3.0 {
		t.Errorf("values = %v/%v, want -3.5 then -3.0", *lines[0].LineValue, *lines[1].LineValue)
	}

	// Unfiltered movement returns every snapshot for the game.
	all, err := repo.GetLineMovement(ctx, "linetest-movement", repository.MovementFilters{})
	if err != nil {
		t.Fatalf("GetLineMovement (no filters) failed: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("lines = %d, want all 4 snapshots", len(all))
	}
}

func TestLineRepoClosingLinesLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := NewLineRepo(testPool)
	sbA := seedSportsbook(t, "linetest-close-a", "LineTest Close A", false, true)
	sbB := seedSportsbook(t, "linetest-close-b", "LineTest Close B", false, true)

	commence := time.Date(2030, 6, 1, 19, 0, 0, 0, time.UTC)
	seedGame(t, "linetest-closing", commence)

	preA := snap("linetest-closing", sbA, "Los Angeles Lakers -3.5", fptr(-3.5), -110, commence.Add(-2*time.Hour))
	preA2 := snap("linetest-closing", sbA, "Los Angeles Lakers -3.5", fptr(-3.0), -112, commence.Add(-30*time.Minute))
	preB := snap("linetest-closing", sbB, "Los Angeles Lakers", nil, -150, commence.Add(-time.Hour))
	postA := snap("linetest-closing", sbA, "Los Angeles Lakers -3.5", fptr(-2.5), -115, commence.Add(30*time.Minute))
	if _, err := repo.InsertLineSnapshots(ctx, []model.LineSnapshot{preA, preA2, preB, postA}); err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}

	rows, err := repo.CaptureClosingLines(ctx, "linetest-closing", commence)
	if err != nil {
		t.Fatalf("CaptureClosingLines failed: %v", err)
	}
	if rows != 2 {
		t.Errorf("captured rows = %d, want 2 line keys", rows)
	}

	closing, err := repo.GetClosingLines(ctx, "linetest-closing", repository.ClosingLineFilters{})
	if err != nil {
		t.Fatalf("GetClosingLines failed: %v", err)
	}
	if len(closing) != 2 {
		t.Fatalf("closing = %d rows, want 2", len(closing))
	}
	for _, cl := range closing {
		if cl.SportsbookID == sbA {
			// The latest pre-commence spread (-3.0 at -30m), not the live
			// post-commence one.
			if cl.LineValue == nil || *cl.LineValue != -3.0 || cl.OddsAmerican != -112 {
				t.Errorf("closing A = %+v, want the -3.0/-112 snapshot", cl)
			}
		}
		if cl.CreatedAt.IsZero() {
			t.Error("created_at should be populated")
		}
	}

	// Filters narrow by sportsbook key and market type.
	filtered, err := repo.GetClosingLines(ctx, "linetest-closing", repository.ClosingLineFilters{
		Sportsbook: "linetest-close-b",
		MarketType: "MONEYLINE",
	})
	if err != nil {
		t.Fatalf("filtered GetClosingLines failed: %v", err)
	}
	if len(filtered) != 1 || filtered[0].SportsbookID != sbB {
		t.Errorf("filtered = %+v, want only book B's moneyline", filtered)
	}

	// Re-running after a newer pre-commence snapshot updates in place
	// (idempotent upsert), keeping one row per key.
	preA3 := snap("linetest-closing", sbA, "Los Angeles Lakers -3.5", fptr(-2.0), -118, commence.Add(-5*time.Minute))
	if _, err := repo.InsertLineSnapshots(ctx, []model.LineSnapshot{preA3}); err != nil {
		t.Fatalf("seed newer snapshot: %v", err)
	}
	if _, err := repo.CaptureClosingLines(ctx, "linetest-closing", commence); err != nil {
		t.Fatalf("re-capture failed: %v", err)
	}
	closing, err = repo.GetClosingLines(ctx, "linetest-closing", repository.ClosingLineFilters{Sportsbook: "linetest-close-a"})
	if err != nil {
		t.Fatalf("GetClosingLines after re-capture failed: %v", err)
	}
	if len(closing) != 1 || closing[0].LineValue == nil || *closing[0].LineValue != -2.0 {
		t.Errorf("closing = %+v, want a single updated -2.0 row", closing)
	}
}
