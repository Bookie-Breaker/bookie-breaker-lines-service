package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/service"
)

// newTriggerIngestion builds an IngestionService whose Odds API client points
// at an unreachable address: the queued goroutine fails fast on the fetch
// before touching any repository, so nil dependencies are never dereferenced.
func newTriggerIngestion() *service.IngestionService {
	client := oddsapi.NewClient("k", "http://127.0.0.1:1")
	return service.NewIngestionService(client, nil, nil, nil, nil, nil, nil)
}

func postTrigger(t *testing.T, h *IngestionHandler, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return doRequest(t, http.MethodPost, "/api/v1/lines/ingestion/trigger", h.TriggerIngestion, func(c echo.Context) {
		if body != "" {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/lines/ingestion/trigger", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			c.SetRequest(req)
		}
	})
}

func TestTriggerIngestionDisabled(t *testing.T) {
	h := NewIngestionHandler(newTriggerIngestion(), []string{"basketball_nba"}, false)

	rec, body := postTrigger(t, h, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when no API key is configured", rec.Code)
	}
	assertErrorCode(t, body, "INGESTION_DISABLED")
}

func TestTriggerIngestionUnknownLeague(t *testing.T) {
	h := NewIngestionHandler(newTriggerIngestion(), []string{"basketball_nba"}, true)

	rec, body := postTrigger(t, h, `{"league":"XFL"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown league", rec.Code)
	}
	assertErrorCode(t, body, "INVALID_PARAMETER")
}

func TestTriggerIngestionMalformedBody(t *testing.T) {
	h := NewIngestionHandler(newTriggerIngestion(), []string{"basketball_nba"}, true)

	rec, body := postTrigger(t, h, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed JSON", rec.Code)
	}
	assertErrorCode(t, body, "INVALID_BODY")
}

func TestTriggerIngestionQueuesLeague(t *testing.T) {
	h := NewIngestionHandler(newTriggerIngestion(), []string{"basketball_nba"}, true)

	rec, body := postTrigger(t, h, `{"league":"NBA"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	data := body["data"].(map[string]any)
	if data["message"] != "ingestion queued" || data["league"] != "NBA" {
		t.Errorf("data = %v, want queued NBA", data)
	}
	assertMeta(t, body)
}

func TestTriggerIngestionEmptyBodyUsesDefaults(t *testing.T) {
	h := NewIngestionHandler(newTriggerIngestion(), []string{"basketball_nba"}, true)

	rec, body := postTrigger(t, h, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 with default sport keys", rec.Code)
	}
	data := body["data"].(map[string]any)
	if data["league"] != "" {
		t.Errorf("league = %v, want empty (defaults used)", data["league"])
	}
}

func TestSportKeyForLeague(t *testing.T) {
	tests := []struct {
		league string
		want   string
	}{
		{"NBA", "basketball_nba"},
		{"NFL", "americanfootball_nfl"},
		{"EPL", "soccer_epl"},
		{"XFL", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := sportKeyForLeague(tt.league); got != tt.want {
			t.Errorf("sportKeyForLeague(%q) = %q, want %q", tt.league, got, tt.want)
		}
	}
}
