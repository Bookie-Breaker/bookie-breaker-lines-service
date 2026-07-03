package oddsapi

import "time"

// OddsResponse is the top-level response from GET /v4/sports/{sport}/odds.
type OddsResponse []Event

// Event represents a single game/event from The Odds API.
type Event struct {
	ID           string      `json:"id"`
	SportKey     string      `json:"sport_key"`
	SportTitle   string      `json:"sport_title"`
	CommenceTime time.Time   `json:"commence_time"`
	HomeTeam     string      `json:"home_team"`
	AwayTeam     string      `json:"away_team"`
	Bookmakers   []Bookmaker `json:"bookmakers"`
}

// Bookmaker represents odds from a single sportsbook.
type Bookmaker struct {
	Key        string    `json:"key"`
	Title      string    `json:"title"`
	LastUpdate time.Time `json:"last_update"`
	Markets    []Market  `json:"markets"`
}

// Market represents a single market type (h2h, spreads, totals).
type Market struct {
	Key        string    `json:"key"`
	LastUpdate time.Time `json:"last_update"`
	Outcomes   []Outcome `json:"outcomes"`
}

// Outcome represents a single selection within a market.
type Outcome struct {
	Name  string   `json:"name"`
	Price float64  `json:"price"` // decimal odds
	Point *float64 `json:"point,omitempty"`
}

// SportResponse is a single sport from GET /v4/sports.
type SportResponse struct {
	Key          string `json:"key"`
	Group        string `json:"group"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Active       bool   `json:"active"`
	HasOutrights bool   `json:"has_outrights"`
}
