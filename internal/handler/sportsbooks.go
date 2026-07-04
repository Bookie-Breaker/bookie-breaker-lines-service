package handler

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

// SportsbooksHandler serves the sportsbook registry endpoint.
type SportsbooksHandler struct {
	sbRepo repository.SportsbookRepository
}

// NewSportsbooksHandler creates a new sportsbooks handler.
func NewSportsbooksHandler(sbRepo repository.SportsbookRepository) *SportsbooksHandler {
	return &SportsbooksHandler{sbRepo: sbRepo}
}

// GetSportsbooks handles GET /api/v1/lines/sportsbooks.
func (h *SportsbooksHandler) GetSportsbooks(c echo.Context) error {
	isSharp, err := parseOptionalBool(c.QueryParam("is_sharp"))
	if err != nil {
		return ErrorResponse(c, http.StatusBadRequest, "INVALID_PARAMETER", "is_sharp must be a boolean")
	}
	isActive, err := parseOptionalBool(c.QueryParam("is_active"))
	if err != nil {
		return ErrorResponse(c, http.StatusBadRequest, "INVALID_PARAMETER", "is_active must be a boolean")
	}

	books, err := h.sbRepo.GetAll(c.Request().Context(), isSharp, isActive)
	if err != nil {
		c.Logger().Error(err)
		return ErrorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
	return SuccessResponse(c, emptyToSlice(books))
}

func parseOptionalBool(v string) (*bool, error) {
	if v == "" {
		return nil, nil //nolint:nilnil // absent optional filter
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, err
	}
	return &b, nil
}
