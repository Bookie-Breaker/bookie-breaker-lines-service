package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/sharpapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

// scriptedStreamer replays one scripted session per Stream call; calls beyond
// the script return an immediately-closed stream with a connection error.
type scriptedStreamer struct {
	mu       sync.Mutex
	sessions [][]sharpapi.Frame
	errs     []error
	calls    int
}

func (f *scriptedStreamer) Stream(_ context.Context) (<-chan sharpapi.Frame, <-chan error) {
	f.mu.Lock()
	i := f.calls
	f.calls++
	f.mu.Unlock()

	var session []sharpapi.Frame
	err := errors.New("stream exhausted")
	if i < len(f.sessions) {
		session = f.sessions[i]
		err = f.errs[i]
	}

	frames := make(chan sharpapi.Frame, len(session))
	for _, fr := range session {
		frames <- fr
	}
	close(frames)

	errCh := make(chan error, 1)
	errCh <- err
	return frames, errCh
}

func (f *scriptedStreamer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// countingSBRepo counts GetAll calls and can serve different registries per
// call, for the stale-map refresh path.
type countingSBRepo struct {
	repository.SportsbookRepository

	mu      sync.Mutex
	perCall [][]model.Sportsbook
	err     error
	calls   int
}

func (f *countingSBRepo) GetAll(_ context.Context, _, _ *bool) ([]model.Sportsbook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	i := f.calls
	f.calls++
	if i >= len(f.perCall) {
		i = len(f.perCall) - 1
	}
	return f.perCall[i], nil
}

func (f *countingSBRepo) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func liveFrame() sharpapi.Frame {
	return sharpapi.Frame{
		EventID:      "ev-live",
		SportKey:     "basketball_nba",
		CommenceTime: time.Now().UTC().Add(time.Hour),
		HomeTeam:     "Los Angeles Lakers",
		AwayTeam:     "Boston Celtics",
		Bookmaker:    sharpapi.Bookmaker{Key: "draftkings"},
		Market: sharpapi.Market{
			Key:      "h2h",
			Outcomes: []sharpapi.Outcome{{Name: "Los Angeles Lakers", Price: 1.80}},
		},
		CapturedAt: time.Now().UTC(),
	}
}

func TestNewLiveConsumerDefaultsThreshold(t *testing.T) {
	c := NewLiveConsumer(&scriptedStreamer{}, nil, nil, nil, 0)
	if c.failureThreshold != defaultLiveFailureThreshold {
		t.Errorf("threshold = %d, want default %d", c.failureThreshold, defaultLiveFailureThreshold)
	}
	c = NewLiveConsumer(&scriptedStreamer{}, nil, nil, nil, 7)
	if c.failureThreshold != 7 {
		t.Errorf("threshold = %d, want 7", c.failureThreshold)
	}
}

func TestHandleFramePersistsLiveSnapshot(t *testing.T) {
	lineRepo := &ingestLineRepo{}
	gameRepo := &ingestGameRepo{}
	sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{{ID: "sb-uuid-1", Key: "draftkings"}}}}
	ingestion := newLiveIngestion(lineRepo, gameRepo)
	consumer := NewLiveConsumer(&scriptedStreamer{}, ingestion, gameRepo, sbRepo, 0)

	if err := consumer.handleFrame(context.Background(), liveFrame()); err != nil {
		t.Fatalf("handleFrame failed: %v", err)
	}

	games := gameRepo.upsertedGames()
	if len(games) != 1 || games[0].GameExternalID != "ev-live" || games[0].League != model.LeagueNBA {
		t.Fatalf("upserted games = %+v, want ev-live/NBA", games)
	}

	inserted := lineRepo.insertedSnapshots()
	if len(inserted) != 1 {
		t.Fatalf("inserted = %+v, want one live snapshot", inserted)
	}
	snap := inserted[0]
	if !snap.IsLive || snap.Source != sharpapi.Source {
		t.Errorf("snapshot = %+v, want live sharpapi snapshot", snap)
	}
	if snap.Selection != "Los Angeles Lakers" || snap.SportsbookID != "sb-uuid-1" {
		t.Errorf("snapshot = %+v, want mapped selection and sportsbook UUID", snap)
	}

	// Second frame reuses the cached sportsbook map: no second GetAll.
	if err := consumer.handleFrame(context.Background(), liveFrame()); err != nil {
		t.Fatalf("second handleFrame failed: %v", err)
	}
	if sbRepo.callCount() != 1 {
		t.Errorf("GetAll calls = %d, want 1 (map cached)", sbRepo.callCount())
	}
}

func TestHandleFrameRefreshesUnknownSportsbook(t *testing.T) {
	lineRepo := &ingestLineRepo{}
	gameRepo := &ingestGameRepo{}
	// First lookup misses the book; the refresh finds it (added since cache).
	sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{
		{},
		{{ID: "sb-uuid-1", Key: "draftkings"}},
	}}
	consumer := NewLiveConsumer(&scriptedStreamer{}, newLiveIngestion(lineRepo, gameRepo), gameRepo, sbRepo, 0)

	if err := consumer.handleFrame(context.Background(), liveFrame()); err != nil {
		t.Fatalf("handleFrame failed after refresh: %v", err)
	}
	if sbRepo.callCount() != 2 {
		t.Errorf("GetAll calls = %d, want 2 (initial + refresh)", sbRepo.callCount())
	}
	if len(lineRepo.insertedSnapshots()) != 1 {
		t.Error("snapshot should persist once the refreshed map resolves the book")
	}
}

func TestHandleFrameStillUnknownSportsbookFails(t *testing.T) {
	sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{}}}
	lineRepo := &ingestLineRepo{}
	gameRepo := &ingestGameRepo{}
	consumer := NewLiveConsumer(&scriptedStreamer{}, newLiveIngestion(lineRepo, gameRepo), gameRepo, sbRepo, 0)

	err := consumer.handleFrame(context.Background(), liveFrame())
	if !errors.Is(err, sharpapi.ErrUnknownSportsbook) {
		t.Fatalf("err = %v, want ErrUnknownSportsbook after refresh", err)
	}
	if len(lineRepo.insertedSnapshots()) != 0 {
		t.Error("nothing should persist for an unknown book")
	}
}

func TestHandleFrameErrors(t *testing.T) {
	t.Run("sportsbook lookup failure", func(t *testing.T) {
		sbRepo := &countingSBRepo{err: errors.New("db down")}
		consumer := NewLiveConsumer(&scriptedStreamer{}, newLiveIngestion(&ingestLineRepo{}, &ingestGameRepo{}), &ingestGameRepo{}, sbRepo, 0)
		if err := consumer.handleFrame(context.Background(), liveFrame()); err == nil || !strings.Contains(err.Error(), "fetch sportsbooks") {
			t.Fatalf("err = %v, want fetch sportsbooks error", err)
		}
	})

	t.Run("unknown sport key", func(t *testing.T) {
		sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{{ID: "sb-uuid-1", Key: "draftkings"}}}}
		consumer := NewLiveConsumer(&scriptedStreamer{}, newLiveIngestion(&ingestLineRepo{}, &ingestGameRepo{}), &ingestGameRepo{}, sbRepo, 0)
		frame := liveFrame()
		frame.SportKey = "quidditch_premier"
		if err := consumer.handleFrame(context.Background(), frame); err == nil || !strings.Contains(err.Error(), "unknown sport_key") {
			t.Fatalf("err = %v, want unknown sport_key error", err)
		}
	})

	t.Run("game upsert failure", func(t *testing.T) {
		sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{{ID: "sb-uuid-1", Key: "draftkings"}}}}
		gameRepo := &ingestGameRepo{upsertErr: errors.New("db down")}
		consumer := NewLiveConsumer(&scriptedStreamer{}, newLiveIngestion(&ingestLineRepo{}, gameRepo), gameRepo, sbRepo, 0)
		if err := consumer.handleFrame(context.Background(), liveFrame()); err == nil || !strings.Contains(err.Error(), "upsert game") {
			t.Fatalf("err = %v, want upsert game error", err)
		}
	})

	t.Run("persist failure", func(t *testing.T) {
		sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{{ID: "sb-uuid-1", Key: "draftkings"}}}}
		lineRepo := &ingestLineRepo{latestErr: errors.New("db down")}
		consumer := NewLiveConsumer(&scriptedStreamer{}, newLiveIngestion(lineRepo, &ingestGameRepo{}), &ingestGameRepo{}, sbRepo, 0)
		if err := consumer.handleFrame(context.Background(), liveFrame()); err == nil || !strings.Contains(err.Error(), "fetch latest line values") {
			t.Fatalf("err = %v, want persist error", err)
		}
	})
}

func TestRunConsumesFramesAndReconnects(t *testing.T) {
	streamer := &scriptedStreamer{
		sessions: [][]sharpapi.Frame{{liveFrame()}},
		errs:     []error{nil},
	}
	lineRepo := &ingestLineRepo{}
	gameRepo := &ingestGameRepo{}
	sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{{ID: "sb-uuid-1", Key: "draftkings"}}}}
	consumer := NewLiveConsumer(streamer, newLiveIngestion(lineRepo, gameRepo), gameRepo, sbRepo, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	consumer.Run(ctx) // blocks until the context deadline

	if len(lineRepo.insertedSnapshots()) != 1 {
		t.Errorf("inserted = %d snapshots, want the streamed frame persisted", len(lineRepo.insertedSnapshots()))
	}
	if streamer.callCount() < 1 {
		t.Error("stream should have been opened at least once")
	}
}

func TestRunKeepsReconnectingThroughFailures(t *testing.T) {
	// Every session fails immediately: Run must keep reconnecting (crossing
	// the failure threshold) until the context ends, not exit early.
	streamer := &scriptedStreamer{}
	sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{}}}
	consumer := NewLiveConsumer(streamer, newLiveIngestion(&ingestLineRepo{}, &ingestGameRepo{}), &ingestGameRepo{}, sbRepo, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	consumer.Run(ctx)

	if streamer.callCount() < 2 {
		t.Errorf("stream calls = %d, want >= 2 (reconnect with backoff)", streamer.callCount())
	}
}

func TestConsumeStreamSkipsFailingFrames(t *testing.T) {
	// Every frame fails on an unknown sportsbook: the frame is skipped and
	// the session reports no successful frames.
	streamer := &scriptedStreamer{
		sessions: [][]sharpapi.Frame{{liveFrame(), liveFrame()}},
		errs:     []error{errors.New("stream reset")},
	}
	sbRepo := &countingSBRepo{perCall: [][]model.Sportsbook{{}}}
	lineRepo := &ingestLineRepo{}
	consumer := NewLiveConsumer(streamer, newLiveIngestion(lineRepo, &ingestGameRepo{}), &ingestGameRepo{}, sbRepo, 0)

	if gotFrame := consumer.consumeStream(context.Background()); gotFrame {
		t.Error("consumeStream = true, want false when every frame fails")
	}
	if len(lineRepo.insertedSnapshots()) != 0 {
		t.Error("failing frames must not persist snapshots")
	}
}

func TestHandleFrameRefreshFailure(t *testing.T) {
	// The initial map misses the book and the refresh itself fails: the
	// refresh error is surfaced.
	sbRepo := &refreshFailSBRepo{}
	consumer := NewLiveConsumer(&scriptedStreamer{}, newLiveIngestion(&ingestLineRepo{}, &ingestGameRepo{}), &ingestGameRepo{}, sbRepo, 0)

	if err := consumer.handleFrame(context.Background(), liveFrame()); err == nil || !strings.Contains(err.Error(), "fetch sportsbooks") {
		t.Fatalf("err = %v, want the refresh failure", err)
	}
}

// refreshFailSBRepo returns an empty registry on the first call and fails on
// the refresh.
type refreshFailSBRepo struct {
	repository.SportsbookRepository
	mu    sync.Mutex
	calls int
}

func (f *refreshFailSBRepo) GetAll(_ context.Context, _, _ *bool) ([]model.Sportsbook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return nil, nil
	}
	return nil, errors.New("db down")
}

// newLiveIngestion builds an IngestionService for the live write path with
// dead-Redis cache/publisher (their failures are warn-only).
func newLiveIngestion(lineRepo *ingestLineRepo, gameRepo *ingestGameRepo) *IngestionService {
	return NewIngestionService(nil, lineRepo, gameRepo, nil, nil,
		newDeadLineCache(), newDeadPublisher())
}
