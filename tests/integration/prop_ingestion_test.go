package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository/postgres"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

// propAPIStub serves both Odds API endpoints the prop path uses: the events
// list and the per-event odds (a single Event object, not an array).
type propAPIStub struct {
	mu        sync.Mutex
	events    []oddsapi.EventStub
	eventOdds map[string]oddsapi.Event
	oddsCalls []string // event IDs fetched via the per-event endpoint
}

func (s *propAPIStub) oddsCallIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.oddsCalls...)
}

func (s *propAPIStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-requests-used", "10")
	w.Header().Set("x-requests-remaining", "490")

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /v4/sports/{sport}/events
	if len(parts) == 4 && parts[3] == "events" {
		_ = json.NewEncoder(w).Encode(s.events)
		return
	}
	// /v4/sports/{sport}/events/{id}/odds
	if len(parts) == 6 && parts[5] == "odds" {
		eventID := parts[4]
		s.oddsCalls = append(s.oddsCalls, eventID)
		event, ok := s.eventOdds[eventID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"unknown event"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(event)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func TestPropIngestionPersistsAndPublishes(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Exec(ctx,
		`INSERT INTO lines.sportsbooks (name, key, is_sharp, is_active)
		VALUES ('DraftKings', 'draftkings', FALSE, TRUE)
		ON CONFLICT (key) DO NOTHING`)
	if err != nil {
		t.Fatalf("seed sportsbooks: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	inWindow := oddsapi.EventStub{
		ID:           "prop-wc-1",
		SportKey:     "soccer_fifa_world_cup",
		CommenceTime: now.Add(6 * time.Hour),
		HomeTeam:     "France",
		AwayTeam:     "Norway",
	}
	stub := &propAPIStub{
		events: []oddsapi.EventStub{
			{ID: "prop-wc-started", SportKey: "soccer_fifa_world_cup", CommenceTime: now.Add(-time.Hour), HomeTeam: "Spain", AwayTeam: "Italy"},
			inWindow,
			{ID: "prop-wc-far", SportKey: "soccer_fifa_world_cup", CommenceTime: now.Add(200 * time.Hour), HomeTeam: "Brazil", AwayTeam: "Germany"},
		},
		eventOdds: map[string]oddsapi.Event{
			"prop-wc-1": {
				ID:           "prop-wc-1",
				SportKey:     "soccer_fifa_world_cup",
				CommenceTime: inWindow.CommenceTime,
				HomeTeam:     "France",
				AwayTeam:     "Norway",
				Bookmakers: []oddsapi.Bookmaker{{
					Key:   "draftkings",
					Title: "DraftKings",
					Markets: []oddsapi.Market{
						{
							Key: "player_goal_scorer_anytime",
							Outcomes: []oddsapi.Outcome{
								{Name: "Yes", Description: "Kylian Mbappé", Price: 2.50},
								{Name: "No", Description: "Kylian Mbappé", Price: 1.55},
							},
						},
						{
							Key: "player_shots",
							Outcomes: []oddsapi.Outcome{
								{Name: "Over", Description: "Erling Haaland", Price: 1.87, Point: fptr(2.5)},
								{Name: "Under", Description: "Erling Haaland", Price: 1.95, Point: fptr(2.5)},
							},
						},
					},
				}},
			},
		},
	}
	stubServer := httptest.NewServer(stub)
	defer stubServer.Close()

	// Subscribe before ingesting so no publish is missed.
	sub := testRedis.Subscribe(ctx, "events:lines.updated")
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	msgs := sub.Channel()

	lineRepo := postgres.NewLineRepo(testPool)
	gameRepo := postgres.NewGameRepo(testPool)
	sbRepo := postgres.NewSportsbookRepo(testPool)
	rawRepo := postgres.NewRawResponseRepo(testPool)
	lineCache := cache.NewLineCache(testRedis)
	publisher := pubsub.NewPublisher(testRedis)

	client := oddsapi.NewClient("test-key", stubServer.URL)
	ingestion := service.NewIngestionService(client, lineRepo, gameRepo, sbRepo, rawRepo, lineCache, publisher)
	props := service.NewPropIngestionService(client, ingestion, gameRepo, sbRepo, rawRepo, 48*time.Hour, 10)

	result, err := props.IngestProps(ctx, "soccer_fifa_world_cup")
	if err != nil {
		t.Fatalf("IngestProps failed: %v", err)
	}

	if result.EventsListed != 3 {
		t.Errorf("events listed = %d, want 3", result.EventsListed)
	}
	if result.EventsFetched != 1 {
		t.Errorf("events fetched = %d, want 1 (window filters the rest)", result.EventsFetched)
	}
	if result.LinesIngested != 4 {
		t.Errorf("lines ingested = %d, want 4", result.LinesIngested)
	}

	// Only the in-window event spent a per-event request.
	if calls := stub.oddsCallIDs(); len(calls) != 1 || calls[0] != "prop-wc-1" {
		t.Errorf("per-event odds calls = %v, want [prop-wc-1]", calls)
	}

	// Prop rows landed with the Wave 0 prop columns populated.
	rows, err := testPool.Query(ctx,
		`SELECT selection, market_type, stat_type, prop_type, player_external_id, line_value
		FROM lines.line_snapshots WHERE game_external_id = 'prop-wc-1'
		ORDER BY selection`)
	if err != nil {
		t.Fatalf("query prop rows: %v", err)
	}
	defer rows.Close()

	type propRow struct {
		marketType, statType, propType, playerID string
		lineValue                                *float64
	}
	got := map[string]propRow{}
	for rows.Next() {
		var selection string
		var r propRow
		if err := rows.Scan(&selection, &r.marketType, &r.statType, &r.propType, &r.playerID, &r.lineValue); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[selection] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("persisted %d prop rows, want 4: %v", len(got), got)
	}

	mbappeYes, ok := got["Kylian Mbappé Anytime Goalscorer Yes"]
	if !ok {
		t.Fatalf("missing goalscorer Yes row; got %v", got)
	}
	if mbappeYes.marketType != "PLAYER_PROP" || mbappeYes.statType != "player_goal_scorer_anytime" ||
		mbappeYes.propType != "YES_NO" || mbappeYes.playerID != "kylian-mbappe" {
		t.Errorf("goalscorer Yes row = %+v", mbappeYes)
	}
	if mbappeYes.lineValue != nil {
		t.Errorf("goalscorer line value = %v, want NULL", *mbappeYes.lineValue)
	}

	haalandOver, ok := got["Erling Haaland Over 2.5"]
	if !ok {
		t.Fatalf("missing shots Over row; got %v", got)
	}
	if haalandOver.statType != "player_shots" || haalandOver.propType != "OVER_UNDER" ||
		haalandOver.playerID != "erling-haaland" {
		t.Errorf("shots Over row = %+v", haalandOver)
	}
	if haalandOver.lineValue == nil || *haalandOver.lineValue != 2.5 {
		t.Errorf("shots Over line value = %v, want 2.5", haalandOver.lineValue)
	}

	// Game metadata came from the events list.
	var home, away string
	if err := testPool.QueryRow(ctx,
		`SELECT home_team, away_team FROM lines.games WHERE game_external_id = 'prop-wc-1'`).Scan(&home, &away); err != nil {
		t.Fatalf("game not upserted: %v", err)
	}
	if home != "France" || away != "Norway" {
		t.Errorf("game teams = %q/%q, want France/Norway", home, away)
	}

	// Exactly one lines.updated publish for the cycle, carrying prop market
	// types and the pre-game source.
	type linesUpdated struct {
		Event       string   `json:"event"`
		League      string   `json:"league"`
		GameIDs     []string `json:"game_ids"`
		MarketTypes []string `json:"market_types"`
		ChangeCount int      `json:"change_count"`
		IsLive      bool     `json:"is_live"`
		Source      string   `json:"source"`
	}
	isOurs := func(evt linesUpdated) bool {
		for _, id := range evt.GameIDs {
			if id == "prop-wc-1" {
				return true
			}
		}
		return false
	}

	var evt linesUpdated
	received := 0
	deadline := time.After(10 * time.Second)
	for received == 0 {
		select {
		case msg := <-msgs:
			var e linesUpdated
			if err := json.Unmarshal([]byte(msg.Payload), &e); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if !isOurs(e) {
				continue // event from another test
			}
			received++
			evt = e
		case <-deadline:
			t.Fatal("timed out waiting for lines.updated event")
		}
	}

	if evt.Event != "lines.updated" || evt.Source != "the_odds_api" || evt.IsLive {
		t.Errorf("event = %+v, want lines.updated from the_odds_api, not live", evt)
	}
	if evt.League != "FIFA_WC" {
		t.Errorf("event league = %q, want FIFA_WC", evt.League)
	}
	if len(evt.MarketTypes) != 1 || evt.MarketTypes[0] != "PLAYER_PROP" {
		t.Errorf("event market types = %v, want [PLAYER_PROP]", evt.MarketTypes)
	}
	if evt.ChangeCount != 4 {
		t.Errorf("event change count = %d, want 4", evt.ChangeCount)
	}

	// The whole cycle publishes once: no second event for this game.
	select {
	case msg := <-msgs:
		var e linesUpdated
		if err := json.Unmarshal([]byte(msg.Payload), &e); err == nil && isOurs(e) {
			t.Errorf("unexpected second publish for prop cycle: %+v", e)
		}
	case <-time.After(2 * time.Second):
	}

	// Raw per-event responses were archived (asynchronously).
	waitFor(t, 10*time.Second, func() (bool, error) {
		var count int
		err := testPool.QueryRow(ctx,
			`SELECT COUNT(*) FROM public.raw_api_responses
			WHERE endpoint = '/v4/sports/soccer_fifa_world_cup/events/prop-wc-1/odds'`).Scan(&count)
		return count == 1, err
	}, "archived raw prop response")
}
