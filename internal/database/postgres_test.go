package database

import (
	"context"
	"testing"
	"time"
)

func TestNewPoolRejectsMalformedURL(t *testing.T) {
	if _, err := NewPool(context.Background(), "://not-a-url"); err == nil {
		t.Error("expected parse error for malformed database URL")
	}
}

func TestNewPoolFailsPingWhenUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Config parses fine, but the ping cannot reach anything on port 1.
	if _, err := NewPool(ctx, "postgres://user:pass@127.0.0.1:1/nope?connect_timeout=1"); err == nil {
		t.Error("expected ping failure for unreachable database")
	}
}
