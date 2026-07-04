package server

import (
	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/handler"
)

func registerRoutes(e *echo.Echo, deps Deps) {
	healthHandler := handler.NewHealthHandler(deps.DB, deps.Redis)
	linesHandler := handler.NewLinesHandler(deps.Query)
	sportsbooksHandler := handler.NewSportsbooksHandler(deps.SportsbookRepo)
	ingestionHandler := handler.NewIngestionHandler(deps.Ingestion, deps.SportKeys, deps.IngestionEnabled)

	api := e.Group("/api/v1/lines")
	api.GET("/health", healthHandler.GetHealth)
	api.GET("/current", linesHandler.GetCurrentLines)
	api.GET("/snapshots/:line_id", linesHandler.GetLineSnapshot)
	api.GET("/game/:game_id", linesHandler.GetGameLines)
	api.GET("/game/:game_id/movement", linesHandler.GetGameLineMovement)
	api.GET("/game/:game_id/best", linesHandler.GetGameBestLines)
	api.GET("/game/:game_id/closing", linesHandler.GetGameClosingLines)
	api.GET("/sportsbooks", sportsbooksHandler.GetSportsbooks)
	api.POST("/ingestion/trigger", ingestionHandler.TriggerIngestion)
}
