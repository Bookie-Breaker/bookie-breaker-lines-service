package main

import (
	"context"
	"log/slog"
	"testing"
)

func TestSetupLoggerLevels(t *testing.T) {
	// setupLogger swaps the process-global default logger; restore it so
	// other tests in this binary keep their configuration.
	original := slog.Default()
	defer slog.SetDefault(original)

	ctx := context.Background()
	tests := []struct {
		level       string
		wantDebug   bool
		wantInfo    bool
		wantWarn    bool
		wantErrorOn bool
	}{
		{"debug", true, true, true, true},
		{"info", false, true, true, true},
		{"warn", false, false, true, true},
		{"error", false, false, false, true},
		{"unknown-defaults-to-info", false, true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			setupLogger(tt.level)
			h := slog.Default().Handler()
			if got := h.Enabled(ctx, slog.LevelDebug); got != tt.wantDebug {
				t.Errorf("debug enabled = %v, want %v", got, tt.wantDebug)
			}
			if got := h.Enabled(ctx, slog.LevelInfo); got != tt.wantInfo {
				t.Errorf("info enabled = %v, want %v", got, tt.wantInfo)
			}
			if got := h.Enabled(ctx, slog.LevelWarn); got != tt.wantWarn {
				t.Errorf("warn enabled = %v, want %v", got, tt.wantWarn)
			}
			if got := h.Enabled(ctx, slog.LevelError); got != tt.wantErrorOn {
				t.Errorf("error enabled = %v, want %v", got, tt.wantErrorOn)
			}
		})
	}
}
