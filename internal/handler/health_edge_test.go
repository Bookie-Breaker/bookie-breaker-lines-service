package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
)

// TestGetHealthUnreachableDependencies exercises the ping-failure branches:
// both pools are constructed lazily against unreachable addresses, so the
// handler's Ping calls fail rather than the constructors.
func TestGetHealthUnreachableDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := pgxpool.New(ctx, "postgres://user:pass@127.0.0.1:1/nope?connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New should be lazy: %v", err)
	}
	defer db.Close()

	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	defer func() { _ = rdb.Close() }()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lines/health", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	h := NewHealthHandler(db, rdb)
	if err := h.GetHealth(c); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when dependencies are down", rec.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Status != "degraded" {
		t.Errorf("status = %q, want degraded", resp.Status)
	}
	if resp.Dependencies["postgres"] == nil || resp.Dependencies["postgres"].Status != "unhealthy" {
		t.Errorf("postgres dependency = %+v, want unhealthy", resp.Dependencies["postgres"])
	}
	if resp.Dependencies["redis"] == nil || resp.Dependencies["redis"].Status != "unhealthy" {
		t.Errorf("redis dependency = %+v, want unhealthy", resp.Dependencies["redis"])
	}
}
