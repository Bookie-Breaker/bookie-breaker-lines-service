package service

import (
	"testing"
	"time"
)

func TestNextBackoffProgressionIsCapped(t *testing.T) {
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second, // 32s capped
		30 * time.Second, // stays at the cap
		30 * time.Second,
	}

	current := liveBackoffInitial // 500ms
	for i, expected := range want {
		current = nextBackoff(current)
		if current != expected {
			t.Fatalf("step %d: backoff = %v, want %v", i+1, current, expected)
		}
	}
}

func TestJitterStaysWithinBounds(t *testing.T) {
	d := 8 * time.Second
	for i := 0; i < 1000; i++ {
		got := jitter(d)
		if got < d/2 || got > d {
			t.Fatalf("jitter(%v) = %v, want within [%v, %v]", d, got, d/2, d)
		}
	}
}

func TestJitterZeroAndNegative(t *testing.T) {
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %v, want 0", got)
	}
	if got := jitter(-time.Second); got != 0 {
		t.Errorf("jitter(-1s) = %v, want 0", got)
	}
}
