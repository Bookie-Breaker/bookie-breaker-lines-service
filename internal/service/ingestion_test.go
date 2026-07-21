package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

// --- shared Redis container -------------------------------------------------
//
// Publish/invalidate assertions need a real Redis (LineCache and Publisher are
// concrete Redis-backed types). The container starts lazily on first use and
// is shared across the package; tests that need it skip when Docker is
// unavailable, mirroring tests/integration.

var (
	redisOnce      sync.Once
	redisContainer *tcredis.RedisContainer
	redisClient    *goredis.Client
	redisStartErr  error
)

func testRedis(t *testing.T) *goredis.Client {
	t.Helper()
	redisOnce.Do(func() {
		ctx := context.Background()
		provider, err := testcontainers.NewDockerProvider()
		if err != nil {
			redisStartErr = err
			return
		}
		healthErr := provider.Health(ctx)
		_ = provider.Close()
		if healthErr != nil {
			redisStartErr = healthErr
			return
		}

		redisContainer, err = tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			redisStartErr = err
			return
		}
		url, err := redisContainer.ConnectionString(ctx)
		if err != nil {
			redisStartErr = err
			return
		}
		opts, err := goredis.ParseURL(url)
		if err != nil {
			redisStartErr = err
			return
		}
		redisClient = goredis.NewClient(opts)
	})
	if redisStartErr != nil {
		t.Skipf("skipping: Docker/Redis unavailable: %v", redisStartErr)
	}
	return redisClient
}

// deadRedis returns a client whose address is unreachable, for tests that
// only exercise the warn-and-continue paths of cache and publish failures.
func deadRedis() *goredis.Client {
	return goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond, MaxRetries: -1})
}

func newDeadLineCache() *cache.LineCache { return cache.NewLineCache(deadRedis()) }

func newDeadPublisher() *pubsub.Publisher { return pubsub.NewPublisher(deadRedis()) }

func TestMain(m *testing.M) {
	code := m.Run()
	if redisClient != nil {
		_ = redisClient.Close()
	}
	if redisContainer != nil {
		_ = testcontainers.TerminateContainer(redisContainer)
	}
	os.Exit(code)
}

// --- ingestion fakes --------------------------------------------------------

type ingestLineRepo struct {
	repository.LineRepository

	mu        sync.Mutex
	latest    map[repository.LineKey]repository.LineValues
	latestErr error
	insertErr error
	inserted  [][]model.LineSnapshot
}

func (f *ingestLineRepo) GetLatestLineValues(_ context.Context, _ []string) (map[repository.LineKey]repository.LineValues, error) {
	if f.latestErr != nil {
		return nil, f.latestErr
	}
	if f.latest == nil {
		return map[repository.LineKey]repository.LineValues{}, nil
	}
	return f.latest, nil
}

func (f *ingestLineRepo) InsertLineSnapshots(_ context.Context, snapshots []model.LineSnapshot) (int, error) {
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserted = append(f.inserted, snapshots)
	return len(snapshots), nil
}

func (f *ingestLineRepo) insertedSnapshots() []model.LineSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []model.LineSnapshot
	for _, batch := range f.inserted {
		all = append(all, batch...)
	}
	return all
}

type ingestGameRepo struct {
	repository.GameRepository

	mu        sync.Mutex
	upserted  [][]model.Game
	upsertErr error
}

func (f *ingestGameRepo) UpsertGames(_ context.Context, games []model.Game) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, games)
	return nil
}

func (f *ingestGameRepo) upsertedGames() []model.Game {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []model.Game
	for _, batch := range f.upserted {
		all = append(all, batch...)
	}
	return all
}

type ingestSBRepo struct {
	repository.SportsbookRepository
	books []model.Sportsbook
	err   error
}

func (f *ingestSBRepo) GetAll(_ context.Context, _, _ *bool) ([]model.Sportsbook, error) {
	return f.books, f.err
}

// ingestRawRepo records archived raw responses and signals arrival, since the
// ingestion path archives asynchronously.
type ingestRawRepo struct {
	mu       sync.Mutex
	inserted []model.RawAPIResponse
	read     int
	notify   chan struct{}
}

func newIngestRawRepo() *ingestRawRepo {
	return &ingestRawRepo{notify: make(chan struct{}, 64)}
}

func (f *ingestRawRepo) Insert(_ context.Context, resp model.RawAPIResponse) error {
	f.mu.Lock()
	f.inserted = append(f.inserted, resp)
	f.mu.Unlock()
	f.notify <- struct{}{}
	return nil
}

func (f *ingestRawRepo) waitForInsert(t *testing.T) model.RawAPIResponse {
	t.Helper()
	select {
	case <-f.notify:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for raw response archive")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := f.inserted[f.read]
	f.read++
	return resp
}

// oddsServer serves a canned bulk-odds payload and records request paths.
func oddsServer(t *testing.T, status int, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const nbaOddsPayload = `[{
	"id": "ev1",
	"sport_key": "basketball_nba",
	"commence_time": "2026-07-21T00:00:00Z",
	"home_team": "Los Angeles Lakers",
	"away_team": "Boston Celtics",
	"bookmakers": [{
		"key": "draftkings",
		"markets": [{
			"key": "h2h",
			"outcomes": [
				{"name": "Los Angeles Lakers", "price": 1.91},
				{"name": "Boston Celtics", "price": 2.05}
			]
		}]
	}]
}]`

func newIngestionFixture(t *testing.T, srvURL string, rdb *goredis.Client) (*IngestionService, *ingestLineRepo, *ingestGameRepo, *ingestRawRepo) {
	t.Helper()
	lineRepo := &ingestLineRepo{}
	gameRepo := &ingestGameRepo{}
	sbRepo := &ingestSBRepo{books: []model.Sportsbook{{ID: "sb-uuid-1", Key: "draftkings", Name: "DraftKings"}}}
	rawRepo := newIngestRawRepo()
	svc := NewIngestionService(
		oddsapi.NewClient("k", srvURL),
		lineRepo, gameRepo, sbRepo, rawRepo,
		cache.NewLineCache(rdb),
		pubsub.NewPublisher(rdb),
	)
	return svc, lineRepo, gameRepo, rawRepo
}

func TestIngestHappyPathPublishesAndInvalidates(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()

	srv := oddsServer(t, http.StatusOK, nbaOddsPayload)
	svc, lineRepo, gameRepo, rawRepo := newIngestionFixture(t, srv.URL, rdb)

	// The Lakers price is already persisted unchanged: it must be skipped.
	lakersKey := repository.LineKey{
		GameExternalID: "ev1",
		SportsbookID:   "sb-uuid-1",
		MarketType:     model.MarketMoneyline,
		Selection:      "Los Angeles Lakers",
	}
	lineRepo.latest = map[repository.LineKey]repository.LineValues{
		lakersKey: {LineValue: nil, OddsAmerican: -110, IsLive: false},
	}

	// Pre-seed the per-game cache so invalidation is observable.
	lineCache := cache.NewLineCache(rdb)
	if err := lineCache.SetCurrentLines(ctx, "ev1", []model.LineSnapshot{{ID: "stale"}}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// Subscribe before ingesting so the published event is not missed.
	sub := rdb.Subscribe(ctx, "events:lines.updated")
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	result, err := svc.Ingest(ctx, "basketball_nba")
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	if result.League != "basketball_nba" || result.GamesFound != 1 {
		t.Errorf("result = %+v, want 1 game for basketball_nba", result)
	}
	if result.LinesIngested != 1 || result.LinesSkipped != 1 {
		t.Errorf("ingested/skipped = %d/%d, want 1/1 (unchanged Lakers line deduped)", result.LinesIngested, result.LinesSkipped)
	}

	// Game metadata recorded for side derivation and the closing sweep.
	games := gameRepo.upsertedGames()
	if len(games) != 1 || games[0].GameExternalID != "ev1" || games[0].League != model.LeagueNBA {
		t.Fatalf("upserted games = %+v, want ev1/NBA", games)
	}
	if games[0].HomeTeam != "Los Angeles Lakers" || games[0].AwayTeam != "Boston Celtics" {
		t.Errorf("teams = %q/%q, want Lakers/Celtics", games[0].HomeTeam, games[0].AwayTeam)
	}

	// Only the changed Celtics snapshot was persisted.
	inserted := lineRepo.insertedSnapshots()
	if len(inserted) != 1 || inserted[0].Selection != "Boston Celtics" {
		t.Fatalf("inserted = %+v, want only the Celtics snapshot", inserted)
	}
	if inserted[0].Source != "the_odds_api" || inserted[0].IsLive {
		t.Errorf("snapshot = %+v, want pre-game the_odds_api snapshot", inserted[0])
	}

	// The cached lines for the game were invalidated.
	cached, err := lineCache.GetCurrentLines(ctx, "ev1")
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if cached != nil {
		t.Errorf("cache = %+v, want invalidated (nil)", cached)
	}

	// A lines.updated event was published with the change summary.
	select {
	case msg := <-sub.Channel():
		var event pubsub.LinesUpdatedEvent
		if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
			t.Fatalf("event payload is not JSON: %v", err)
		}
		if event.Event != "lines.updated" {
			t.Errorf("event = %q, want lines.updated", event.Event)
		}
		if event.League != "NBA" || event.ChangeCount != 1 || event.Source != "the_odds_api" || event.IsLive {
			t.Errorf("event = %+v, want NBA pre-game change of 1", event)
		}
		if len(event.GameIDs) != 1 || event.GameIDs[0] != "ev1" {
			t.Errorf("game_ids = %v, want [ev1]", event.GameIDs)
		}
		if len(event.MarketTypes) != 1 || event.MarketTypes[0] != "MONEYLINE" {
			t.Errorf("market_types = %v, want [MONEYLINE]", event.MarketTypes)
		}
		if len(event.SportsbooksUpdated) != 1 || event.SportsbooksUpdated[0] != "draftkings" {
			t.Errorf("sportsbooks_updated = %v, want [draftkings]", event.SportsbooksUpdated)
		}
		if _, err := time.Parse(time.RFC3339, event.Timestamp); err != nil {
			t.Errorf("timestamp %q is not RFC3339: %v", event.Timestamp, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lines.updated event")
	}

	// The raw response was archived asynchronously.
	raw := rawRepo.waitForInsert(t)
	if raw.Endpoint != "/v4/sports/basketball_nba/odds" || raw.HTTPStatus != http.StatusOK {
		t.Errorf("raw archive = %+v, want odds endpoint at 200", raw)
	}
	if !strings.Contains(raw.ResponseBody, "Boston Celtics") {
		t.Error("raw archive should carry the response body")
	}
}

func TestIngestNoLinesToIngest(t *testing.T) {
	srv := oddsServer(t, http.StatusOK, `[]`)
	svc, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())

	result, err := svc.Ingest(context.Background(), "basketball_nba")
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	if result.GamesFound != 0 || result.LinesIngested != 0 || result.LinesSkipped != 0 {
		t.Errorf("result = %+v, want empty cycle", result)
	}
	if len(lineRepo.insertedSnapshots()) != 0 {
		t.Error("nothing should be inserted for an empty response")
	}
}

func TestIngestAllLinesUnchanged(t *testing.T) {
	srv := oddsServer(t, http.StatusOK, nbaOddsPayload)
	svc, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())

	lineRepo.latest = map[repository.LineKey]repository.LineValues{
		{GameExternalID: "ev1", SportsbookID: "sb-uuid-1", MarketType: model.MarketMoneyline, Selection: "Los Angeles Lakers"}: {OddsAmerican: -110},
		{GameExternalID: "ev1", SportsbookID: "sb-uuid-1", MarketType: model.MarketMoneyline, Selection: "Boston Celtics"}:     {OddsAmerican: 105},
	}

	result, err := svc.Ingest(context.Background(), "basketball_nba")
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	if result.LinesIngested != 0 || result.LinesSkipped != 2 {
		t.Errorf("ingested/skipped = %d/%d, want 0/2", result.LinesIngested, result.LinesSkipped)
	}
	if len(lineRepo.insertedSnapshots()) != 0 {
		t.Error("unchanged lines must not be re-inserted")
	}
}

func TestIngestToleratesCacheAndPublishFailures(t *testing.T) {
	// LineCache and Publisher point at an unreachable Redis: invalidation and
	// publish failures are warnings, not ingestion errors.
	srv := oddsServer(t, http.StatusOK, nbaOddsPayload)
	svc, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())

	result, err := svc.Ingest(context.Background(), "basketball_nba")
	if err != nil {
		t.Fatalf("Ingest must succeed despite Redis being down: %v", err)
	}
	if result.LinesIngested != 2 {
		t.Errorf("ingested = %d, want 2", result.LinesIngested)
	}
	if len(lineRepo.insertedSnapshots()) != 2 {
		t.Error("both snapshots should be persisted")
	}
}

func TestIngestErrorPaths(t *testing.T) {
	t.Run("fetch failure", func(t *testing.T) {
		svc, _, _, _ := newIngestionFixture(t, "http://127.0.0.1:1", deadRedis())
		if _, err := svc.Ingest(context.Background(), "basketball_nba"); err == nil || !strings.Contains(err.Error(), "fetch odds") {
			t.Fatalf("err = %v, want fetch odds error", err)
		}
	})

	t.Run("sportsbook lookup failure", func(t *testing.T) {
		srv := oddsServer(t, http.StatusOK, nbaOddsPayload)
		lineRepo := &ingestLineRepo{}
		svc := NewIngestionService(
			oddsapi.NewClient("k", srv.URL),
			lineRepo, &ingestGameRepo{}, &ingestSBRepo{err: errors.New("db down")}, newIngestRawRepo(),
			cache.NewLineCache(deadRedis()), pubsub.NewPublisher(deadRedis()),
		)
		if _, err := svc.Ingest(context.Background(), "basketball_nba"); err == nil || !strings.Contains(err.Error(), "fetch sportsbooks") {
			t.Fatalf("err = %v, want fetch sportsbooks error", err)
		}
	})

	t.Run("game upsert failure", func(t *testing.T) {
		srv := oddsServer(t, http.StatusOK, nbaOddsPayload)
		svc, _, gameRepo, _ := newIngestionFixture(t, srv.URL, deadRedis())
		gameRepo.upsertErr = errors.New("db down")
		if _, err := svc.Ingest(context.Background(), "basketball_nba"); err == nil || !strings.Contains(err.Error(), "upsert games") {
			t.Fatalf("err = %v, want upsert games error", err)
		}
	})

	t.Run("latest values failure", func(t *testing.T) {
		srv := oddsServer(t, http.StatusOK, nbaOddsPayload)
		svc, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
		lineRepo.latestErr = errors.New("db down")
		if _, err := svc.Ingest(context.Background(), "basketball_nba"); err == nil || !strings.Contains(err.Error(), "fetch latest line values") {
			t.Fatalf("err = %v, want latest line values error", err)
		}
	})

	t.Run("insert failure", func(t *testing.T) {
		srv := oddsServer(t, http.StatusOK, nbaOddsPayload)
		svc, lineRepo, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
		lineRepo.insertErr = errors.New("db down")
		if _, err := svc.Ingest(context.Background(), "basketball_nba"); err == nil || !strings.Contains(err.Error(), "insert line snapshots") {
			t.Fatalf("err = %v, want insert error", err)
		}
	})
}

func TestPersistSnapshotsEmptyInput(t *testing.T) {
	svc, _, _, _ := newIngestionFixture(t, "http://127.0.0.1:1", deadRedis())
	inserted, skipped, err := svc.persistSnapshots(context.Background(), nil, nil)
	if inserted != 0 || skipped != 0 || err != nil {
		t.Errorf("persistSnapshots(nil) = (%d, %d, %v), want (0, 0, nil)", inserted, skipped, err)
	}
}

func TestUniqueHelpers(t *testing.T) {
	snaps := []model.LineSnapshot{
		{GameExternalID: "g1", MarketType: model.MarketSpread, SportsbookID: "sb-1"},
		{GameExternalID: "g1", MarketType: model.MarketTotal, SportsbookID: "sb-2"},
		{GameExternalID: "g2", MarketType: model.MarketSpread, SportsbookID: "sb-1"},
	}

	ids := uniqueGameIDs(snaps)
	if len(ids) != 2 || ids[0] != "g1" || ids[1] != "g2" {
		t.Errorf("uniqueGameIDs = %v, want [g1 g2]", ids)
	}

	markets := uniqueMarketTypes(snaps)
	if len(markets) != 2 || markets[0] != "SPREAD" || markets[1] != "TOTAL" {
		t.Errorf("uniqueMarketTypes = %v, want [SPREAD TOTAL]", markets)
	}

	sbMap := map[string]string{"draftkings": "sb-1", "fanduel": "sb-2"}
	books := uniqueSportsbooks(snaps, sbMap)
	if len(books) != 2 || books[0] != "draftkings" || books[1] != "fanduel" {
		t.Errorf("uniqueSportsbooks = %v, want [draftkings fanduel]", books)
	}

	// Sportsbook IDs missing from the map are dropped, not emitted as "".
	unknown := uniqueSportsbooks([]model.LineSnapshot{{SportsbookID: "mystery"}}, sbMap)
	if len(unknown) != 0 {
		t.Errorf("uniqueSportsbooks(unknown) = %v, want empty", unknown)
	}
}
