// In-package pub/sub tests against real Redis via testcontainers, skipped
// when Docker is unavailable (mirroring tests/integration).
package pubsub

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

var (
	redisOnce      sync.Once
	redisContainer *tcredis.RedisContainer
	redisClient    *redis.Client
	redisStartErr  error
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	redisOnce.Do(func() {
		ctx := context.Background()
		provider, err := testcontainers.NewDockerProvider()
		if err != nil {
			redisStartErr = err
			return
		}
		healthErr := provider.Health(ctx)
		_ = provider.Close()
		if healthErr != nil {
			redisStartErr = healthErr
			return
		}
		redisContainer, err = tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			redisStartErr = err
			return
		}
		url, err := redisContainer.ConnectionString(ctx)
		if err != nil {
			redisStartErr = err
			return
		}
		opts, err := redis.ParseURL(url)
		if err != nil {
			redisStartErr = err
			return
		}
		redisClient = redis.NewClient(opts)
	})
	if redisStartErr != nil {
		t.Skipf("skipping: Docker/Redis unavailable: %v", redisStartErr)
	}
	return redisClient
}

func TestMain(m *testing.M) {
	code := m.Run()
	if redisClient != nil {
		_ = redisClient.Close()
	}
	if redisContainer != nil {
		_ = testcontainers.TerminateContainer(redisContainer)
	}
	os.Exit(code)
}

func TestPublishLinesUpdated(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	p := NewPublisher(rdb)

	sub := rdb.Subscribe(ctx, "events:lines.updated")
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	err := p.PublishLinesUpdated(ctx, LinesUpdatedEvent{
		League:             "NBA",
		GameIDs:            []string{"g1", "g2"},
		MarketTypes:        []string{"SPREAD"},
		SportsbooksUpdated: []string{"draftkings"},
		ChangeCount:        3,
		IsLive:             true,
		Source:             "sharpapi",
	})
	if err != nil {
		t.Fatalf("PublishLinesUpdated failed: %v", err)
	}

	select {
	case msg := <-sub.Channel():
		var got LinesUpdatedEvent
		if err := json.Unmarshal([]byte(msg.Payload), &got); err != nil {
			t.Fatalf("payload is not JSON: %v", err)
		}
		// Event name and timestamp are stamped by the publisher.
		if got.Event != "lines.updated" {
			t.Errorf("event = %q, want lines.updated", got.Event)
		}
		if ts, err := time.Parse(time.RFC3339, got.Timestamp); err != nil || time.Since(ts) > time.Minute {
			t.Errorf("timestamp = %q, want a fresh RFC3339 stamp (%v)", got.Timestamp, err)
		}
		if got.League != "NBA" || got.ChangeCount != 3 || !got.IsLive || got.Source != "sharpapi" {
			t.Errorf("event = %+v, want the published fields round-tripped", got)
		}
		if len(got.GameIDs) != 2 || got.GameIDs[0] != "g1" {
			t.Errorf("game_ids = %v, want [g1 g2]", got.GameIDs)
		}
		if len(got.MarketTypes) != 1 || got.MarketTypes[0] != "SPREAD" {
			t.Errorf("market_types = %v, want [SPREAD]", got.MarketTypes)
		}
		if len(got.SportsbooksUpdated) != 1 || got.SportsbooksUpdated[0] != "draftkings" {
			t.Errorf("sportsbooks_updated = %v, want [draftkings]", got.SportsbooksUpdated)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lines.updated event")
	}
}

func TestPublishLinesUpdatedUnreachableRedis(t *testing.T) {
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 10 * time.Millisecond, MaxRetries: -1})
	defer func() { _ = dead.Close() }()

	p := NewPublisher(dead)
	if err := p.PublishLinesUpdated(context.Background(), LinesUpdatedEvent{League: "NBA"}); err == nil {
		t.Error("publish must fail when Redis is unreachable")
	}
}
