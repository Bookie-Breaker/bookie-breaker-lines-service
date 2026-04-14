package server

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/handler"
)

func registerRoutes(e *echo.Echo, db *pgxpool.Pool, rdb *redis.Client) {
	healthHandler := handler.NewHealthHandler(db, rdb)

	api := e.Group("/api/v1/lines")
	api.GET("/health", healthHandler.GetHealth)
}
