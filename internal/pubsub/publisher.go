package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const channelLinesUpdated = "events:lines.updated"

// LinesUpdatedEvent matches the payload schema in redis-schemas.md.
type LinesUpdatedEvent struct {
	Event              string   `json:"event"`
	Timestamp          string   `json:"timestamp"`
	League             string   `json:"league"`
	GameIDs            []string `json:"game_ids"`
	MarketTypes        []string `json:"market_types"`
	SportsbooksUpdated []string `json:"sportsbooks_updated"`
	ChangeCount        int      `json:"change_count"`
	Source             string   `json:"source"`
}

// Publisher publishes events to Redis pub/sub channels.
type Publisher struct {
	rdb *redis.Client
}

// NewPublisher creates a new Redis pub/sub publisher.
func NewPublisher(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

// PublishLinesUpdated publishes a lines.updated event.
func (p *Publisher) PublishLinesUpdated(ctx context.Context, event LinesUpdatedEvent) error {
	event.Event = "lines.updated"
	event.Timestamp = time.Now().UTC().Format(time.RFC3339)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal lines.updated event: %w", err)
	}

	if err := p.rdb.Publish(ctx, channelLinesUpdated, payload).Err(); err != nil {
		return fmt.Errorf("publish to %s: %w", channelLinesUpdated, err)
	}

	slog.Info("published lines.updated event",
		"league", event.League,
		"games", len(event.GameIDs),
		"changes", event.ChangeCount,
	)

	return nil
}
