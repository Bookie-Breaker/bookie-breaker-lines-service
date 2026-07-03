package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
)

type HealthHandler struct {
	db        *pgxpool.Pool
	rdb       *redis.Client
	startTime time.Time
}

func NewHealthHandler(db *pgxpool.Pool, rdb *redis.Client) *HealthHandler {
	return &HealthHandler{
		db:        db,
		rdb:       rdb,
		startTime: time.Now(),
	}
}

type DependencyStatus struct {
	Status    string  `json:"status"`
	LatencyMs float64 `json:"latency_ms"`
}

type HealthResponse struct {
	Status        string                       `json:"status"`
	Service       string                       `json:"service"`
	Version       string                       `json:"version"`
	UptimeSeconds float64                      `json:"uptime_seconds"`
	Dependencies  map[string]*DependencyStatus `json:"dependencies"`
}

func (h *HealthHandler) GetHealth(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()

	deps := make(map[string]*DependencyStatus)
	overallStatus := "healthy"

	// Check Postgres
	pgStart := time.Now()
	if h.db == nil {
		deps["postgres"] = &DependencyStatus{Status: "unhealthy", LatencyMs: 0}
		overallStatus = "degraded"
	} else if err := h.db.Ping(ctx); err != nil {
		deps["postgres"] = &DependencyStatus{Status: "unhealthy", LatencyMs: float64(time.Since(pgStart).Milliseconds())}
		overallStatus = "degraded"
	} else {
		deps["postgres"] = &DependencyStatus{Status: "healthy", LatencyMs: float64(time.Since(pgStart).Milliseconds())}
	}

	// Check Redis
	redisStart := time.Now()
	if h.rdb == nil {
		deps["redis"] = &DependencyStatus{Status: "unhealthy", LatencyMs: 0}
		overallStatus = "degraded"
	} else if err := h.rdb.Ping(ctx).Err(); err != nil {
		deps["redis"] = &DependencyStatus{Status: "unhealthy", LatencyMs: float64(time.Since(redisStart).Milliseconds())}
		overallStatus = "degraded"
	} else {
		deps["redis"] = &DependencyStatus{Status: "healthy", LatencyMs: float64(time.Since(redisStart).Milliseconds())}
	}

	resp := HealthResponse{
		Status:        overallStatus,
		Service:       "lines-service",
		Version:       "0.1.0",
		UptimeSeconds: time.Since(h.startTime).Seconds(),
		Dependencies:  deps,
	}

	status := http.StatusOK
	if overallStatus != "healthy" {
		status = http.StatusServiceUnavailable
	}

	return c.JSON(status, resp)
}
