package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository/postgres"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/server"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

func fptr(f float64) *float64 { return &f }

// oddsStub serves a swappable Odds API payload.
type oddsStub struct {
	mu     sync.Mutex
	events []oddsapi.Event
}

func (s *oddsStub) set(events []oddsapi.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = events
}

func (s *oddsStub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-requests-remaining", "400")
		_ = json.NewEncoder(w).Encode(s.events)
	})
}

func fixtureEvents(commence time.Time, lakersSpreadPrice float64) []oddsapi.Event {
	markets := func(home, away string, homeSpreadPrice float64) []oddsapi.Market {
		return []oddsapi.Market{
			{Key: "h2h", Outcomes: []oddsapi.Outcome{
				{Name: home, Price: 1.71},
				{Name: away, Price: 2.20},
			}},
			{Key: "spreads", Outcomes: []oddsapi.Outcome{
				{Name: home, Price: homeSpreadPrice, Point: fptr(-3.5)},
				{Name: away, Price: 1.91, Point: fptr(3.5)},
			}},
			{Key: "totals", Outcomes: []oddsapi.Outcome{
				{Name: "Over", Price: 1.91, Point: fptr(220.5)},
				{Name: "Under", Price: 1.91, Point: fptr(220.5)},
			}},
		}
	}

	return []oddsapi.Event{
		{
			ID:           "game-1",
			SportKey:     "basketball_nba",
			CommenceTime: commence,
			HomeTeam:     "Los Angeles Lakers",
			AwayTeam:     "Boston Celtics",
			Bookmakers: []oddsapi.Bookmaker{
				{Key: "draftkings", Title: "DraftKings", Markets: markets("Los Angeles Lakers", "Boston Celtics", lakersSpreadPrice)},
			},
		},
		{
			ID:           "game-2",
			SportKey:     "basketball_nba",
			CommenceTime: commence,
			HomeTeam:     "Golden State Warriors",
			AwayTeam:     "Denver Nuggets",
			Bookmakers: []oddsapi.Bookmaker{
				{Key: "draftkings", Title: "DraftKings", Markets: markets("Golden State Warriors", "Denver Nuggets", 1.91)},
			},
		},
	}
}

type envelope struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		RequestID  string `json:"request_id"`
		Pagination *struct {
			Limit      int    `json:"limit"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		} `json:"pagination"`
	} `json:"meta"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func doRequest(t *testing.T, e http.Handler, method, target string) (int, envelope) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response %s %s is not an envelope: %v\n%s", method, target, err, rec.Body.String())
	}
	return rec.Code, env
}

func TestIngestionAndReadAPI(t *testing.T) {
	ctx := context.Background()

	// Seed sportsbooks.
	_, err := testPool.Exec(ctx,
		`INSERT INTO lines.sportsbooks (name, key, is_sharp, is_active)
		VALUES ('DraftKings', 'draftkings', FALSE, TRUE), ('Pinnacle', 'pinnacle', TRUE, TRUE)
		ON CONFLICT (key) DO NOTHING`)
	if err != nil {
		t.Fatalf("seed sportsbooks: %v", err)
	}

	stub := &oddsStub{}
	stubServer := httptest.NewServer(stub.handler())
	defer stubServer.Close()

	commence := time.Now().UTC().Add(6 * time.Hour).Truncate(time.Second)
	stub.set(fixtureEvents(commence, 1.91))

	lineRepo := postgres.NewLineRepo(testPool)
	gameRepo := postgres.NewGameRepo(testPool)
	sbRepo := postgres.NewSportsbookRepo(testPool)
	rawRepo := postgres.NewRawResponseRepo(testPool)
	lineCache := cache.NewLineCache(testRedis)
	publisher := pubsub.NewPublisher(testRedis)

	client := oddsapi.NewClient("test-key", stubServer.URL)
	ingestion := service.NewIngestionService(client, lineRepo, gameRepo, sbRepo, rawRepo, lineCache, publisher)
	closing := service.NewClosingLineService(lineRepo, gameRepo)
	query := service.NewLineQueryService(lineRepo, gameRepo, sbRepo, lineCache)

	e := server.New(server.Deps{
		DB:               testPool,
		Redis:            testRedis,
		Query:            query,
		Ingestion:        ingestion,
		SportsbookRepo:   sbRepo,
		SportKeys:        []string{"basketball_nba"},
		IngestionEnabled: true,
	})

	t.Run("first ingest persists snapshots and game metadata", func(t *testing.T) {
		result, err := ingestion.Ingest(ctx, "basketball_nba")
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if result.LinesIngested != 12 {
			t.Errorf("lines_ingested = %d, want 12", result.LinesIngested)
		}
		if result.GamesFound != 2 {
			t.Errorf("games_found = %d, want 2", result.GamesFound)
		}

		var games int
		if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM lines.games`).Scan(&games); err != nil {
			t.Fatal(err)
		}
		if games != 2 {
			t.Errorf("lines.games rows = %d, want 2", games)
		}
	})

	t.Run("identical second ingest is fully deduplicated", func(t *testing.T) {
		result, err := ingestion.Ingest(ctx, "basketball_nba")
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if result.LinesIngested != 0 {
			t.Errorf("lines_ingested = %d, want 0", result.LinesIngested)
		}
		if result.LinesSkipped != 12 {
			t.Errorf("lines_skipped = %d, want 12", result.LinesSkipped)
		}
	})

	t.Run("changed odds insert exactly the changed snapshot", func(t *testing.T) {
		stub.set(fixtureEvents(commence, 1.87)) // Lakers spread price moves
		result, err := ingestion.Ingest(ctx, "basketball_nba")
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if result.LinesIngested != 1 {
			t.Errorf("lines_ingested = %d, want 1", result.LinesIngested)
		}
		if result.LinesSkipped != 11 {
			t.Errorf("lines_skipped = %d, want 11", result.LinesSkipped)
		}
	})

	t.Run("GET /current returns enveloped lines with derived fields", func(t *testing.T) {
		code, env := doRequest(t, e, http.MethodGet, "/api/v1/lines/current?league=NBA")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}

		var lines []map[string]any
		if err := json.Unmarshal(env.Data, &lines); err != nil {
			t.Fatalf("decode data: %v", err)
		}
		if len(lines) != 12 {
			t.Fatalf("len(data) = %d, want 12 latest lines", len(lines))
		}
		if env.Meta.Pagination == nil {
			t.Fatal("meta.pagination missing")
		}

		for _, l := range lines {
			if l["implied_probability"] == nil || l["implied_probability"].(float64) <= 0 {
				t.Fatalf("implied_probability missing on %v", l["selection"])
			}
			if l["side"] == nil || l["side"] == "" {
				t.Fatalf("side missing on line %v", l)
			}
		}
	})

	t.Run("cursor pagination pages through all lines without duplicates", func(t *testing.T) {
		seen := make(map[string]bool)
		cursor := ""
		pages := 0

		for {
			target := "/api/v1/lines/current?limit=5"
			if cursor != "" {
				target += "&cursor=" + cursor
			}
			code, env := doRequest(t, e, http.MethodGet, target)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}

			var lines []map[string]any
			if err := json.Unmarshal(env.Data, &lines); err != nil {
				t.Fatal(err)
			}
			for _, l := range lines {
				id := l["id"].(string)
				if seen[id] {
					t.Fatalf("duplicate snapshot %s across pages", id)
				}
				seen[id] = true
			}

			pages++
			if pages > 10 {
				t.Fatal("pagination did not terminate")
			}
			if env.Meta.Pagination == nil || !env.Meta.Pagination.HasMore {
				break
			}
			cursor = env.Meta.Pagination.NextCursor
		}

		if len(seen) != 12 {
			t.Errorf("paginated total = %d, want 12", len(seen))
		}
	})

	t.Run("invalid cursor returns 400", func(t *testing.T) {
		code, env := doRequest(t, e, http.MethodGet, "/api/v1/lines/current?cursor=!!!bad")
		if code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", code)
		}
		if env.Error == nil || env.Error.Code != "INVALID_CURSOR" {
			t.Errorf("error code = %v, want INVALID_CURSOR", env.Error)
		}
	})

	t.Run("GET /game/{id} serves lines and 404s unknown games", func(t *testing.T) {
		code, env := doRequest(t, e, http.MethodGet, "/api/v1/lines/game/game-1")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		var lines []map[string]any
		if err := json.Unmarshal(env.Data, &lines); err != nil {
			t.Fatal(err)
		}
		if len(lines) != 6 {
			t.Errorf("len(data) = %d, want 6", len(lines))
		}

		code, env = doRequest(t, e, http.MethodGet, "/api/v1/lines/game/nope")
		if code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", code)
		}
		if env.Error == nil || env.Error.Code != "NOT_FOUND" {
			t.Errorf("error code = %v, want NOT_FOUND", env.Error)
		}
	})

	t.Run("GET /movement aggregates opening and current", func(t *testing.T) {
		code, env := doRequest(t, e, http.MethodGet, "/api/v1/lines/game/game-1/movement?market_type=SPREAD")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}

		var movements []map[string]any
		if err := json.Unmarshal(env.Data, &movements); err != nil {
			t.Fatal(err)
		}
		if len(movements) != 2 {
			t.Fatalf("movement groups = %d, want 2 (home + away)", len(movements))
		}

		for _, m := range movements {
			snaps := m["line_snapshots"].([]any)
			if m["selection"] == "Los Angeles Lakers -3.5" && len(snaps) != 2 {
				t.Errorf("Lakers spread should have 2 snapshots, got %d", len(snaps))
			}
			if m["opening_odds"] == nil || m["current_odds"] == nil {
				t.Errorf("opening/current odds missing on %v", m["selection"])
			}
		}
	})

	t.Run("GET /best picks the highest decimal odds", func(t *testing.T) {
		code, env := doRequest(t, e, http.MethodGet, "/api/v1/lines/game/game-1/best?market_type=TOTAL")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		var best []map[string]any
		if err := json.Unmarshal(env.Data, &best); err != nil {
			t.Fatal(err)
		}
		if len(best) != 2 {
			t.Errorf("best lines = %d, want 2 (over + under)", len(best))
		}
	})

	t.Run("closing capture is triggered by commence time and idempotent", func(t *testing.T) {
		// Simulate a game that started an hour ago with snapshots taken
		// before tip-off (closing capture only considers pre-commence rows).
		_, err := testPool.Exec(ctx,
			`UPDATE lines.games SET commence_time = NOW() - INTERVAL '1 hour' WHERE game_external_id = 'game-2'`)
		if err != nil {
			t.Fatal(err)
		}
		// Hypertable chunks cannot be moved by UPDATE, so insert backdated
		// copies of the snapshots to stand in for pre-game observations.
		_, err = testPool.Exec(ctx,
			`INSERT INTO lines.line_snapshots
				(game_external_id, sportsbook_id, league, market_type, selection, line_value, odds_american, odds_decimal, is_live, captured_at, source)
			SELECT game_external_id, sportsbook_id, league, market_type, selection, line_value, odds_american, odds_decimal, is_live, captured_at - INTERVAL '3 hours', source
			FROM lines.line_snapshots WHERE game_external_id = 'game-2'`)
		if err != nil {
			t.Fatal(err)
		}

		closing.CaptureDue(ctx)

		code, env := doRequest(t, e, http.MethodGet, "/api/v1/lines/game/game-2/closing")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		var closingLines []map[string]any
		if err := json.Unmarshal(env.Data, &closingLines); err != nil {
			t.Fatal(err)
		}
		if len(closingLines) != 6 {
			t.Fatalf("closing lines = %d, want 6", len(closingLines))
		}

		var capturedAt *time.Time
		if err := testPool.QueryRow(ctx,
			`SELECT closing_captured_at FROM lines.games WHERE game_external_id = 'game-2'`).Scan(&capturedAt); err != nil {
			t.Fatal(err)
		}
		if capturedAt == nil {
			t.Fatal("closing_captured_at not set")
		}

		// Second sweep finds nothing due and changes nothing.
		closing.CaptureDue(ctx)
		var count int
		if err := testPool.QueryRow(ctx,
			`SELECT COUNT(*) FROM lines.closing_lines WHERE game_external_id = 'game-2'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 6 {
			t.Errorf("closing lines after second sweep = %d, want 6", count)
		}
	})

	t.Run("GET /sportsbooks filters by is_sharp", func(t *testing.T) {
		code, env := doRequest(t, e, http.MethodGet, "/api/v1/lines/sportsbooks?is_sharp=true")
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		var books []map[string]any
		if err := json.Unmarshal(env.Data, &books); err != nil {
			t.Fatal(err)
		}
		if len(books) != 1 || books[0]["key"] != "pinnacle" {
			t.Errorf("sharp books = %v, want [pinnacle]", books)
		}
	})
}
