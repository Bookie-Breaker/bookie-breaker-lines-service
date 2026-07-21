// In-package cache tests against real Redis via testcontainers, skipped when
// Docker is unavailable (mirroring tests/integration).
package cache

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

var (
	redisOnce      sync.Once
	redisContainer *tcredis.RedisContainer
	redisURL       string
	redisStartErr  error
)

func testRedisURL(t *testing.T) string {
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
		redisURL, redisStartErr = redisContainer.ConnectionString(ctx)
	})
	if redisStartErr != nil {
		t.Skipf("skipping: Docker/Redis unavailable: %v", redisStartErr)
	}
	return redisURL
}

func TestMain(m *testing.M) {
	code := m.Run()
	if redisContainer != nil {
		_ = testcontainers.TerminateContainer(redisContainer)
	}
	os.Exit(code)
}

func testClient(t *testing.T) *redis.Client {
	t.Helper()
	client, err := NewClient(context.Background(), testRedisURL(t))
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestNewClient(t *testing.T) {
	ctx := context.Background()

	t.Run("connects and pings", func(t *testing.T) {
		client := testClient(t)
		if err := client.Ping(ctx).Err(); err != nil {
			t.Errorf("ping after NewClient failed: %v", err)
		}
	})

	t.Run("rejects malformed URL", func(t *testing.T) {
		if _, err := NewClient(ctx, "not-a-redis-url"); err == nil {
			t.Error("expected parse error for malformed URL")
		}
	})

	t.Run("fails when unreachable", func(t *testing.T) {
		pingCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		if _, err := NewClient(pingCtx, "redis://127.0.0.1:1"); err == nil {
			t.Error("expected ping error for unreachable Redis")
		}
	})
}

func cachedLine(id string) model.LineSnapshot {
	line := -3.5
	return model.LineSnapshot{
		ID:             id,
		GameExternalID: "g1",
		SportsbookID:   "sb-1",
		MarketType:     model.MarketSpread,
		Selection:      "Los Angeles Lakers -3.5",
		LineValue:      &line,
		OddsAmerican:   -110,
		OddsDecimal:    1.91,
		CapturedAt:     time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

func TestLineCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	c := NewLineCache(client)

	want := []model.LineSnapshot{cachedLine("l1"), cachedLine("l2")}
	if err := c.SetCurrentLines(ctx, "cachetest-game", want); err != nil {
		t.Fatalf("SetCurrentLines failed: %v", err)
	}

	got, err := c.GetCurrentLines(ctx, "cachetest-game")
	if err != nil {
		t.Fatalf("GetCurrentLines failed: %v", err)
	}
	if len(got) != 2 || got[0].ID != "l1" || got[1].ID != "l2" {
		t.Fatalf("cached lines = %+v, want the two seeded snapshots", got)
	}
	if got[0].LineValue == nil || *got[0].LineValue != -3.5 || !got[0].CapturedAt.Equal(want[0].CapturedAt) {
		t.Errorf("cached line = %+v, want full field round-trip", got[0])
	}

	// The cache entry carries a TTL so stale lines expire on their own.
	ttl, err := client.TTL(ctx, "lines:current:cachetest-game").Result()
	if err != nil {
		t.Fatalf("TTL lookup failed: %v", err)
	}
	if ttl <= 0 || ttl > 5*time.Minute {
		t.Errorf("ttl = %v, want (0, 5m]", ttl)
	}
}

func TestLineCacheMissReturnsNil(t *testing.T) {
	c := NewLineCache(testClient(t))
	got, err := c.GetCurrentLines(context.Background(), "cachetest-never-set")
	if err != nil {
		t.Fatalf("cache miss must not error: %v", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil on miss", got)
	}
}

func TestLineCacheCorruptedEntry(t *testing.T) {
	ctx := context.Background()
	client := testClient(t)
	c := NewLineCache(client)

	if err := client.Set(ctx, "lines:current:cachetest-corrupt", "{not json", time.Minute).Err(); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}
	if _, err := c.GetCurrentLines(ctx, "cachetest-corrupt"); err == nil {
		t.Error("corrupted cache entries must surface an unmarshal error")
	}
}

func TestLineCacheInvalidateGame(t *testing.T) {
	ctx := context.Background()
	c := NewLineCache(testClient(t))

	if err := c.SetCurrentLines(ctx, "cachetest-invalidate", []model.LineSnapshot{cachedLine("l1")}); err != nil {
		t.Fatalf("SetCurrentLines failed: %v", err)
	}
	if err := c.InvalidateGame(ctx, "cachetest-invalidate"); err != nil {
		t.Fatalf("InvalidateGame failed: %v", err)
	}
	got, err := c.GetCurrentLines(ctx, "cachetest-invalidate")
	if err != nil || got != nil {
		t.Errorf("after invalidation got (%+v, %v), want (nil, nil)", got, err)
	}
}
