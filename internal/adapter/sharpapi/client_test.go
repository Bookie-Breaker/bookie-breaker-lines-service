package sharpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/sharpapi"
)

const frameJSON = `{"event_id": "wc-semi-1", "sport_key": "soccer_fifa_world_cup", ` +
	`"commence_time": "2026-07-10T19:00:00Z", "home_team": "Argentina", "away_team": "France", ` +
	`"bookmaker": {"key": "draftkings", "title": "DraftKings"}, ` +
	`"market": {"key": "h2h", "outcomes": [{"name": "Argentina", "price": 2.10}, ` +
	`{"name": "Draw", "price": 3.30}, {"name": "France", "price": 3.60}]}, ` +
	`"captured_at": "2026-07-10T19:41:02Z"}`

// sseServer serves the given raw SSE body on /v1/stream and closes.
func sseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stream" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept header = %q, want text/event-stream", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
		w.(http.Flusher).Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, frames <-chan sharpapi.Frame, errs <-chan error) ([]sharpapi.Frame, error) {
	t.Helper()
	var got []sharpapi.Frame
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return got, <-errs
			}
			got = append(got, f)
		case <-timeout:
			t.Fatal("timed out waiting for stream to close")
		}
	}
}

func TestStreamSingleFrame(t *testing.T) {
	srv := sseServer(t, http.StatusOK, "data: "+frameJSON+"\n\n")
	client := sharpapi.NewClient(srv.URL, "", nil)

	frames, errs := client.Stream(context.Background())
	got, err := collect(t, frames, errs)
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("frames = %d, want 1", len(got))
	}

	f := got[0]
	if f.EventID != "wc-semi-1" || f.SportKey != "soccer_fifa_world_cup" {
		t.Errorf("unexpected frame identity: %+v", f)
	}
	if f.Bookmaker.Key != "draftkings" || f.Market.Key != "h2h" {
		t.Errorf("unexpected bookmaker/market: %+v", f)
	}
	if len(f.Market.Outcomes) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(f.Market.Outcomes))
	}
	if f.Market.Outcomes[1].Name != "Draw" || f.Market.Outcomes[1].Price != 3.30 {
		t.Errorf("draw outcome = %+v", f.Market.Outcomes[1])
	}
	if want := time.Date(2026, 7, 10, 19, 41, 2, 0, time.UTC); !f.CapturedAt.Equal(want) {
		t.Errorf("captured_at = %v, want %v", f.CapturedAt, want)
	}
}

func TestStreamMultiLineData(t *testing.T) {
	// One event split across multiple data: lines joins with newlines,
	// which is whitespace inside JSON.
	split := `{"event_id": "wc-semi-1",` + "\ndata: " +
		`"sport_key": "soccer_fifa_world_cup", "home_team": "Argentina", "away_team": "France",` + "\ndata: " +
		`"bookmaker": {"key": "draftkings", "title": "DraftKings"},` + "\ndata: " +
		`"market": {"key": "h2h", "outcomes": [{"name": "Argentina", "price": 2.10}]}}`
	srv := sseServer(t, http.StatusOK, "data: "+split+"\n\n")
	client := sharpapi.NewClient(srv.URL, "", nil)

	frames, errs := client.Stream(context.Background())
	got, err := collect(t, frames, errs)
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("frames = %d, want 1", len(got))
	}
	if got[0].EventID != "wc-semi-1" || len(got[0].Market.Outcomes) != 1 {
		t.Errorf("unexpected frame: %+v", got[0])
	}
}

func TestStreamKeepAlivesAndMalformedFramesAreSkipped(t *testing.T) {
	body := ": ping\n\n" +
		"data: {not valid json\n\n" +
		": another keep-alive\n" +
		"data: " + frameJSON + "\n\n" +
		": ping\n\n"
	srv := sseServer(t, http.StatusOK, body)
	client := sharpapi.NewClient(srv.URL, "", nil)

	frames, errs := client.Stream(context.Background())
	got, err := collect(t, frames, errs)
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("frames = %d, want 1 (malformed skipped, keep-alives ignored)", len(got))
	}
	if got[0].EventID != "wc-semi-1" {
		t.Errorf("event_id = %q", got[0].EventID)
	}
}

func TestStreamEndWithoutTrailingBlankLineDispatches(t *testing.T) {
	// Stream closes mid-event: the final data line has no blank-line
	// terminator but is still dispatched at EOF.
	srv := sseServer(t, http.StatusOK, "data: "+frameJSON+"\n")
	client := sharpapi.NewClient(srv.URL, "", nil)

	frames, errs := client.Stream(context.Background())
	got, err := collect(t, frames, errs)
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("frames = %d, want 1", len(got))
	}
}

func TestStreamNon200IsTerminalError(t *testing.T) {
	srv := sseServer(t, http.StatusServiceUnavailable, "down")
	client := sharpapi.NewClient(srv.URL, "", nil)

	frames, errs := client.Stream(context.Background())
	got, err := collect(t, frames, errs)
	if len(got) != 0 {
		t.Fatalf("frames = %d, want 0", len(got))
	}
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestStreamSendsAuthorizationWhenKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()

	client := sharpapi.NewClient(srv.URL, "secret", nil)
	frames, errs := client.Stream(context.Background())
	if _, err := collect(t, frames, errs); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
}

func TestStreamClosesOnContextCancel(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, ": ping\n\n")
		w.(http.Flusher).Flush()
		<-blocked // hold the stream open until the test finishes
	}))
	defer srv.Close()
	defer close(blocked)

	ctx, cancel := context.WithCancel(context.Background())
	client := sharpapi.NewClient(srv.URL, "", nil)
	frames, errs := client.Stream(ctx)

	cancel()

	select {
	case _, ok := <-frames:
		if ok {
			t.Fatal("received unexpected frame")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("frame channel did not close after context cancel")
	}
	if err := <-errs; err != nil {
		t.Errorf("expected no terminal error on cancel, got %v", err)
	}
}
