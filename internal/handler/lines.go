package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// LinesHandler serves the line read endpoints.
type LinesHandler struct {
	query *service.LineQueryService
}

// NewLinesHandler creates a new lines handler.
func NewLinesHandler(query *service.LineQueryService) *LinesHandler {
	return &LinesHandler{query: query}
}

// GetCurrentLines handles GET /api/v1/lines/current.
func (h *LinesHandler) GetCurrentLines(c echo.Context) error {
	filters := repository.CurrentLineFilters{
		Leagues:     splitCSV(c.QueryParam("league")),
		GameIDs:     splitCSV(c.QueryParam("game_id")),
		Sportsbooks: splitCSV(c.QueryParam("sportsbook")),
		MarketTypes: splitCSV(c.QueryParam("market_type")),
		Date:        c.QueryParam("date"),
		Limit:       parseLimit(c),
		Cursor:      c.QueryParam("cursor"),
	}

	lines, hasMore, cursor, err := h.query.CurrentLines(c.Request().Context(), filters)
	if err != nil {
		return h.mapError(c, err)
	}
	return PaginatedResponse(c, emptyToSlice(lines), filters.Limit, hasMore, cursor)
}

// GetGameLines handles GET /api/v1/lines/game/{game_id}.
func (h *LinesHandler) GetGameLines(c echo.Context) error {
	filters := repository.CurrentLineFilters{
		Sportsbooks: splitCSV(c.QueryParam("sportsbook")),
		MarketTypes: splitCSV(c.QueryParam("market_type")),
		Limit:       parseLimit(c),
		Cursor:      c.QueryParam("cursor"),
	}

	lines, hasMore, cursor, err := h.query.GameLines(c.Request().Context(), c.Param("game_id"), filters, c.QueryParam("side"))
	if err != nil {
		return h.mapError(c, err)
	}
	return PaginatedResponse(c, emptyToSlice(lines), filters.Limit, hasMore, cursor)
}

// GetLineSnapshot handles GET /api/v1/lines/snapshots/{line_id}.
func (h *LinesHandler) GetLineSnapshot(c echo.Context) error {
	snap, err := h.query.Snapshot(c.Request().Context(), c.Param("line_id"))
	if err != nil {
		return h.mapError(c, err)
	}
	return SuccessResponse(c, snap)
}

// GetGameLineMovement handles GET /api/v1/lines/game/{game_id}/movement.
func (h *LinesHandler) GetGameLineMovement(c echo.Context) error {
	marketType := c.QueryParam("market_type")
	if marketType == "" {
		marketType = "SPREAD"
	}
	filters := repository.MovementFilters{
		Sportsbook: c.QueryParam("sportsbook"),
		MarketType: marketType,
		Selection:  c.QueryParam("selection"),
	}

	movements, err := h.query.Movement(c.Request().Context(), c.Param("game_id"), filters)
	if err != nil {
		return h.mapError(c, err)
	}
	return SuccessResponse(c, emptyToSlice(movements))
}

// GetGameBestLines handles GET /api/v1/lines/game/{game_id}/best.
func (h *LinesHandler) GetGameBestLines(c echo.Context) error {
	best, err := h.query.Best(c.Request().Context(), c.Param("game_id"), c.QueryParam("market_type"), c.QueryParam("selection"))
	if err != nil {
		return h.mapError(c, err)
	}
	return SuccessResponse(c, emptyToSlice(best))
}

// GetGameClosingLines handles GET /api/v1/lines/game/{game_id}/closing.
func (h *LinesHandler) GetGameClosingLines(c echo.Context) error {
	filters := repository.ClosingLineFilters{
		Sportsbook: c.QueryParam("sportsbook"),
		MarketType: c.QueryParam("market_type"),
	}

	closing, err := h.query.Closing(c.Request().Context(), c.Param("game_id"), filters)
	if err != nil {
		return h.mapError(c, err)
	}
	return SuccessResponse(c, emptyToSlice(closing))
}

func (h *LinesHandler) mapError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrGameNotFound):
		return ErrorResponse(c, http.StatusNotFound, "NOT_FOUND", "game not found")
	case errors.Is(err, service.ErrSnapshotNotFound):
		return ErrorResponse(c, http.StatusNotFound, "NOT_FOUND", "line snapshot not found")
	case errors.Is(err, repository.ErrInvalidCursor):
		return ErrorResponse(c, http.StatusBadRequest, "INVALID_CURSOR", "pagination cursor is malformed")
	default:
		c.Logger().Error(err)
		return ErrorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseLimit(c echo.Context) int {
	raw := c.QueryParam("limit")
	if raw == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// emptyToSlice keeps empty results serializing as [] instead of null.
func emptyToSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
