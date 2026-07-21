package oddsapi

import (
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func fptr(f float64) *float64 { return &f }

func TestImpliedProbabilityNonPositive(t *testing.T) {
	if got := ImpliedProbability(0); got != 0 {
		t.Errorf("ImpliedProbability(0) = %v, want 0", got)
	}
	if got := ImpliedProbability(-1.5); got != 0 {
		t.Errorf("ImpliedProbability(-1.5) = %v, want 0", got)
	}
}

func TestFormatFloat(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{2.5, "2.5"},
		{3, "3"},
		{-3.5, "-3.5"},
		{-7, "-7"},
		{0, "0"},
	}
	for _, tt := range tests {
		if got := formatFloat(tt.in); got != tt.want {
			t.Errorf("formatFloat(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildSelectionBranches(t *testing.T) {
	tests := []struct {
		name       string
		selection  string
		point      *float64
		marketType model.MarketType
		want       string
	}{
		{"spread positive point gets plus sign", "Celtics", fptr(3.5), model.MarketSpread, "Celtics +3.5"},
		{"spread negative point keeps sign", "Lakers", fptr(-3.5), model.MarketSpread, "Lakers -3.5"},
		{"spread without point is name only", "Lakers", nil, model.MarketSpread, "Lakers"},
		{"total over carries point", "Over", fptr(220.5), model.MarketTotal, "Over 220.5"},
		{"total under carries point", "Under", fptr(220), model.MarketTotal, "Under 220"},
		{"total with unexpected name ignores point", "Draw", fptr(2.5), model.MarketTotal, "Draw"},
		{"total without point is name only", "Over", nil, model.MarketTotal, "Over"},
		{"moneyline is plain name", "Lakers", nil, model.MarketMoneyline, "Lakers"},
		{"unknown market falls back to name", "Something", fptr(1), model.MarketFuture, "Something"},
	}
	for _, tt := range tests {
		if got := BuildSelection(tt.selection, tt.point, tt.marketType); got != tt.want {
			t.Errorf("%s: BuildSelection = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNormalizeSkipsUnknownMarketKey(t *testing.T) {
	events := OddsResponse{{
		ID:       "ev1",
		SportKey: "basketball_nba",
		HomeTeam: "Lakers",
		AwayTeam: "Celtics",
		Bookmakers: []Bookmaker{{
			Key: "draftkings",
			Markets: []Market{
				{Key: "outrights", Outcomes: []Outcome{{Name: "Lakers", Price: 5.0}}},
				{Key: "h2h", Outcomes: []Outcome{{Name: "Lakers", Price: 1.91}}},
			},
		}},
	}}

	result := Normalize(events, map[string]string{"draftkings": "sb-1"}, time.Now().UTC())
	if len(result.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1 (unknown market key skipped)", len(result.Snapshots))
	}
	if result.Snapshots[0].MarketType != model.MarketMoneyline {
		t.Errorf("market type = %s, want MONEYLINE", result.Snapshots[0].MarketType)
	}
	if result.GameCount != 1 || len(result.Games) != 1 {
		t.Errorf("games = %d/%d, want 1/1", result.GameCount, len(result.Games))
	}
}

func TestHumanizeMarketKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"player_first_td", "First Td"},
		{"batter_home_runs", "Home Runs"},
		{"pitcher_strikeouts", "Strikeouts"},
		{"team_total_corners", "Total Corners"},
		{"weird_key", "Weird Key"},
		{"player__double", " Double"}, // empty word segments survive the join
	}
	for _, tt := range tests {
		if got := humanizeMarketKey(tt.in); got != tt.want {
			t.Errorf("humanizeMarketKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildPropSelectionFallbacks(t *testing.T) {
	// Yes/No market without a curated label falls back to the humanized key.
	got := buildPropSelection("player_anytime_assist", PropTypeYesNo, Outcome{
		Name:        "Yes",
		Description: " Kevin De Bruyne ",
	})
	if got != "Kevin De Bruyne Anytime Assist Yes" {
		t.Errorf("selection = %q, want humanized fallback label", got)
	}

	// Over/Under outcome without a point omits the point suffix.
	got = buildPropSelection("player_shots", PropTypeOverUnder, Outcome{
		Name:        "Over",
		Description: "Bukayo Saka",
	})
	if got != "Bukayo Saka Over" {
		t.Errorf("selection = %q, want name without point", got)
	}
}
