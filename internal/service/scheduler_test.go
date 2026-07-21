package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRunsImmediatelyAndOnTicks(t *testing.T) {
	var oddsRequests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oddsRequests.Add(1)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	gameRepo := &closingGameRepo{}
	closing := NewClosingLineService(&closingLineRepo{}, gameRepo)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	// Start blocks until the context is canceled.
	NewScheduler(ingestion, closing, 30*time.Millisecond, []string{"basketball_nba", "soccer_epl"}).Start(ctx)

	// The immediate startup cycle alone fires one request per sport key, and
	// at least one tick should fit in the window.
	if got := oddsRequests.Load(); got < 4 {
		t.Errorf("odds requests = %d, want >= 4 (2 sports x startup + >=1 tick)", got)
	}
	gameRepo.mu.Lock()
	dueCalls := gameRepo.dueCalls
	gameRepo.mu.Unlock()
	if dueCalls < 2 {
		t.Errorf("closing sweeps = %d, want >= 2 (one per cycle)", dueCalls)
	}
}

func TestSchedulerCycleStopsMidLoopOnCancel(t *testing.T) {
	var oddsRequests atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Cancel while the first sport is in flight so the loop's ctx guard
		// (and the closing-sweep guard) trip before the second sport runs.
		oddsRequests.Add(1)
		cancel()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	gameRepo := &closingGameRepo{}
	closing := NewClosingLineService(&closingLineRepo{}, gameRepo)

	NewScheduler(ingestion, closing, time.Hour, []string{"basketball_nba", "soccer_epl"}).Start(ctx)

	if got := oddsRequests.Load(); got != 1 {
		t.Errorf("odds requests = %d, want 1 (second sport skipped after cancel)", got)
	}
	gameRepo.mu.Lock()
	dueCalls := gameRepo.dueCalls
	gameRepo.mu.Unlock()
	if dueCalls != 0 {
		t.Errorf("closing sweeps = %d, want 0 after cancel", dueCalls)
	}
}

func TestPropSchedulerRunsImmediatelyAndOnTicks(t *testing.T) {
	var eventsRequests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		eventsRequests.Add(1)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	props := newPropFixture(t, srv.URL, ingestion)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	NewPropScheduler(props, 30*time.Millisecond, []string{"soccer_epl"}).Start(ctx)

	if got := eventsRequests.Load(); got < 2 {
		t.Errorf("events requests = %d, want >= 2 (startup + >=1 tick)", got)
	}
}

func TestPropSchedulerSurvivesCycleErrors(t *testing.T) {
	// The events endpoint is unreachable: every cycle fails, is logged, and
	// the scheduler keeps running until the context ends.
	ingestion, _, _, _ := newIngestionFixture(t, "http://127.0.0.1:1", deadRedis())
	props := newPropFixture(t, "http://127.0.0.1:1", ingestion)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	NewPropScheduler(props, 30*time.Millisecond, []string{"soccer_epl"}).Start(ctx)
	// Reaching here proves a failing cycle does not kill the loop.
}

func TestPropSchedulerStopsOnCanceledContext(t *testing.T) {
	var eventsRequests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		eventsRequests.Add(1)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	ingestion, _, _, _ := newIngestionFixture(t, srv.URL, deadRedis())
	props := newPropFixture(t, srv.URL, ingestion)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	NewPropScheduler(props, time.Hour, []string{"soccer_epl"}).Start(ctx)

	if got := eventsRequests.Load(); got != 0 {
		t.Errorf("events requests = %d, want 0 when the context starts canceled", got)
	}
}
