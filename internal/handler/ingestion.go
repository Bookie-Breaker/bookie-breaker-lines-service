package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

// IngestionHandler serves the manual ingestion trigger endpoint.
type IngestionHandler struct {
	ingestion        *service.IngestionService
	defaultSportKeys []string
	enabled          bool
}

// NewIngestionHandler creates a new ingestion handler. enabled reflects
// whether an Odds API key is configured.
func NewIngestionHandler(ingestion *service.IngestionService, defaultSportKeys []string, enabled bool) *IngestionHandler {
	return &IngestionHandler{
		ingestion:        ingestion,
		defaultSportKeys: defaultSportKeys,
		enabled:          enabled,
	}
}

type triggerRequest struct {
	League string `json:"league"`
}

// TriggerIngestion handles POST /api/v1/lines/ingestion/trigger.
func (h *IngestionHandler) TriggerIngestion(c echo.Context) error {
	if !h.enabled {
		return ErrorResponse(c, http.StatusServiceUnavailable, "INGESTION_DISABLED", "ODDS_API_KEY is not configured")
	}

	var req triggerRequest
	if err := c.Bind(&req); err != nil && c.Request().ContentLength > 0 {
		return ErrorResponse(c, http.StatusBadRequest, "INVALID_BODY", "request body must be JSON")
	}

	sportKeys := h.defaultSportKeys
	if req.League != "" {
		key := sportKeyForLeague(req.League)
		if key == "" {
			return ErrorResponse(c, http.StatusBadRequest, "INVALID_PARAMETER", "unknown league")
		}
		sportKeys = []string{key}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, sport := range sportKeys {
			if _, err := h.ingestion.Ingest(ctx, sport); err != nil {
				slog.Error("manual ingestion failed", "sport", sport, "error", err)
			}
		}
	}()

	return AcceptedResponse(c, map[string]string{
		"message": "ingestion queued",
		"league":  req.League,
	})
}

func sportKeyForLeague(league string) string {
	for key, l := range oddsapi.SportKeyToLeague {
		if string(l) == league {
			return key
		}
	}
	return ""
}
