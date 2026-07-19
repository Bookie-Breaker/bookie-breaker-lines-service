// Package sharpapi consumes the SharpAPI server-sent-events stream of live
// line updates (ADR-007, amended). Built against the infra-ops sharp-stub;
// real credentials are deferred.
package sharpapi

import "time"

// Frame is one live line update pushed on the SSE stream. Each frame carries
// a single market at a single bookmaker for a single event.
type Frame struct {
	EventID      string    `json:"event_id"`
	SportKey     string    `json:"sport_key"`
	CommenceTime time.Time `json:"commence_time"`
	HomeTeam     string    `json:"home_team"`
	AwayTeam     string    `json:"away_team"`
	Bookmaker    Bookmaker `json:"bookmaker"`
	Market       Market    `json:"market"`
	CapturedAt   time.Time `json:"captured_at"`
}

// Bookmaker identifies the sportsbook a frame's prices belong to.
type Bookmaker struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Market is a single market (h2h, spreads, totals) with its outcomes.
type Market struct {
	Key      string    `json:"key"`
	Outcomes []Outcome `json:"outcomes"`
}

// Outcome is one selection within a market. Price is decimal odds; Point is
// present for spreads and totals.
type Outcome struct {
	Name  string   `json:"name"`
	Price float64  `json:"price"`
	Point *float64 `json:"point,omitempty"`
}
