package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port                 int
	DatabaseURL          string
	RedisURL             string
	OddsAPIKey           string
	OddsAPIPollInterval  time.Duration
	OTELExporterEndpoint string
	OTELServiceName      string
	LogLevel             string
}

func Load() *Config {
	return &Config{
		Port:                 getEnvInt("PORT", 8001),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://bookiebreaker:localdev@localhost:5432/bookiebreaker?search_path=lines"),
		RedisURL:             getEnv("REDIS_URL", "redis://localhost:6379"),
		OddsAPIKey:           getEnv("ODDS_API_KEY", ""),
		OddsAPIPollInterval:  time.Duration(getEnvInt("ODDS_API_POLL_INTERVAL", 300)) * time.Second,
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
