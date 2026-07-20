package oddsapi_test

import (
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

func fptr(f float64) *float64 { return &f }

func TestClassifyMarketKey(t *testing.T) {
	tests := []struct {
		key    string
		want   model.MarketType
		wantOK bool
	}{
		// Exact lookups win first.
		{"h2h", model.MarketMoneyline, true},
		{"spreads", model.MarketSpread, true},
		{"totals", model.MarketTotal, true},
		// player_* prefix.
		{"player_points", model.MarketPlayerProp, true},
		{"player_goal_scorer_anytime", model.MarketPlayerProp, true},
		{"player_anytime_td", model.MarketPlayerProp, true},
		// The Odds API baseball prop keys use batter_/pitcher_ prefixes.
		{"batter_hits", model.MarketPlayerProp, true},
		{"batter_home_runs", model.MarketPlayerProp, true},
		{"pitcher_strikeouts", model.MarketPlayerProp, true},
		// team_* prefix.
		{"team_totals", model.MarketTeamProp, true},
		// Unrecognized keys are rejected.
		{"alternate_spreads", "", false},
		{"outrights", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		got, ok := oddsapi.ClassifyMarketKey(tt.key)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("ClassifyMarketKey(%q) = (%q, %v), want (%q, %v)", tt.key, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestPropTypeForMarket(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"player_goal_scorer_anytime", oddsapi.PropTypeYesNo},
		{"player_anytime_td", oddsapi.PropTypeYesNo},
		{"player_shots", oddsapi.PropTypeOverUnder},
		{"batter_hits", oddsapi.PropTypeOverUnder},
		{"pitcher_strikeouts", oddsapi.PropTypeOverUnder},
	}
	for _, tt := range tests {
		if got := oddsapi.PropTypeForMarket(tt.key); got != tt.want {
			t.Errorf("PropTypeForMarket(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestSlugifyPlayerName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Kylian Mbappé", "kylian-mbappe"},
		{"Erling Haaland", "erling-haaland"},
		{"Jesús Luzardo", "jesus-luzardo"},
		{"N'Golo Kanté", "n-golo-kante"},
		{"João Félix", "joao-felix"},
		{"Bukayo Saka", "bukayo-saka"},
		{"  Trimmed  Name  ", "trimmed-name"},
		{"O'Neill Jr.", "o-neill-jr"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := oddsapi.SlugifyPlayerName(tt.name); got != tt.want {
			t.Errorf("SlugifyPlayerName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func propEvent(sportKey string, markets []oddsapi.Market) oddsapi.Event {
	return oddsapi.Event{
		ID:           "evt-1",
		SportKey:     sportKey,
		CommenceTime: time.Date(2026, 7, 20, 18, 0, 0, 0, time.UTC),
		HomeTeam:     "Home FC",
		AwayTeam:     "Away FC",
		Bookmakers: []oddsapi.Bookmaker{
			{Key: "draftkings", Title: "DraftKings", Markets: markets},
		},
	}
}

func TestNormalizeEventPropsGoalscorerYesNo(t *testing.T) {
	event := propEvent("soccer_fifa_world_cup", []oddsapi.Market{{
		Key: "player_goal_scorer_anytime",
		Outcomes: []oddsapi.Outcome{
			{Name: "Yes", Description: "Kylian Mbappé", Price: 2.50},
			{Name: "No", Description: "Kylian Mbappé", Price: 1.55},
		},
	}})

	captured := time.Now().UTC()
	snaps := oddsapi.NormalizeEventProps(event, map[string]string{"draftkings": "sb-1"}, captured)
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots (Yes/No pair), got %d", len(snaps))
	}

	yes, no := snaps[0], snaps[1]
	if yes.Selection != "Kylian Mbappé Anytime Goalscorer Yes" {
		t.Errorf("Yes selection = %q, want %q", yes.Selection, "Kylian Mbappé Anytime Goalscorer Yes")
	}
	if no.Selection != "Kylian Mbappé Anytime Goalscorer No" {
		t.Errorf("No selection = %q, want %q", no.Selection, "Kylian Mbappé Anytime Goalscorer No")
	}

	for _, snap := range snaps {
		if snap.MarketType != model.MarketPlayerProp {
			t.Errorf("market type = %q, want PLAYER_PROP", snap.MarketType)
		}
		if snap.StatType != "player_goal_scorer_anytime" {
			t.Errorf("stat type = %q, want raw market key (ADR-029)", snap.StatType)
		}
		if snap.PropType != oddsapi.PropTypeYesNo {
			t.Errorf("prop type = %q, want YES_NO", snap.PropType)
		}
		if snap.PlayerExternalID != "kylian-mbappe" {
			t.Errorf("player slug = %q, want kylian-mbappe", snap.PlayerExternalID)
		}
		if snap.LineValue != nil {
			t.Errorf("line value = %v, want nil for YES_NO", *snap.LineValue)
		}
		if snap.League != model.LeagueFIFAWC {
			t.Errorf("league = %q, want FIFA_WC", snap.League)
		}
		if snap.IsLive {
			t.Error("prop snapshots must not be live")
		}
		if snap.Source != "the_odds_api" {
			t.Errorf("source = %q, want the_odds_api", snap.Source)
		}
		if !snap.CapturedAt.Equal(captured) {
			t.Errorf("captured at = %v, want %v", snap.CapturedAt, captured)
		}
	}

	if yes.OddsAmerican != 150 {
		t.Errorf("Yes odds american = %d, want 150", yes.OddsAmerican)
	}
	if no.OddsDecimal != 1.55 {
		t.Errorf("No odds decimal = %v, want 1.55", no.OddsDecimal)
	}
}

func TestNormalizeEventPropsShotsOverUnder(t *testing.T) {
	event := propEvent("soccer_epl", []oddsapi.Market{{
		Key: "player_shots",
		Outcomes: []oddsapi.Outcome{
			{Name: "Over", Description: "Erling Haaland", Price: 1.87, Point: fptr(2.5)},
			{Name: "Under", Description: "Erling Haaland", Price: 1.95, Point: fptr(2.5)},
		},
	}})

	snaps := oddsapi.NormalizeEventProps(event, map[string]string{"draftkings": "sb-1"}, time.Now().UTC())
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}

	over, under := snaps[0], snaps[1]
	if over.Selection != "Erling Haaland Over 2.5" {
		t.Errorf("Over selection = %q, want %q", over.Selection, "Erling Haaland Over 2.5")
	}
	if under.Selection != "Erling Haaland Under 2.5" {
		t.Errorf("Under selection = %q, want %q", under.Selection, "Erling Haaland Under 2.5")
	}
	for _, snap := range snaps {
		if snap.StatType != "player_shots" {
			t.Errorf("stat type = %q, want player_shots", snap.StatType)
		}
		if snap.PropType != oddsapi.PropTypeOverUnder {
			t.Errorf("prop type = %q, want OVER_UNDER", snap.PropType)
		}
		if snap.PlayerExternalID != "erling-haaland" {
			t.Errorf("player slug = %q, want erling-haaland", snap.PlayerExternalID)
		}
		if snap.LineValue == nil || *snap.LineValue != 2.5 {
			t.Errorf("line value = %v, want 2.5", snap.LineValue)
		}
		if snap.League != model.LeagueEPL {
			t.Errorf("league = %q, want EPL", snap.League)
		}
	}
}

func TestNormalizeEventPropsMLBBatterHits(t *testing.T) {
	event := propEvent("baseball_mlb", []oddsapi.Market{{
		Key: "batter_hits",
		Outcomes: []oddsapi.Outcome{
			{Name: "Over", Description: "Juan Soto", Price: 2.10, Point: fptr(1.5)},
			{Name: "Under", Description: "Juan Soto", Price: 1.74, Point: fptr(1.5)},
		},
	}})

	snaps := oddsapi.NormalizeEventProps(event, map[string]string{"draftkings": "sb-1"}, time.Now().UTC())
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].Selection != "Juan Soto Over 1.5" {
		t.Errorf("selection = %q, want %q", snaps[0].Selection, "Juan Soto Over 1.5")
	}
	if snaps[0].MarketType != model.MarketPlayerProp {
		t.Errorf("market type = %q, want PLAYER_PROP for batter_ prefix", snaps[0].MarketType)
	}
	if snaps[0].StatType != "batter_hits" {
		t.Errorf("stat type = %q, want batter_hits", snaps[0].StatType)
	}
	if snaps[0].PlayerExternalID != "juan-soto" {
		t.Errorf("player slug = %q, want juan-soto", snaps[0].PlayerExternalID)
	}
	if snaps[0].League != model.LeagueMLB {
		t.Errorf("league = %q, want MLB", snaps[0].League)
	}
}

func TestNormalizeEventPropsSkips(t *testing.T) {
	event := propEvent("soccer_epl", []oddsapi.Market{
		// Non-prop market keys are the bulk path's job; ignored here even if
		// the API returns them.
		{Key: "h2h", Outcomes: []oddsapi.Outcome{{Name: "Home FC", Price: 1.9}}},
		// Unknown market keys are skipped.
		{Key: "alternate_totals", Outcomes: []oddsapi.Outcome{{Name: "Over", Price: 1.9, Point: fptr(3.5)}}},
		// Outcomes without a player description are unusable.
		{Key: "player_shots", Outcomes: []oddsapi.Outcome{{Name: "Over", Price: 1.9, Point: fptr(2.5)}}},
	})

	if snaps := oddsapi.NormalizeEventProps(event, map[string]string{"draftkings": "sb-1"}, time.Now().UTC()); len(snaps) != 0 {
		t.Errorf("expected 0 snapshots, got %d: %+v", len(snaps), snaps)
	}

	// Unknown sportsbooks and unknown sport keys yield nothing.
	valid := propEvent("soccer_epl", []oddsapi.Market{{
		Key:      "player_shots",
		Outcomes: []oddsapi.Outcome{{Name: "Over", Description: "Erling Haaland", Price: 1.9, Point: fptr(2.5)}},
	}})
	if snaps := oddsapi.NormalizeEventProps(valid, map[string]string{"unknown-book": "sb-9"}, time.Now().UTC()); len(snaps) != 0 {
		t.Errorf("unknown sportsbook: expected 0 snapshots, got %d", len(snaps))
	}
	unknownSport := valid
	unknownSport.SportKey = "cricket_ipl"
	if snaps := oddsapi.NormalizeEventProps(unknownSport, map[string]string{"draftkings": "sb-1"}, time.Now().UTC()); len(snaps) != 0 {
		t.Errorf("unknown sport: expected 0 snapshots, got %d", len(snaps))
	}
}
