package oddsapi_test

import (
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func TestDecimalToAmerican(t *testing.T) {
	tests := []struct {
		decimal  float64
		expected int
	}{
		{1.909, -110},
		{2.0, 100},
		{2.5, 150},
		{1.5, -200},
		{3.0, 200},
		{1.333, -300},
	}

	for _, tc := range tests {
		got := oddsapi.DecimalToAmerican(tc.decimal)
		if got != tc.expected {
			t.Errorf("DecimalToAmerican(%v) = %d, want %d", tc.decimal, got, tc.expected)
		}
	}
}

func TestAmericanToDecimal(t *testing.T) {
	tests := []struct {
		american int
		expected float64
	}{
		{-110, 1.909090909},
		{100, 2.0},
		{150, 2.5},
		{-200, 1.5},
	}

	for _, tc := range tests {
		got := oddsapi.AmericanToDecimal(tc.american)
		diff := got - tc.expected
		if diff > 0.01 || diff < -0.01 {
			t.Errorf("AmericanToDecimal(%d) = %f, want ~%f", tc.american, got, tc.expected)
		}
	}
}

func TestImpliedProbability(t *testing.T) {
	tests := []struct {
		decimal  float64
		expected float64
	}{
		{2.0, 0.5},
		{1.909, 0.5238},
		{3.0, 0.3333},
	}

	for _, tc := range tests {
		got := oddsapi.ImpliedProbability(tc.decimal)
		diff := got - tc.expected
		if diff > 0.001 || diff < -0.001 {
			t.Errorf("ImpliedProbability(%v) = %f, want ~%f", tc.decimal, got, tc.expected)
		}
	}
}

func TestNormalize(t *testing.T) {
	pt := func(f float64) *float64 { return &f }

	events := oddsapi.OddsResponse{
		{
			ID:           "game123",
			SportKey:     "basketball_nba",
			HomeTeam:     "Los Angeles Lakers",
			AwayTeam:     "Boston Celtics",
			CommenceTime: time.Now().Add(24 * time.Hour),
			Bookmakers: []oddsapi.Bookmaker{
				{
					Key:   "draftkings",
					Title: "DraftKings",
					Markets: []oddsapi.Market{
						{
							Key: "spreads",
							Outcomes: []oddsapi.Outcome{
								{Name: "Los Angeles Lakers", Price: 1.909, Point: pt(-3.5)},
								{Name: "Boston Celtics", Price: 1.909, Point: pt(3.5)},
							},
						},
						{
							Key: "h2h",
							Outcomes: []oddsapi.Outcome{
								{Name: "Los Angeles Lakers", Price: 1.606},
								{Name: "Boston Celtics", Price: 2.400},
							},
						},
						{
							Key: "totals",
							Outcomes: []oddsapi.Outcome{
								{Name: "Over", Price: 1.909, Point: pt(220.5)},
								{Name: "Under", Price: 1.909, Point: pt(220.5)},
							},
						},
					},
				},
			},
		},
	}

	sbMap := map[string]string{
		"draftkings": "dk-uuid-123",
	}

	result := oddsapi.Normalize(events, sbMap, time.Now())

	if result.GameCount != 1 {
		t.Errorf("expected 1 game, got %d", result.GameCount)
	}

	if len(result.Snapshots) != 6 {
		t.Fatalf("expected 6 snapshots, got %d", len(result.Snapshots))
	}

	// Verify spread selection format
	spread := result.Snapshots[0]
	if spread.Selection != "Los Angeles Lakers -3.5" {
		t.Errorf("expected selection 'Los Angeles Lakers -3.5', got %q", spread.Selection)
	}
	if spread.MarketType != model.MarketSpread {
		t.Errorf("expected market type SPREAD, got %q", spread.MarketType)
	}
	if spread.OddsAmerican != -110 {
		t.Errorf("expected odds -110, got %d", spread.OddsAmerican)
	}

	// Verify moneyline
	ml := result.Snapshots[2]
	if ml.MarketType != model.MarketMoneyline {
		t.Errorf("expected MONEYLINE, got %q", ml.MarketType)
	}
	if ml.LineValue != nil {
		t.Error("moneyline should have nil line_value")
	}

	// Verify total selection format
	over := result.Snapshots[4]
	if over.Selection != "Over 220.5" {
		t.Errorf("expected selection 'Over 220.5', got %q", over.Selection)
	}

	// All should reference the correct game and sportsbook
	for _, snap := range result.Snapshots {
		if snap.GameExternalID != "game123" {
			t.Errorf("expected game_id 'game123', got %q", snap.GameExternalID)
		}
		if snap.SportsbookID != "dk-uuid-123" {
			t.Errorf("expected sportsbook_id 'dk-uuid-123', got %q", snap.SportsbookID)
		}
		if snap.League != model.LeagueNBA {
			t.Errorf("expected league NBA, got %q", snap.League)
		}
		if snap.Source != "the_odds_api" {
			t.Errorf("expected source 'the_odds_api', got %q", snap.Source)
		}
	}
}

func TestNormalize_UnknownSportKey(t *testing.T) {
	events := oddsapi.OddsResponse{
		{
			ID:       "game123",
			SportKey: "unknown_sport",
		},
	}

	result := oddsapi.Normalize(events, map[string]string{}, time.Now())

	if len(result.Snapshots) != 0 {
		t.Errorf("expected 0 snapshots for unknown sport, got %d", len(result.Snapshots))
	}
}

func TestNormalize_UnknownSportsbook(t *testing.T) {
	events := oddsapi.OddsResponse{
		{
			ID:       "game123",
			SportKey: "basketball_nba",
			Bookmakers: []oddsapi.Bookmaker{
				{
					Key: "unknown_book",
					Markets: []oddsapi.Market{
						{
							Key: "h2h",
							Outcomes: []oddsapi.Outcome{
								{Name: "Team A", Price: 2.0},
							},
						},
					},
				},
			},
		},
	}

	result := oddsapi.Normalize(events, map[string]string{}, time.Now())

	if len(result.Snapshots) != 0 {
		t.Errorf("expected 0 snapshots for unknown sportsbook, got %d", len(result.Snapshots))
	}
}
