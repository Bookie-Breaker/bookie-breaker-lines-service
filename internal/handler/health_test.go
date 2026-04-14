package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/handler"
)

func TestGetHealth_ResponseShape(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lines/health", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Pass nil for db and redis — health check will report them as unhealthy
	h := handler.NewHealthHandler(nil, nil)

	// Should not panic even with nil dependencies
	if err := h.GetHealth(c); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}

	var resp handler.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Service != "lines-service" {
		t.Errorf("expected service 'lines-service', got %q", resp.Service)
	}

	if resp.Version == "" {
		t.Error("expected non-empty version")
	}

	if resp.UptimeSeconds < 0 {
		t.Error("expected non-negative uptime")
	}
}
