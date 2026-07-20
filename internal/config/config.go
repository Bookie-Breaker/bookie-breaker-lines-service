package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port                 int
	DatabaseURL          string
	RedisURL             string
	OddsAPIKey           string
	OddsAPIPollInterval  time.Duration
	OddsAPISports        []string
	PropSports           []string
	PropPollInterval     time.Duration
	PropCommenceWindow   time.Duration
	PropMaxEventsPerRun  int
	SharpAPIURL          string
	SharpAPIKey          string
	SharpAPIFailures     int
	OTELExporterEndpoint string
	OTELServiceName      string
	LogLevel             string
}

func Load() *Config {
	return &Config{
		Port:                 getEnvInt("PORT", 8001),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://bookiebreaker:localdev@localhost:5432/bookiebreaker?search_path=lines,public"),
		RedisURL:             getEnv("REDIS_URL", "redis://localhost:6379"),
		OddsAPIKey:           getEnv("ODDS_API_KEY", ""),
		OddsAPIPollInterval:  time.Duration(getEnvInt("ODDS_API_POLL_INTERVAL", 300)) * time.Second,
		OddsAPISports:        getEnvList("ODDS_API_SPORTS", []string{"basketball_nba"}),
		PropSports:           getEnvListEmptyOK("PROP_SPORTS", []string{"soccer_fifa_world_cup", "soccer_epl", "baseball_mlb"}),
		PropPollInterval:     time.Duration(getEnvInt("PROP_POLL_INTERVAL", 1800)) * time.Second,
		PropCommenceWindow:   time.Duration(getEnvInt("PROP_COMMENCE_WINDOW_HOURS", 48)) * time.Hour,
		PropMaxEventsPerRun:  getEnvInt("PROP_MAX_EVENTS_PER_CYCLE", 10),
		SharpAPIURL:          getEnv("SHARP_API_URL", ""),
		SharpAPIKey:          getEnv("SHARP_API_KEY", ""),
		SharpAPIFailures:     getEnvInt("SHARP_API_FAILURE_THRESHOLD", 5),
		OTELExporterEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		OTELServiceName:      getEnv("OTEL_SERVICE_NAME", "lines-service"),
		LogLevel:             getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

// getEnvListEmptyOK is getEnvList except that a variable explicitly set to
// an empty (or all-whitespace/comma) value yields an empty list instead of
// the fallback — setting PROP_SPORTS="" disables prop ingestion entirely.
func getEnvListEmptyOK(key string, fallback []string) []string {
	v, set := os.LookupEnv(key)
	if !set {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func getEnvList(key string, fallback []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}
