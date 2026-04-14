package model

import (
	"time"
)

type LineSnapshot struct {
	ID             string     `json:"id"`
	GameExternalID string     `json:"game_id"`
	SportsbookID   string     `json:"sportsbook_id"`
	SportsbookKey  string     `json:"sportsbook_key,omitempty"`
	League         League     `json:"league,omitempty"`
	MarketType     MarketType `json:"market_type"`
	Selection      string     `json:"selection"`
	Side           string     `json:"side,omitempty"`
	LineValue      *float64   `json:"line_value"`
	OddsAmerican   int        `json:"odds_american"`
	OddsDecimal    float64    `json:"odds_decimal"`
	ImpliedProb    float64    `json:"implied_probability,omitempty"`
	IsLive         bool       `json:"is_live,omitempty"`
	IsOpening      bool       `json:"is_opening,omitempty"`
	IsClosing      bool       `json:"is_closing,omitempty"`
	CapturedAt     time.Time  `json:"timestamp"`
	Source         string     `json:"source,omitempty"`
}

type ClosingLine struct {
	ID             string     `json:"id"`
	GameExternalID string     `json:"game_id"`
	SportsbookID   string     `json:"sportsbook_id"`
	SportsbookKey  string     `json:"sportsbook_key,omitempty"`
	League         League     `json:"league,omitempty"`
	MarketType     MarketType `json:"market_type"`
	Selection      string     `json:"selection"`
	LineValue      *float64   `json:"line_value"`
	OddsAmerican   int        `json:"odds_american"`
	OddsDecimal    float64    `json:"odds_decimal"`
	CapturedAt     time.Time  `json:"captured_at"`
	CreatedAt      time.Time  `json:"created_at,omitzero"`
}

type BestLine struct {
	MarketType       MarketType `json:"market_type"`
	Selection        string     `json:"selection"`
	Side             string     `json:"side,omitempty"`
	LineValue        *float64   `json:"line_value"`
	BestOddsAmerican int        `json:"best_odds_american"`
	BestOddsDecimal  float64    `json:"best_odds_decimal"`
	ImpliedProb      float64    `json:"implied_probability"`
	SportsbookID     string     `json:"sportsbook_id"`
	SportsbookKey    string     `json:"sportsbook_key"`
	SportsbookName   string     `json:"sportsbook_name"`
	Timestamp        time.Time  `json:"timestamp"`
	LineID           string     `json:"line_id"`
}

type LineMovement struct {
	GameID            string             `json:"game_id"`
	SportsbookID      string             `json:"sportsbook_id"`
	SportsbookKey     string             `json:"sportsbook_key"`
	MarketType        MarketType         `json:"market_type"`
	Selection         string             `json:"selection"`
	OpeningLine       *float64           `json:"opening_line"`
	OpeningOdds       *int               `json:"opening_odds"`
	CurrentLine       *float64           `json:"current_line"`
	CurrentOdds       int                `json:"current_odds"`
	ClosingLine       *float64           `json:"closing_line"`
	ClosingOdds       *int               `json:"closing_odds"`
	TotalMovement     *float64           `json:"total_movement"`
	IsReverseMovement bool               `json:"is_reverse_movement"`
	Snapshots         []MovementSnapshot `json:"line_snapshots"`
}

type MovementSnapshot struct {
	LineValue    *float64  `json:"line_value"`
	OddsAmerican int       `json:"odds_american"`
	Timestamp    time.Time `json:"timestamp"`
	IsOpening    bool      `json:"is_opening"`
}

type RawAPIResponse struct {
	Service      string    `json:"service"`
	Source       string    `json:"source"`
	Endpoint     string    `json:"endpoint"`
	HTTPStatus   int       `json:"http_status"`
	RequestBody  *string   `json:"request_body,omitempty"`
	ResponseBody string    `json:"response_body"`
	CapturedAt   time.Time `json:"captured_at"`
}
