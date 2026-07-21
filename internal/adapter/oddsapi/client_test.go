package oddsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient points a Client at a stub Odds API server.
func newTestClient(handler http.Handler) (*Client, *httptest.Server) {
	srv := httptest.NewServer(handler)
	return NewClient("test-key", srv.URL), srv
}

func TestNewClientDefaultBaseURL(t *testing.T) {
	c := NewClient("k", "")
	if c.baseURL != "https://api.the-odds-api.com" {
		t.Errorf("baseURL = %q, want production default", c.baseURL)
	}
	if c.httpClient.Timeout != 30*time.Second {
		t.Errorf("timeout = %v, want 30s", c.httpClient.Timeout)
	}

	c = NewClient("k", "http://localhost:9999")
	if c.baseURL != "http://localhost:9999" {
		t.Errorf("baseURL = %q, want explicit override", c.baseURL)
	}
}

func TestGetOddsSuccess(t *testing.T) {
	var gotPath, gotQuery string
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("x-requests-used", "42")
		w.Header().Set("x-requests-remaining", "458")
		_, _ = w.Write([]byte(`[{"id":"ev1","sport_key":"basketball_nba","home_team":"Lakers","away_team":"Celtics",
			"bookmakers":[{"key":"draftkings","markets":[{"key":"h2h","outcomes":[{"name":"Lakers","price":1.91}]}]}]}]`))
	}))
	defer srv.Close()

	result, err := c.GetOdds(context.Background(), "basketball_nba", []string{"h2h", "spreads"})
	if err != nil {
		t.Fatalf("GetOdds failed: %v", err)
	}
	if gotPath != "/v4/sports/basketball_nba/odds" {
		t.Errorf("path = %q, want /v4/sports/basketball_nba/odds", gotPath)
	}
	if !strings.Contains(gotQuery, "markets=h2h,spreads") || !strings.Contains(gotQuery, "apiKey=test-key") {
		t.Errorf("query = %q, want markets and apiKey params", gotQuery)
	}
	if result.HTTPStatus != http.StatusOK {
		t.Errorf("HTTPStatus = %d, want 200", result.HTTPStatus)
	}
	if result.RequestsUsed != 42 || result.RequestsLeft != 458 {
		t.Errorf("quota = (%d, %d), want (42, 458)", result.RequestsUsed, result.RequestsLeft)
	}
	if len(result.Events) != 1 || result.Events[0].ID != "ev1" {
		t.Fatalf("events = %+v, want one event ev1", result.Events)
	}
	if len(result.Events[0].Bookmakers) != 1 || result.Events[0].Bookmakers[0].Key != "draftkings" {
		t.Errorf("bookmakers not decoded: %+v", result.Events[0].Bookmakers)
	}
	if len(result.RawBody) == 0 {
		t.Error("RawBody should carry the response for archival")
	}
}

func TestGetOddsOmitsMarketsParamWhenEmpty(t *testing.T) {
	var gotQuery string
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	if _, err := c.GetOdds(context.Background(), "basketball_nba", nil); err != nil {
		t.Fatalf("GetOdds failed: %v", err)
	}
	if strings.Contains(gotQuery, "markets=") {
		t.Errorf("query = %q, should not contain markets param", gotQuery)
	}
}

func TestGetOddsNon200ReturnsBodyForArchival(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-requests-used", "500")
		w.Header().Set("x-requests-remaining", "0")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid key"}`))
	}))
	defer srv.Close()

	result, err := c.GetOdds(context.Background(), "basketball_nba", nil)
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Errorf("error = %v, want status 401 mention", err)
	}
	if result == nil {
		t.Fatal("result must be returned alongside the error so callers can archive the failure")
	}
	if result.HTTPStatus != http.StatusUnauthorized || !strings.Contains(string(result.RawBody), "invalid key") {
		t.Errorf("result = %+v, want archived 401 body", result)
	}
	if result.RequestsLeft != 0 || result.RequestsUsed != 500 {
		t.Errorf("quota = (%d, %d), want (500, 0)", result.RequestsUsed, result.RequestsLeft)
	}
}

func TestGetOddsMalformedJSON(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	result, err := c.GetOdds(context.Background(), "basketball_nba", nil)
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("err = %v, want decode error", err)
	}
	if result == nil || string(result.RawBody) != `{not json` {
		t.Error("raw body should still be returned on decode failure")
	}
}

func TestGetOddsConnectionError(t *testing.T) {
	c := NewClient("k", "http://127.0.0.1:1")
	result, err := c.GetOdds(context.Background(), "basketball_nba", nil)
	if err == nil || !strings.Contains(err.Error(), "execute request") {
		t.Fatalf("err = %v, want execute request error", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want nil when no body was received", result)
	}
}

func TestGetOddsContextCanceled(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.GetOdds(ctx, "basketball_nba", nil); err == nil {
		t.Fatal("expected error when context deadline is exceeded")
	}
}

func TestGetEventsSuccess(t *testing.T) {
	var gotPath string
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("x-requests-used", "7")
		w.Header().Set("x-requests-remaining", "493")
		_, _ = w.Write([]byte(`[{"id":"ev1","sport_key":"soccer_epl","home_team":"Arsenal","away_team":"Chelsea"},
			{"id":"ev2","sport_key":"soccer_epl","home_team":"Liverpool","away_team":"Spurs"}]`))
	}))
	defer srv.Close()

	result, err := c.GetEvents(context.Background(), "soccer_epl")
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if gotPath != "/v4/sports/soccer_epl/events" {
		t.Errorf("path = %q, want /v4/sports/soccer_epl/events", gotPath)
	}
	if len(result.Events) != 2 || result.Events[1].HomeTeam != "Liverpool" {
		t.Fatalf("events = %+v, want 2 stubs", result.Events)
	}
	if result.RequestsUsed != 7 || result.RequestsLeft != 493 {
		t.Errorf("quota = (%d, %d), want (7, 493)", result.RequestsUsed, result.RequestsLeft)
	}
}

func TestGetEventsErrors(t *testing.T) {
	t.Run("non-200 keeps body", func(t *testing.T) {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`quota exceeded`))
		}))
		defer srv.Close()

		result, err := c.GetEvents(context.Background(), "soccer_epl")
		if err == nil || !strings.Contains(err.Error(), "status 429") {
			t.Fatalf("err = %v, want 429 error", err)
		}
		if result == nil || result.HTTPStatus != http.StatusTooManyRequests {
			t.Errorf("result = %+v, want archived 429", result)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"not":"an array"}`))
		}))
		defer srv.Close()

		if _, err := c.GetEvents(context.Background(), "soccer_epl"); err == nil || !strings.Contains(err.Error(), "decode response") {
			t.Fatalf("err = %v, want decode error", err)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		c := NewClient("k", "http://127.0.0.1:1")
		result, err := c.GetEvents(context.Background(), "soccer_epl")
		if err == nil {
			t.Fatal("expected connection error")
		}
		if result != nil {
			t.Errorf("result = %+v, want nil with no body", result)
		}
	})
}

func TestGetEventOddsSuccess(t *testing.T) {
	var gotPath, gotQuery string
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		// Per-event endpoint returns a single Event object, not an array.
		_, _ = w.Write([]byte(`{"id":"ev1","sport_key":"soccer_epl","home_team":"Arsenal","away_team":"Chelsea",
			"bookmakers":[{"key":"draftkings","markets":[{"key":"player_shots",
			"outcomes":[{"name":"Over","description":"Bukayo Saka","price":1.85,"point":2.5}]}]}]}`))
	}))
	defer srv.Close()

	result, err := c.GetEventOdds(context.Background(), "soccer_epl", "ev1", []string{"player_shots"})
	if err != nil {
		t.Fatalf("GetEventOdds failed: %v", err)
	}
	if gotPath != "/v4/sports/soccer_epl/events/ev1/odds" {
		t.Errorf("path = %q, want per-event odds path", gotPath)
	}
	if !strings.Contains(gotQuery, "markets=player_shots") {
		t.Errorf("query = %q, want markets param", gotQuery)
	}
	if result.Event.ID != "ev1" || len(result.Event.Bookmakers) != 1 {
		t.Fatalf("event = %+v, want decoded single event", result.Event)
	}
	out := result.Event.Bookmakers[0].Markets[0].Outcomes[0]
	if out.Description != "Bukayo Saka" || out.Point == nil || *out.Point != 2.5 {
		t.Errorf("outcome = %+v, want prop outcome with description and point", out)
	}
}

func TestGetEventOddsErrors(t *testing.T) {
	t.Run("non-200 keeps body", func(t *testing.T) {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`event not found`))
		}))
		defer srv.Close()

		result, err := c.GetEventOdds(context.Background(), "soccer_epl", "missing", nil)
		if err == nil || !strings.Contains(err.Error(), "status 404") {
			t.Fatalf("err = %v, want 404 error", err)
		}
		if result == nil || !strings.Contains(string(result.RawBody), "event not found") {
			t.Errorf("result = %+v, want archived 404 body", result)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[]`)) // array where an object is expected
		}))
		defer srv.Close()

		if _, err := c.GetEventOdds(context.Background(), "soccer_epl", "ev1", nil); err == nil || !strings.Contains(err.Error(), "decode response") {
			t.Fatalf("err = %v, want decode error", err)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		c := NewClient("k", "http://127.0.0.1:1")
		result, err := c.GetEventOdds(context.Background(), "soccer_epl", "ev1", nil)
		if err == nil {
			t.Fatal("expected connection error")
		}
		if result != nil {
			t.Errorf("result = %+v, want nil with no body", result)
		}
	})
}

func TestGetSports(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v4/sports" {
				t.Errorf("path = %q, want /v4/sports", r.URL.Path)
			}
			_, _ = w.Write([]byte(`[{"key":"basketball_nba","title":"NBA","active":true}]`))
		}))
		defer srv.Close()

		sports, err := c.GetSports(context.Background())
		if err != nil {
			t.Fatalf("GetSports failed: %v", err)
		}
		if len(sports) != 1 || sports[0].Key != "basketball_nba" || !sports[0].Active {
			t.Errorf("sports = %+v, want one active NBA entry", sports)
		}
	})

	t.Run("non-200", func(t *testing.T) {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
		}))
		defer srv.Close()

		if _, err := c.GetSports(context.Background()); err == nil || !strings.Contains(err.Error(), "status 500") {
			t.Fatalf("err = %v, want 500 error", err)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{not json`))
		}))
		defer srv.Close()

		if _, err := c.GetSports(context.Background()); err == nil || !strings.Contains(err.Error(), "decode response") {
			t.Fatalf("err = %v, want decode error", err)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		c := NewClient("k", "http://127.0.0.1:1")
		if _, err := c.GetSports(context.Background()); err == nil {
			t.Fatal("expected connection error")
		}
	})
}
