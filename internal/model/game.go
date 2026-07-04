package model

import "time"

// Game holds ingestion metadata for a game observed in odds feeds.
// It is not the canonical game entity (owned by statistics-service);
// it exists so lines-service can derive sides, filter by date, and
// know when to capture closing lines.
type Game struct {
	GameExternalID    string     `json:"game_id"`
	League            League     `json:"league"`
	HomeTeam          string     `json:"home_team"`
	AwayTeam          string     `json:"away_team"`
	CommenceTime      time.Time  `json:"commence_time"`
	ClosingCapturedAt *time.Time `json:"closing_captured_at,omitempty"`
	FirstSeenAt       time.Time  `json:"first_seen_at,omitzero"`
	UpdatedAt         time.Time  `json:"updated_at,omitzero"`
}
