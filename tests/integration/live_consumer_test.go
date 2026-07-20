package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/sharpapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository/postgres"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

// sseStub serves the SharpAPI SSE stream: the first connection emits the
// configured frames then closes mid-stream (no graceful end marker); every
// subsequent connection sends only a keep-alive and closes, so the consumer
// keeps reconnecting.
type sseStub struct {
	mu     sync.Mutex
	conns  int
	frames []sharpapi.Frame
}

func (s *sseStub) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func (s *sseStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.conns++
	n := s.conns
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)

	// Keep-alive comment line: the parser must tolerate it.
	_, _ = fmt.Fprint(w, ": ping\n\n")
	fl.Flush()

	if n != 1 {
		return
	}
	for _, f := range s.frames {
		payload, _ := json.Marshal(f)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		fl.Flush()
	}
	// Handler returns: the connection drops mid-stream and the consumer
	// must reconnect.
}

func liveFrames(commence time.Time) []sharpapi.Frame {
	captured := time.Now().UTC().Truncate(time.Second)
	base := sharpapi.Frame{
		EventID:      "live-wc-1",
		SportKey:     "soccer_fifa_world_cup",
		CommenceTime: commence,
		HomeTeam:     "Argentina",
		AwayTeam:     "France",
		Bookmaker:    sharpapi.Bookmaker{Key: "draftkings", Title: "DraftKings"},
		CapturedAt:   captured,
	}

	h2h := base
	h2h.Market = sharpapi.Market{Key: "h2h", Outcomes: []sharpapi.Outcome{
		{Name: "Argentina", Price: 2.10},
		{Name: "Draw", Price: 3.30},
		{Name: "France", Price: 3.60},
	}}

	spreads := base
	spreads.CapturedAt = captured.Add(time.Second)
	spreads.Market = sharpapi.Market{Key: "spreads", Outcomes: []sharpapi.Outcome{
		{Name: "Argentina", Price: 1.95, Point: fptr(-0.5)},
		{Name: "France", Price: 1.87, Point: fptr(0.5)},
	}}

	totals := base
	totals.CapturedAt = captured.Add(2 * time.Second)
	totals.Market = sharpapi.Market{Key: "totals", Outcomes: []sharpapi.Outcome{
		{Name: "Over", Price: 2.00, Point: fptr(2.5)},
		{Name: "Under", Price: 1.80, Point: fptr(2.5)},
	}}

	return []sharpapi.Frame{h2h, spreads, totals}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() (bool, error), what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := cond()
		if err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestLiveConsumerPersistsAndPublishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Seed sportsbooks.
	_, err := testPool.Exec(ctx,
		`INSERT INTO lines.sportsbooks (name, key, is_sharp, is_active)
		VALUES ('DraftKings', 'draftkings', FALSE, TRUE)
		ON CONFLICT (key) DO NOTHING`)
	if err != nil {
		t.Fatalf("seed sportsbooks: %v", err)
	}

	stub := &sseStub{frames: liveFrames(time.Now().UTC().Add(time.Hour).Truncate(time.Second))}
	stubServer := httptest.NewServer(stub)
	defer stubServer.Close()

	// Subscribe before the consumer starts so no publish is missed.
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

	// The Odds API client is unused by the live path but required to build
	// the ingestion service whose persist path the consumer reuses.
	ingestion := service.NewIngestionService(
		oddsapi.NewClient("unused", "http://127.0.0.1:0"),
		lineRepo, gameRepo, sbRepo, rawRepo, lineCache, publisher,
	)

	consumer := service.NewLiveConsumer(
		sharpapi.NewClient(stubServer.URL, "", nil),
		ingestion, gameRepo, sbRepo, 5,
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		consumer.Run(ctx)
	}()

	// All three frames persist as is_live snapshots: 3 (h2h) + 2 (spreads)
	// + 2 (totals).
	waitFor(t, 20*time.Second, func() (bool, error) {
		var count int
		err := testPool.QueryRow(ctx,
			`SELECT COUNT(*) FROM lines.line_snapshots
			WHERE game_external_id = 'live-wc-1' AND is_live AND source = 'sharpapi'`).Scan(&count)
		return count == 7, err
	}, "7 live snapshots")

	// No rows escaped the live flags.
	var nonLive int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM lines.line_snapshots
		WHERE game_external_id = 'live-wc-1' AND (NOT is_live OR source <> 'sharpapi')`).Scan(&nonLive); err != nil {
		t.Fatal(err)
	}
	if nonLive != 0 {
		t.Errorf("non-live sharpapi snapshots = %d, want 0", nonLive)
	}

	// Game metadata was upserted from the frame.
	var home, away string
	if err := testPool.QueryRow(ctx,
		`SELECT home_team, away_team FROM lines.games WHERE game_external_id = 'live-wc-1'`).Scan(&home, &away); err != nil {
		t.Fatalf("game not upserted: %v", err)
	}
	if home != "Argentina" || away != "France" {
		t.Errorf("game teams = %q/%q, want Argentina/France", home, away)
	}

	// The stream closed mid-stream after the first connection; the consumer
	// reconnects with backoff.
	waitFor(t, 20*time.Second, func() (bool, error) {
		return stub.connections() >= 2, nil
	}, "stream reconnect")

	// Each processed frame published lines.updated with is_live and source.
	type linesUpdated struct {
		Event       string   `json:"event"`
		League      string   `json:"league"`
		GameIDs     []string `json:"game_ids"`
		ChangeCount int      `json:"change_count"`
		IsLive      bool     `json:"is_live"`
		Source      string   `json:"source"`
	}

	received := 0
	timeout := time.After(20 * time.Second)
	for received < 3 {
		select {
		case msg := <-msgs:
			var evt linesUpdated
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if evt.Source != "sharpapi" {
				continue // event from another test's ingestion
			}
			received++
			if !evt.IsLive {
				t.Errorf("event is_live = false, want true: %+v", evt)
			}
			if evt.Event != "lines.updated" {
				t.Errorf("event name = %q, want lines.updated", evt.Event)
			}
			if evt.League != "FIFA_WC" {
				t.Errorf("event league = %q, want FIFA_WC", evt.League)
			}
			if len(evt.GameIDs) != 1 || evt.GameIDs[0] != "live-wc-1" {
				t.Errorf("event game_ids = %v, want [live-wc-1]", evt.GameIDs)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for live events; received %d of 3", received)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("consumer did not stop after context cancel")
	}
}
