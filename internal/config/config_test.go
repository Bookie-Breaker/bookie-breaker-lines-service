package config

import (
	"os"
	"testing"
	"time"
)

// clearConfigEnv unsets every variable Load reads so defaults apply; t.Setenv
// is called first for each key to register restoration after the test.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"PORT", "DATABASE_URL", "REDIS_URL", "ODDS_API_KEY",
		"ODDS_API_POLL_INTERVAL", "ODDS_API_SPORTS",
		"PROP_SPORTS", "PROP_POLL_INTERVAL", "PROP_COMMENCE_WINDOW_HOURS", "PROP_MAX_EVENTS_PER_CYCLE",
		"SHARP_API_URL", "SHARP_API_KEY", "SHARP_API_FAILURE_THRESHOLD",
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_SERVICE_NAME", "LOG_LEVEL",
	}
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)

	cfg := Load()
	if cfg.Port != 8001 {
		t.Errorf("Port = %d, want 8001", cfg.Port)
	}
	if cfg.RedisURL != "redis://localhost:6379" {
		t.Errorf("RedisURL = %q, want default", cfg.RedisURL)
	}
	if cfg.OddsAPIKey != "" || cfg.SharpAPIURL != "" {
		t.Error("API credentials must default to empty")
	}
	if cfg.OddsAPIPollInterval != 300*time.Second {
		t.Errorf("OddsAPIPollInterval = %v, want 300s", cfg.OddsAPIPollInterval)
	}
	if len(cfg.OddsAPISports) != 1 || cfg.OddsAPISports[0] != "basketball_nba" {
		t.Errorf("OddsAPISports = %v, want [basketball_nba]", cfg.OddsAPISports)
	}
	if len(cfg.PropSports) != 3 {
		t.Errorf("PropSports = %v, want the 3 default sports", cfg.PropSports)
	}
	if cfg.PropCommenceWindow != 48*time.Hour {
		t.Errorf("PropCommenceWindow = %v, want 48h", cfg.PropCommenceWindow)
	}
	if cfg.PropMaxEventsPerRun != 10 || cfg.SharpAPIFailures != 5 {
		t.Errorf("caps = %d/%d, want 10/5", cfg.PropMaxEventsPerRun, cfg.SharpAPIFailures)
	}
	if cfg.OTELServiceName != "lines-service" || cfg.LogLevel != "info" {
		t.Errorf("service/log = %q/%q, want lines-service/info", cfg.OTELServiceName, cfg.LogLevel)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PORT", "9001")
	t.Setenv("ODDS_API_KEY", "secret")
	t.Setenv("ODDS_API_POLL_INTERVAL", "60")
	t.Setenv("ODDS_API_SPORTS", " basketball_nba , soccer_epl ,")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := Load()
	if cfg.Port != 9001 || cfg.OddsAPIKey != "secret" || cfg.LogLevel != "debug" {
		t.Errorf("cfg = %+v, want overrides applied", cfg)
	}
	if cfg.OddsAPIPollInterval != time.Minute {
		t.Errorf("OddsAPIPollInterval = %v, want 60s", cfg.OddsAPIPollInterval)
	}
	if len(cfg.OddsAPISports) != 2 || cfg.OddsAPISports[0] != "basketball_nba" || cfg.OddsAPISports[1] != "soccer_epl" {
		t.Errorf("OddsAPISports = %v, want trimmed 2-item list", cfg.OddsAPISports)
	}
}

func TestLoadBadIntFallsBack(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PORT", "not-a-number")

	if cfg := Load(); cfg.Port != 8001 {
		t.Errorf("Port = %d, want fallback 8001 on parse failure", cfg.Port)
	}
}

func TestPropSportsEmptyDisablesProps(t *testing.T) {
	clearConfigEnv(t)
	// Explicitly empty PROP_SPORTS disables prop ingestion — it must NOT fall
	// back to the defaults.
	t.Setenv("PROP_SPORTS", "")

	if cfg := Load(); len(cfg.PropSports) != 0 {
		t.Errorf("PropSports = %v, want empty when explicitly cleared", cfg.PropSports)
	}
}

func TestPropSportsWhitespaceOnlyDisablesProps(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PROP_SPORTS", " , , ")

	if cfg := Load(); len(cfg.PropSports) != 0 {
		t.Errorf("PropSports = %v, want empty for whitespace-only value", cfg.PropSports)
	}
}

func TestGetEnvListWhitespaceFallsBack(t *testing.T) {
	clearConfigEnv(t)
	// Unlike PROP_SPORTS, a whitespace-only ODDS_API_SPORTS falls back to the
	// default list rather than disabling polling.
	t.Setenv("ODDS_API_SPORTS", " , ")

	if cfg := Load(); len(cfg.OddsAPISports) != 1 || cfg.OddsAPISports[0] != "basketball_nba" {
		t.Errorf("OddsAPISports = %v, want default fallback", cfg.OddsAPISports)
	}
}
