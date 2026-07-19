package sharpapi_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/sharpapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func fptr(f float64) *float64 { return &f }

var testBooks = map[string]string{"draftkings": "sb-uuid-dk"}

func baseFrame() sharpapi.Frame {
	return sharpapi.Frame{
		EventID:      "wc-semi-1",
		SportKey:     "soccer_fifa_world_cup",
		CommenceTime: time.Date(2026, 7, 10, 19, 0, 0, 0, time.UTC),
		HomeTeam:     "Argentina",
		AwayTeam:     "France",
		Bookmaker:    sharpapi.Bookmaker{Key: "draftkings", Title: "DraftKings"},
		CapturedAt:   time.Date(2026, 7, 10, 19, 41, 2, 0, time.UTC),
	}
}

func TestNormalizeThreeWayH2H(t *testing.T) {
	frame := baseFrame()
	frame.Market = sharpapi.Market{Key: "h2h", Outcomes: []sharpapi.Outcome{
		{Name: "Argentina", Price: 2.10},
		{Name: "Draw", Price: 3.30},
		{Name: "France", Price: 3.60},
	}}

	snaps, game, err := sharpapi.Normalize(frame, testBooks)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("snapshots = %d, want 3", len(snaps))
	}

	if game == nil || game.GameExternalID != "wc-semi-1" || game.League != model.LeagueFIFAWC {
		t.Fatalf("unexpected game: %+v", game)
	}
	if game.HomeTeam != "Argentina" || game.AwayTeam != "France" {
		t.Errorf("teams = %q/%q", game.HomeTeam, game.AwayTeam)
	}

	wantSelections := []string{"Argentina", "Draw", "France"}
	wantAmerican := []int{110, 230, 260} // 2.10, 3.30, 3.60 decimal
	for i, s := range snaps {
		if !s.IsLive {
			t.Errorf("snapshot %d IsLive = false, want true", i)
		}
		if s.Source != "sharpapi" {
			t.Errorf("snapshot %d Source = %q, want sharpapi", i, s.Source)
		}
		if s.MarketType != model.MarketMoneyline {
			t.Errorf("snapshot %d market = %v, want MONEYLINE", i, s.MarketType)
		}
		if s.Selection != wantSelections[i] {
			t.Errorf("snapshot %d selection = %q, want %q", i, s.Selection, wantSelections[i])
		}
		if s.OddsAmerican != wantAmerican[i] {
			t.Errorf("snapshot %d odds_american = %d, want %d", i, s.OddsAmerican, wantAmerican[i])
		}
		if s.SportsbookID != "sb-uuid-dk" {
			t.Errorf("snapshot %d sportsbook = %q", i, s.SportsbookID)
		}
		if !s.CapturedAt.Equal(frame.CapturedAt) {
			t.Errorf("snapshot %d captured_at = %v, want %v", i, s.CapturedAt, frame.CapturedAt)
		}
		if s.LineValue != nil {
			t.Errorf("snapshot %d line_value = %v, want nil for h2h", i, *s.LineValue)
		}
	}
}

func TestNormalizeSpreadsCarryPoints(t *testing.T) {
	frame := baseFrame()
	frame.SportKey = "basketball_nba"
	frame.EventID = "nba-1"
	frame.HomeTeam = "Los Angeles Lakers"
	frame.AwayTeam = "Boston Celtics"
	frame.Market = sharpapi.Market{Key: "spreads", Outcomes: []sharpapi.Outcome{
		{Name: "Los Angeles Lakers", Price: 1.91, Point: fptr(-3.5)},
		{Name: "Boston Celtics", Price: 1.87, Point: fptr(3.5)},
	}}

	snaps, _, err := sharpapi.Normalize(frame, testBooks)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}

	if snaps[0].Selection != "Los Angeles Lakers -3.5" {
		t.Errorf("home selection = %q, want %q", snaps[0].Selection, "Los Angeles Lakers -3.5")
	}
	if snaps[1].Selection != "Boston Celtics +3.5" {
		t.Errorf("away selection = %q, want %q", snaps[1].Selection, "Boston Celtics +3.5")
	}
	if snaps[0].MarketType != model.MarketSpread {
		t.Errorf("market = %v, want SPREAD", snaps[0].MarketType)
	}
	if snaps[0].LineValue == nil || *snaps[0].LineValue != -3.5 {
		t.Errorf("home line_value = %v, want -3.5", snaps[0].LineValue)
	}
	if snaps[0].OddsAmerican != -110 { // 1.91 decimal
		t.Errorf("home odds_american = %d, want -110", snaps[0].OddsAmerican)
	}
	if !snaps[0].IsLive || snaps[0].Source != "sharpapi" {
		t.Errorf("live/source not set: %+v", snaps[0])
	}
}

func TestNormalizeTotalsCarryPoints(t *testing.T) {
	frame := baseFrame()
	frame.Market = sharpapi.Market{Key: "totals", Outcomes: []sharpapi.Outcome{
		{Name: "Over", Price: 2.00, Point: fptr(2.5)},
		{Name: "Under", Price: 1.80, Point: fptr(2.5)},
	}}

	snaps, _, err := sharpapi.Normalize(frame, testBooks)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}
	if snaps[0].Selection != "Over 2.5" || snaps[1].Selection != "Under 2.5" {
		t.Errorf("selections = %q, %q", snaps[0].Selection, snaps[1].Selection)
	}
	if snaps[0].OddsAmerican != 100 || snaps[1].OddsAmerican != -125 {
		t.Errorf("odds = %d, %d, want 100, -125", snaps[0].OddsAmerican, snaps[1].OddsAmerican)
	}
	if snaps[0].MarketType != model.MarketTotal {
		t.Errorf("market = %v, want TOTAL", snaps[0].MarketType)
	}
}

func TestNormalizeUnknownSportKey(t *testing.T) {
	frame := baseFrame()
	frame.SportKey = "cricket_ipl"
	frame.Market = sharpapi.Market{Key: "h2h", Outcomes: []sharpapi.Outcome{{Name: "X", Price: 2.0}}}

	if _, _, err := sharpapi.Normalize(frame, testBooks); err == nil {
		t.Fatal("expected error for unknown sport key")
	}
}

func TestNormalizeUnknownSportsbookIsSentinel(t *testing.T) {
	frame := baseFrame()
	frame.Bookmaker.Key = "brand-new-book"
	frame.Market = sharpapi.Market{Key: "h2h", Outcomes: []sharpapi.Outcome{{Name: "X", Price: 2.0}}}

	_, game, err := sharpapi.Normalize(frame, testBooks)
	if !errors.Is(err, sharpapi.ErrUnknownSportsbook) {
		t.Fatalf("err = %v, want ErrUnknownSportsbook", err)
	}
	if game == nil {
		t.Error("game metadata should still be returned")
	}
}

func TestNormalizeZeroCapturedAtDefaultsToNow(t *testing.T) {
	frame := baseFrame()
	frame.CapturedAt = time.Time{}
	frame.Market = sharpapi.Market{Key: "h2h", Outcomes: []sharpapi.Outcome{{Name: "Argentina", Price: 2.0}}}

	before := time.Now().UTC()
	snaps, _, err := sharpapi.Normalize(frame, testBooks)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if snaps[0].CapturedAt.Before(before) || snaps[0].CapturedAt.After(time.Now().UTC()) {
		t.Errorf("captured_at = %v not defaulted to now", snaps[0].CapturedAt)
	}
}
