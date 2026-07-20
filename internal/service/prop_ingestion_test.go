package service

import (
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
)

func propStub(id string, commence time.Time) oddsapi.EventStub {
	return oddsapi.EventStub{
		ID:           id,
		SportKey:     "soccer_epl",
		CommenceTime: commence,
		HomeTeam:     "Home FC",
		AwayTeam:     "Away FC",
	}
}

func TestFilterPropEventsWindow(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	window := 48 * time.Hour

	events := []oddsapi.EventStub{
		propStub("started", now.Add(-time.Hour)),       // already started
		propStub("starting-now", now),                  // not strictly in the future
		propStub("soon", now.Add(2*time.Hour)),         // in window
		propStub("tomorrow", now.Add(30*time.Hour)),    // in window
		propStub("window-edge", now.Add(48*time.Hour)), // inclusive edge
		propStub("next-week", now.Add(120*time.Hour)),  // beyond window
	}

	selected, capped := filterPropEvents(events, now, window, 10)
	if capped != 0 {
		t.Errorf("capped = %d, want 0", capped)
	}
	want := []string{"soon", "tomorrow", "window-edge"}
	if len(selected) != len(want) {
		t.Fatalf("selected %d events, want %d: %+v", len(selected), len(want), selected)
	}
	for i, id := range want {
		if selected[i].ID != id {
			t.Errorf("selected[%d] = %q, want %q (API order preserved)", i, selected[i].ID, id)
		}
	}
}

func TestFilterPropEventsCap(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	var events []oddsapi.EventStub
	for i := 0; i < 15; i++ {
		events = append(events, propStub(string(rune('a'+i)), now.Add(time.Duration(i+1)*time.Hour)))
	}

	selected, capped := filterPropEvents(events, now, 48*time.Hour, 10)
	if len(selected) != 10 {
		t.Errorf("selected = %d events, want 10 (cap)", len(selected))
	}
	if capped != 5 {
		t.Errorf("capped = %d, want 5", capped)
	}
	// Soonest events (API order) survive the cap.
	if selected[0].ID != "a" || selected[9].ID != "j" {
		t.Errorf("cap kept %q..%q, want a..j", selected[0].ID, selected[9].ID)
	}

	// Cap larger than the pool: nothing dropped.
	selected, capped = filterPropEvents(events, now, 48*time.Hour, 20)
	if len(selected) != 15 || capped != 0 {
		t.Errorf("uncapped run = (%d, %d), want (15, 0)", len(selected), capped)
	}
}

// TestPropSelectionsDeriveSide round-trips selections built by the prop
// normalizer through the actual deriveSide used when serving lines, so the
// formats never drift apart.
func TestPropSelectionsDeriveSide(t *testing.T) {
	event := oddsapi.Event{
		ID:       "evt-derive",
		SportKey: "soccer_fifa_world_cup",
		HomeTeam: "France",
		AwayTeam: "Norway",
		Bookmakers: []oddsapi.Bookmaker{{
			Key: "draftkings",
			Markets: []oddsapi.Market{
				{
					Key: "player_goal_scorer_anytime",
					Outcomes: []oddsapi.Outcome{
						{Name: "Yes", Description: "Kylian Mbappé", Price: 2.50},
						{Name: "No", Description: "Kylian Mbappé", Price: 1.55},
					},
				},
				{
					Key: "player_shots_on_target",
					Outcomes: []oddsapi.Outcome{
						{Name: "Over", Description: "Erling Haaland", Price: 1.87, Point: floatPtr(1.5)},
						{Name: "Under", Description: "Erling Haaland", Price: 1.95, Point: floatPtr(1.5)},
					},
				},
			},
		}},
	}

	snaps := oddsapi.NormalizeEventProps(event, map[string]string{"draftkings": "sb-1"}, time.Now().UTC())
	if len(snaps) != 4 {
		t.Fatalf("expected 4 snapshots, got %d", len(snaps))
	}

	wantSides := map[string]string{
		"Kylian Mbappé Anytime Goalscorer Yes": "YES",
		"Kylian Mbappé Anytime Goalscorer No":  "NO",
		"Erling Haaland Over 1.5":              "OVER",
		"Erling Haaland Under 1.5":             "UNDER",
	}

	for _, snap := range snaps {
		want, ok := wantSides[snap.Selection]
		if !ok {
			t.Errorf("unexpected selection %q", snap.Selection)
			continue
		}
		// Props derive side from the selection text alone; the game is not
		// consulted (and player surnames must never match team names).
		if got := deriveSide(snap, nil); got != want {
			t.Errorf("deriveSide(%q) = %q, want %q", snap.Selection, got, want)
		}
	}
}

func floatPtr(f float64) *float64 { return &f }
