package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

const lineCacheTTL = 5 * time.Minute

// LineCache caches current lines in Redis.
type LineCache struct {
	rdb *redis.Client
}

// NewLineCache creates a new line cache.
func NewLineCache(rdb *redis.Client) *LineCache {
	return &LineCache{rdb: rdb}
}

func lineKey(gameID string) string {
	return fmt.Sprintf("lines:current:%s", gameID)
}

// SetCurrentLines caches current lines for a game.
func (c *LineCache) SetCurrentLines(ctx context.Context, gameID string, lines []model.LineSnapshot) error {
	data, err := json.Marshal(lines)
	if err != nil {
		return fmt.Errorf("marshal lines for cache: %w", err)
	}
	return c.rdb.Set(ctx, lineKey(gameID), data, lineCacheTTL).Err()
}

// GetCurrentLines returns cached lines for a game, or nil if not cached.
func (c *LineCache) GetCurrentLines(ctx context.Context, gameID string) ([]model.LineSnapshot, error) {
	data, err := c.rdb.Get(ctx, lineKey(gameID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached lines: %w", err)
	}

	var lines []model.LineSnapshot
	if err := json.Unmarshal(data, &lines); err != nil {
		return nil, fmt.Errorf("unmarshal cached lines: %w", err)
	}
	return lines, nil
}

// InvalidateGame removes cached lines for a game.
func (c *LineCache) InvalidateGame(ctx context.Context, gameID string) error {
	return c.rdb.Del(ctx, lineKey(gameID)).Err()
}
