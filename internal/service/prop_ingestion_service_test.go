package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

// newPropFixture wires a PropIngestionService onto an existing ingestion
// service with fresh fakes, using the defaults for window and cap.
func newPropFixture(t *testing.T, srvURL string, ingestion *IngestionService) *PropIngestionService {
	t.Helper()
	return NewPropIngestionService(
		oddsapi.NewClient("k", srvURL),
		ingestion,
		&ingestGameRepo{},
		&ingestSBRepo{books: []model.Sportsbook{{ID: "sb-uuid-1", Key: "draftkings"}}},
		newIngestRawRepo(),
		0, 0,
	)
}

// propStubServer serves the events list and per-event prop odds endpoints.
func propStubServer(t *testing.T, events string, eventOdds map[string]struct {
	status int
	body   string
}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			_, _ = w.Write([]byte(events))
			return
		}
		for id, resp := range eventOdds {
			if strings.Contains(r.URL.Path, "/events/"+id+"/odds") {
				w.WriteHeader(resp.status)
				_, _ = w.Write([]byte(resp.body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func eventStubJSON(id string, commence time.Time) string {
	return fmt.Sprintf(`{"id":%q,"sport_key":"soccer_epl","home_team":"Arsenal","away_team":"Chelsea","commence_time":%q}`,
		id, commence.Format(time.RFC3339))
}

func propOddsJSON(id string) string {
	return fmt.Sprintf(`{
		"id": %q,
		"sport_key": "soccer_epl",
		"home_team": "Arsenal",
		"away_team": "Chelsea",
		"bookmakers": [{
			"key": "draftkings",
			"markets": [{
				"key": "player_shots",
				"outcomes": [
					{"name": "Over", "description": "Bukayo Saka", "price": 1.85, "point": 2.5},
					{"name": "Under", "description": "Bukayo Saka", "price": 1.95, "point": 2.5}
				]
			}]
		}]
	}`, id)
}

func TestIngestPropsHappyPath(t *testing.T) {
	now := time.Now().UTC()
	events := fmt.Sprintf(`[%s,%s,%s]`,
		eventStubJSON("evA", now.Add(2*time.Hour)),   // in window
		eventStubJSON("evB", now.Add(-time.Hour)),    // already started
		eventStubJSON("evC", now.Add(100*time.Hour)), // beyond the 48h window
	)
	srv := propStubServer(t, events, map[string]struct {
		status int
		body   string
	}{
		"evA": {http.StatusOK, propOddsJSON("evA")},
	})

	ingestion, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	gameRepo := &ingestGameRepo{}
	rawRepo := newIngestRawRepo()
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, gameRepo,
		&ingestSBRepo{books: []model.Sportsbook{{ID: "sb-uuid-1", Key: "draftkings"}}},
		rawRepo, 0, 0,
	)

	result, err := props.IngestProps(context.Background(), "soccer_epl")
	if err != nil {
		t.Fatalf("IngestProps failed: %v", err)
	}

	if result.EventsListed != 3 || result.EventsInScope != 1 || result.EventsFetched != 1 || result.EventsCapped != 0 {
		t.Errorf("result = %+v, want 3 listed / 1 in scope / 1 fetched", result)
	}
	if result.LinesIngested != 2 || result.LinesSkipped != 0 {
		t.Errorf("lines = %d/%d, want 2 ingested", result.LinesIngested, result.LinesSkipped)
	}

	// Game metadata was recorded up front for the in-window event only.
	games := gameRepo.upsertedGames()
	if len(games) != 1 || games[0].GameExternalID != "evA" || games[0].League != model.LeagueEPL {
		t.Fatalf("upserted games = %+v, want evA/EPL", games)
	}

	inserted := lineRepo.insertedSnapshots()
	if len(inserted) != 2 {
		t.Fatalf("inserted = %+v, want 2 prop snapshots", inserted)
	}
	over := inserted[0]
	if over.Selection != "Bukayo Saka Over 2.5" || over.MarketType != model.MarketPlayerProp {
		t.Errorf("snapshot = %+v, want deriveSide-compatible Over selection", over)
	}
	if over.PlayerExternalID != "bukayo-saka" || over.StatType != "player_shots" || over.PropType != oddsapi.PropTypeOverUnder {
		t.Errorf("prop columns = %q/%q/%q, want ADR-029 fields", over.PlayerExternalID, over.StatType, over.PropType)
	}

	// The per-event raw response was archived asynchronously.
	raw := rawRepo.waitForInsert(t)
	if raw.Endpoint != "/v4/sports/soccer_epl/events/evA/odds" || raw.HTTPStatus != http.StatusOK {
		t.Errorf("raw archive = %+v, want per-event endpoint at 200", raw)
	}
}

func TestIngestPropsCapTruncatesCycle(t *testing.T) {
	now := time.Now().UTC()
	events := fmt.Sprintf(`[%s,%s]`,
		eventStubJSON("evA", now.Add(2*time.Hour)),
		eventStubJSON("evB", now.Add(3*time.Hour)),
	)
	srv := propStubServer(t, events, map[string]struct {
		status int
		body   string
	}{
		"evA": {http.StatusOK, propOddsJSON("evA")},
		"evB": {http.StatusOK, propOddsJSON("evB")},
	})

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, &ingestGameRepo{},
		&ingestSBRepo{books: []model.Sportsbook{{ID: "sb-uuid-1", Key: "draftkings"}}},
		newIngestRawRepo(), 0, 1, // cap at one event per cycle
	)

	result, err := props.IngestProps(context.Background(), "soccer_epl")
	if err != nil {
		t.Fatalf("IngestProps failed: %v", err)
	}
	if result.EventsInScope != 2 || result.EventsCapped != 1 || result.EventsFetched != 1 {
		t.Errorf("result = %+v, want cap to drop one of two in-window events", result)
	}
}

func TestIngestPropsUnknownSportIsNoop(t *testing.T) {
	ingestion, lineRepo, _, _ := newIngestionFixture(t, "http://127.0.0.1:1", deadRedis())
	props := newPropFixture(t, "http://127.0.0.1:1", ingestion)

	result, err := props.IngestProps(context.Background(), "curling_worlds")
	if err != nil {
		t.Fatalf("unknown sport must be a no-op, got error: %v", err)
	}
	if result.Sport != "curling_worlds" || result.EventsListed != 0 || result.LinesIngested != 0 {
		t.Errorf("result = %+v, want empty no-op result", result)
	}
	if len(lineRepo.insertedSnapshots()) != 0 {
		t.Error("no client calls or inserts should happen for an unconfigured sport")
	}
}

func TestIngestPropsEventsFetchError(t *testing.T) {
	ingestion, _, _, _ := newIngestionFixture(t, "http://127.0.0.1:1", deadRedis())
	props := newPropFixture(t, "http://127.0.0.1:1", ingestion)

	if _, err := props.IngestProps(context.Background(), "soccer_epl"); err == nil || !strings.Contains(err.Error(), "fetch events") {
		t.Fatalf("err = %v, want fetch events error", err)
	}
}

func TestIngestPropsNoEventsInWindow(t *testing.T) {
	now := time.Now().UTC()
	srv := propStubServer(t, fmt.Sprintf(`[%s]`, eventStubJSON("evOld", now.Add(-2*time.Hour))), nil)

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	gameRepo := &ingestGameRepo{}
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, gameRepo,
		&ingestSBRepo{}, newIngestRawRepo(), 0, 0,
	)

	result, err := props.IngestProps(context.Background(), "soccer_epl")
	if err != nil {
		t.Fatalf("IngestProps failed: %v", err)
	}
	if result.EventsListed != 1 || result.EventsInScope != 0 {
		t.Errorf("result = %+v, want the started event filtered out", result)
	}
	if len(gameRepo.upsertedGames()) != 0 {
		t.Error("no games should be recorded when nothing is in scope")
	}
}

func TestIngestPropsSportsbookError(t *testing.T) {
	now := time.Now().UTC()
	srv := propStubServer(t, fmt.Sprintf(`[%s]`, eventStubJSON("evA", now.Add(2*time.Hour))), nil)

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, &ingestGameRepo{},
		&ingestSBRepo{err: errors.New("db down")}, newIngestRawRepo(), 0, 0,
	)

	if _, err := props.IngestProps(context.Background(), "soccer_epl"); err == nil || !strings.Contains(err.Error(), "fetch sportsbooks") {
		t.Fatalf("err = %v, want fetch sportsbooks error", err)
	}
}

func TestIngestPropsUpsertGamesError(t *testing.T) {
	now := time.Now().UTC()
	srv := propStubServer(t, fmt.Sprintf(`[%s]`, eventStubJSON("evA", now.Add(2*time.Hour))), nil)

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, &ingestGameRepo{upsertErr: errors.New("db down")},
		&ingestSBRepo{}, newIngestRawRepo(), 0, 0,
	)

	if _, err := props.IngestProps(context.Background(), "soccer_epl"); err == nil || !strings.Contains(err.Error(), "upsert games") {
		t.Fatalf("err = %v, want upsert games error", err)
	}
}

func TestIngestPropsBadEventDoesNotSinkCycle(t *testing.T) {
	now := time.Now().UTC()
	events := fmt.Sprintf(`[%s,%s]`,
		eventStubJSON("evBad", now.Add(time.Hour)),
		eventStubJSON("evGood", now.Add(2*time.Hour)),
	)
	srv := propStubServer(t, events, map[string]struct {
		status int
		body   string
	}{
		"evBad":  {http.StatusInternalServerError, `upstream broke`},
		"evGood": {http.StatusOK, propOddsJSON("evGood")},
	})

	ingestion, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	rawRepo := newIngestRawRepo()
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, &ingestGameRepo{},
		&ingestSBRepo{books: []model.Sportsbook{{ID: "sb-uuid-1", Key: "draftkings"}}},
		rawRepo, 0, 0,
	)

	result, err := props.IngestProps(context.Background(), "soccer_epl")
	if err != nil {
		t.Fatalf("one bad event must not sink the cycle: %v", err)
	}
	if result.EventsFetched != 1 || result.LinesIngested != 2 {
		t.Errorf("result = %+v, want the healthy event ingested", result)
	}
	for _, snap := range lineRepo.insertedSnapshots() {
		if snap.GameExternalID != "evGood" {
			t.Errorf("snapshot for %q persisted, want only evGood", snap.GameExternalID)
		}
	}

	// Both responses were archived — the 500 is still evidence.
	statuses := map[int]bool{}
	statuses[rawRepo.waitForInsert(t).HTTPStatus] = true
	statuses[rawRepo.waitForInsert(t).HTTPStatus] = true
	if !statuses[http.StatusOK] || !statuses[http.StatusInternalServerError] {
		t.Errorf("archived statuses = %v, want both 200 and 500", statuses)
	}
}

func TestIngestPropsNoPropOutcomes(t *testing.T) {
	now := time.Now().UTC()
	// The per-event response only carries an h2h market, which the prop
	// normalizer ignores; the cycle ends with nothing to persist.
	h2hOnly := `{"id":"evA","sport_key":"soccer_epl","bookmakers":[{"key":"draftkings",
		"markets":[{"key":"h2h","outcomes":[{"name":"Arsenal","price":1.5}]}]}]}`
	srv := propStubServer(t, fmt.Sprintf(`[%s]`, eventStubJSON("evA", now.Add(time.Hour))), map[string]struct {
		status int
		body   string
	}{
		"evA": {http.StatusOK, h2hOnly},
	})

	ingestion, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, &ingestGameRepo{},
		&ingestSBRepo{books: []model.Sportsbook{{ID: "sb-uuid-1", Key: "draftkings"}}},
		newIngestRawRepo(), 0, 0,
	)

	result, err := props.IngestProps(context.Background(), "soccer_epl")
	if err != nil {
		t.Fatalf("IngestProps failed: %v", err)
	}
	if result.EventsFetched != 1 || result.LinesIngested != 0 {
		t.Errorf("result = %+v, want fetched but nothing ingested", result)
	}
	if len(lineRepo.insertedSnapshots()) != 0 {
		t.Error("non-prop markets must not be persisted by the prop path")
	}
}

func TestIngestPropsStopsOnContextCancellation(t *testing.T) {
	now := time.Now().UTC()
	srv := propStubServer(t, fmt.Sprintf(`[%s]`, eventStubJSON("evA", now.Add(time.Hour))), nil)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel from inside the sportsbook lookup: the per-event loop's context
	// guard trips before any odds request is spent.
	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	props := NewPropIngestionService(
		oddsapi.NewClient("k", srv.URL), ingestion, &ingestGameRepo{},
		&cancelingSBRepo{cancel: cancel}, newIngestRawRepo(), 0, 0,
	)

	if _, err := props.IngestProps(ctx, "soccer_epl"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

type cancelingSBRepo struct {
	ingestSBRepo
	cancel context.CancelFunc
}

func (f *cancelingSBRepo) GetAll(ctx context.Context, isSharp, isActive *bool) ([]model.Sportsbook, error) {
	f.cancel()
	return f.ingestSBRepo.GetAll(ctx, isSharp, isActive)
}
