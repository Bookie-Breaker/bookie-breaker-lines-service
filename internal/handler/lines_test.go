package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

// Fakes implement just the repository methods the query service touches;
// the embedded interface panics on anything unexpected.

type fakeLineRepo struct {
	repository.LineRepository
	current  []model.LineSnapshot
	hasMore  bool
	snapshot *model.LineSnapshot
	movement []model.LineSnapshot
	closing  []model.ClosingLine
	err      error
}

func (f *fakeLineRepo) GetCurrentLines(_ context.Context, _ repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	return f.current, f.hasMore, f.err
}

func (f *fakeLineRepo) GetGameLines(_ context.Context, _ string, _ repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	return f.current, f.hasMore, f.err
}

func (f *fakeLineRepo) GetLineByID(_ context.Context, _ string) (*model.LineSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.snapshot, nil
}

func (f *fakeLineRepo) GetLineMovement(_ context.Context, _ string, _ repository.MovementFilters) ([]model.LineSnapshot, error) {
	return f.movement, f.err
}

func (f *fakeLineRepo) GetClosingLines(_ context.Context, _ string, _ repository.ClosingLineFilters) ([]model.ClosingLine, error) {
	return f.closing, f.err
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
	err   error

	gotIsSharp  *bool
	gotIsActive *bool
}

func (f *fakeSBRepo) GetAll(_ context.Context, isSharp, isActive *bool) ([]model.Sportsbook, error) {
	f.gotIsSharp = isSharp
	f.gotIsActive = isActive
	return f.books, f.err
}

// deadLineCache is a real LineCache pointed at an unreachable Redis; cache
// errors are non-fatal for the query service, so reads fall through to the
// repository.
func deadLineCache() *cache.LineCache {
	return cache.NewLineCache(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond, MaxRetries: -1}))
}

func testGame() model.Game {
	return model.Game{
		GameExternalID: "g1",
		League:         model.LeagueNBA,
		HomeTeam:       "Los Angeles Lakers",
		AwayTeam:       "Boston Celtics",
		CommenceTime:   time.Date(2026, 7, 3, 19, 0, 0, 0, time.UTC),
	}
}

func testSnapshot(id string) model.LineSnapshot {
	line := -3.5
	return model.LineSnapshot{
		ID:             id,
		GameExternalID: "g1",
		SportsbookID:   "sb-1",
		SportsbookKey:  "draftkings",
		MarketType:     model.MarketSpread,
		Selection:      "Los Angeles Lakers -3.5",
		LineValue:      &line,
		OddsAmerican:   -110,
		OddsDecimal:    1.91,
		CapturedAt:     time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

func newQueryService(lineRepo *fakeLineRepo, gameRepo *fakeGameRepo) *service.LineQueryService {
	return service.NewLineQueryService(lineRepo, gameRepo, &fakeSBRepo{}, deadLineCache())
}

// doRequest runs one request through a fresh echo instance and returns the
// recorder plus the decoded body.
func doRequest(t *testing.T, method, target string, h echo.HandlerFunc, setup func(echo.Context)) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if setup != nil {
		setup(c)
	}
	if err := h(c); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return rec, body
}

func assertErrorCode(t *testing.T, body map[string]any, wantCode string) {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("body has no error object: %v", body)
	}
	if errObj["code"] != wantCode {
		t.Errorf("error code = %v, want %s", errObj["code"], wantCode)
	}
	if errObj["message"] == "" {
		t.Error("error message should not be empty")
	}
}

func assertMeta(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	meta, ok := body["meta"].(map[string]any)
	if !ok {
		t.Fatalf("body has no meta object: %v", body)
	}
	if meta["timestamp"] == "" || meta["request_id"] == "" {
		t.Errorf("meta missing timestamp/request_id: %v", meta)
	}
	return meta
}

func TestGetCurrentLines(t *testing.T) {
	lineRepo := &fakeLineRepo{current: []model.LineSnapshot{testSnapshot("l1")}, hasMore: true}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}
	h := NewLinesHandler(newQueryService(lineRepo, gameRepo))

	rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/current?limit=1&league=NBA", h.GetCurrentLines, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data, ok := body["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("data = %v, want one line", body["data"])
	}
	line := data[0].(map[string]any)
	if line["side"] != "HOME" {
		t.Errorf("side = %v, want HOME (enriched against game metadata)", line["side"])
	}
	if line["implied_probability"] == nil {
		t.Error("implied_probability should be enriched")
	}
	meta := assertMeta(t, body)
	pag, ok := meta["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("meta has no pagination: %v", meta)
	}
	if pag["limit"] != float64(1) || pag["has_more"] != true || pag["next_cursor"] == "" {
		t.Errorf("pagination = %v, want limit 1, has_more, cursor", pag)
	}
}

func TestGetCurrentLinesEmptySerializesAsArray(t *testing.T) {
	h := NewLinesHandler(newQueryService(&fakeLineRepo{}, &fakeGameRepo{}))

	rec, _ := doRequest(t, http.MethodGet, "/api/v1/lines/current", h.GetCurrentLines, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var typed struct {
		Data []model.LineSnapshot `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &typed); err != nil {
		t.Fatal(err)
	}
	if typed.Data == nil {
		t.Error("empty result must serialize as [], not null")
	}
}

func TestGetCurrentLinesInvalidIsLive(t *testing.T) {
	h := NewLinesHandler(newQueryService(&fakeLineRepo{}, &fakeGameRepo{}))

	rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/current?is_live=maybe", h.GetCurrentLines, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertErrorCode(t, body, "INVALID_PARAMETER")
}

func TestGetCurrentLinesInvalidCursor(t *testing.T) {
	h := NewLinesHandler(newQueryService(&fakeLineRepo{err: repository.ErrInvalidCursor}, &fakeGameRepo{}))

	rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/current?cursor=bogus", h.GetCurrentLines, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertErrorCode(t, body, "INVALID_CURSOR")
}

func TestGetCurrentLinesInternalError(t *testing.T) {
	h := NewLinesHandler(newQueryService(&fakeLineRepo{err: errors.New("db down")}, &fakeGameRepo{}))

	rec, body := doRequest(t, http.MethodGet, "/api/v1/lines/current", h.GetCurrentLines, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertErrorCode(t, body, "INTERNAL_ERROR")
}

func withGameID(id string) func(echo.Context) {
	return func(c echo.Context) {
		c.SetParamNames("game_id")
		c.SetParamValues(id)
	}
}

func TestGetGameLines(t *testing.T) {
	lineRepo := &fakeLineRepo{current: []model.LineSnapshot{testSnapshot("l1")}}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}
	h := NewLinesHandler(newQueryService(lineRepo, gameRepo))

	rec, body := doRequest(t, http.MethodGet, "/?side=home", h.GetGameLines, withGameID("g1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %v, want the HOME line to pass the side filter", data)
	}

	// The away side filter should drop the home snapshot.
	_, body = doRequest(t, http.MethodGet, "/?side=away", h.GetGameLines, withGameID("g1"))
	if len(body["data"].([]any)) != 0 {
		t.Errorf("data = %v, want empty for side=away", body["data"])
	}
}

func TestGetGameLinesUnknownGame(t *testing.T) {
	h := NewLinesHandler(newQueryService(&fakeLineRepo{}, &fakeGameRepo{}))

	rec, body := doRequest(t, http.MethodGet, "/", h.GetGameLines, withGameID("missing"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertErrorCode(t, body, "NOT_FOUND")
}

func TestGetGameLinesInvalidIsLive(t *testing.T) {
	h := NewLinesHandler(newQueryService(&fakeLineRepo{}, &fakeGameRepo{}))

	rec, body := doRequest(t, http.MethodGet, "/?is_live=nope", h.GetGameLines, withGameID("g1"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	assertErrorCode(t, body, "INVALID_PARAMETER")
}

func TestGetLineSnapshot(t *testing.T) {
	snap := testSnapshot("l1")
	lineRepo := &fakeLineRepo{snapshot: &snap, movement: []model.LineSnapshot{snap}}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}
	h := NewLinesHandler(newQueryService(lineRepo, gameRepo))

	rec, body := doRequest(t, http.MethodGet, "/", h.GetLineSnapshot, func(c echo.Context) {
		c.SetParamNames("line_id")
		c.SetParamValues("l1")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := body["data"].(map[string]any)
	if data["id"] != "l1" || data["side"] != "HOME" {
		t.Errorf("data = %v, want enriched snapshot l1", data)
	}
	// The only snapshot in history is this one, so it is the opening line.
	if data["is_opening"] != true {
		t.Errorf("is_opening = %v, want true", data["is_opening"])
	}
	assertMeta(t, body)
}

func TestGetLineSnapshotNotFound(t *testing.T) {
	lineRepo := &fakeLineRepo{err: fmt.Errorf("get line by id %q: %w", "missing", pgx.ErrNoRows)}
	h := NewLinesHandler(newQueryService(lineRepo, &fakeGameRepo{}))

	rec, body := doRequest(t, http.MethodGet, "/", h.GetLineSnapshot, func(c echo.Context) {
		c.SetParamNames("line_id")
		c.SetParamValues("missing")
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertErrorCode(t, body, "NOT_FOUND")
}

func TestGetGameLineMovement(t *testing.T) {
	snap := testSnapshot("l1")
	later := testSnapshot("l2")
	later.CapturedAt = snap.CapturedAt.Add(time.Hour)
	later.OddsAmerican = -115

	lineRepo := &fakeLineRepo{movement: []model.LineSnapshot{snap, later}}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}
	h := NewLinesHandler(newQueryService(lineRepo, gameRepo))

	rec, body := doRequest(t, http.MethodGet, "/", h.GetGameLineMovement, withGameID("g1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %v, want one movement group", data)
	}
	group := data[0].(map[string]any)
	if group["current_odds"] != float64(-115) {
		t.Errorf("current_odds = %v, want -115", group["current_odds"])
	}
	if len(group["line_snapshots"].([]any)) != 2 {
		t.Errorf("snapshots = %v, want 2", group["line_snapshots"])
	}
}

func TestGetGameLineMovementUnknownGame(t *testing.T) {
	h := NewLinesHandler(newQueryService(&fakeLineRepo{}, &fakeGameRepo{}))

	rec, body := doRequest(t, http.MethodGet, "/", h.GetGameLineMovement, withGameID("missing"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	assertErrorCode(t, body, "NOT_FOUND")
}

func TestGetGameBestLines(t *testing.T) {
	lineRepo := &fakeLineRepo{current: []model.LineSnapshot{testSnapshot("l1")}}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}
	h := NewLinesHandler(newQueryService(lineRepo, gameRepo))

	rec, body := doRequest(t, http.MethodGet, "/?market_type=SPREAD", h.GetGameBestLines, withGameID("g1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data = %v, want one best line", data)
	}
	bestLine := data[0].(map[string]any)
	if bestLine["best_odds_american"] != float64(-110) || bestLine["line_id"] != "l1" {
		t.Errorf("best line = %v, want odds -110 from l1", bestLine)
	}
}

func TestGetGameClosingLines(t *testing.T) {
	closingVal := -2.5
	lineRepo := &fakeLineRepo{closing: []model.ClosingLine{{
		GameExternalID: "g1",
		SportsbookID:   "sb-1",
		MarketType:     model.MarketSpread,
		Selection:      "Los Angeles Lakers -2.5",
		LineValue:      &closingVal,
		OddsAmerican:   -115,
	}}}
	gameRepo := &fakeGameRepo{games: map[string]model.Game{"g1": testGame()}}
	h := NewLinesHandler(newQueryService(lineRepo, gameRepo))

	rec, body := doRequest(t, http.MethodGet, "/", h.GetGameClosingLines, withGameID("g1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	data := body["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["odds_american"] != float64(-115) {
		t.Errorf("data = %v, want the closing line", data)
	}

	rec, body = doRequest(t, http.MethodGet, "/", h.GetGameClosingLines, withGameID("missing"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for unknown game", rec.Code)
	}
	assertErrorCode(t, body, "NOT_FOUND")
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b", []string{"a", "b"}},
		{" a , b ,, ", []string{"a", "b"}},
	}
	for _, tt := range tests {
		got := splitCSV(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		query string
		want  int
	}{
		{"", defaultLimit},
		{"limit=25", 25},
		{"limit=abc", defaultLimit},
		{"limit=0", defaultLimit},
		{"limit=-5", defaultLimit},
		{"limit=9999", maxLimit},
	}
	e := echo.New()
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
		c := e.NewContext(req, httptest.NewRecorder())
		if got := parseLimit(c); got != tt.want {
			t.Errorf("parseLimit(%q) = %d, want %d", tt.query, got, tt.want)
		}
	}
}
