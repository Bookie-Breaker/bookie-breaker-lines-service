package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

type stubLineRepo struct {
	repository.LineRepository
}

func (stubLineRepo) GetCurrentLines(_ context.Context, _ repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	return nil, false, nil
}

type stubGameRepo struct {
	repository.GameRepository
}

func (stubGameRepo) GetGames(_ context.Context, _ []string) (map[string]model.Game, error) {
	return map[string]model.Game{}, nil
}

type stubSBRepo struct {
	repository.SportsbookRepository
}

func (stubSBRepo) GetAll(_ context.Context, _, _ *bool) ([]model.Sportsbook, error) {
	return []model.Sportsbook{{ID: "sb-1", Key: "draftkings", Name: "DraftKings", IsActive: true}}, nil
}

func newTestServer() *echo.Echo {
	deadRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond, MaxRetries: -1})
	lineCache := cache.NewLineCache(deadRedis)
	query := service.NewLineQueryService(stubLineRepo{}, stubGameRepo{}, stubSBRepo{}, lineCache)
	ingestion := service.NewIngestionService(oddsapi.NewClient("k", "http://127.0.0.1:1"), stubLineRepo{}, stubGameRepo{}, stubSBRepo{}, nil, lineCache, nil)

	return New(Deps{
		DB:               nil, // health reports postgres unhealthy
		Redis:            nil, // health reports redis unhealthy
		Query:            query,
		Ingestion:        ingestion,
		SportsbookRepo:   stubSBRepo{},
		SportKeys:        []string{"basketball_nba"},
		IngestionEnabled: false,
	})
}

func TestServerRoutes(t *testing.T) {
	e := newTestServer()

	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{"health degraded without deps", http.MethodGet, "/api/v1/lines/health", http.StatusServiceUnavailable},
		{"current lines", http.MethodGet, "/api/v1/lines/current", http.StatusOK},
		{"sportsbooks", http.MethodGet, "/api/v1/lines/sportsbooks", http.StatusOK},
		{"ingestion trigger disabled", http.MethodPost, "/api/v1/lines/ingestion/trigger", http.StatusServiceUnavailable},
		{"unknown route", http.MethodGet, "/api/v1/nope", http.StatusNotFound},
		{"wrong method", http.MethodDelete, "/api/v1/lines/current", http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("%s %s = %d, want %d", tt.method, tt.target, rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestServerSetsRequestID(t *testing.T) {
	e := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lines/sportsbooks", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Header().Get(echo.HeaderXRequestID) == "" {
		t.Error("response must carry an X-Request-ID header")
	}

	// Client-supplied IDs are propagated for cross-service tracing.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/lines/sportsbooks", nil)
	req.Header.Set(echo.HeaderXRequestID, "trace-me")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Header().Get(echo.HeaderXRequestID) != "trace-me" {
		t.Errorf("request ID = %q, want trace-me", rec.Header().Get(echo.HeaderXRequestID))
	}
}

func TestServerEnvelopeShape(t *testing.T) {
	e := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lines/sportsbooks", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	data, ok := body["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("data = %v, want the stub sportsbook", body["data"])
	}
	meta, ok := body["meta"].(map[string]any)
	if !ok || meta["request_id"] == "" || meta["timestamp"] == "" {
		t.Errorf("meta = %v, want request_id and timestamp", body["meta"])
	}
}

func TestServerCORSHeaders(t *testing.T) {
	e := newTestServer()

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/lines/current", nil)
	req.Header.Set(echo.HeaderOrigin, "http://localhost:3000")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := rec.Header().Get(echo.HeaderAccessControlAllowOrigin); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}
