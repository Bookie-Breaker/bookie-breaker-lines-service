package model

import (
	"encoding/json"
	"testing"
	"time"
)

// TestLineSnapshotPropFieldsJSON pins the ADR-029 prop field contract:
// player_id, stat_type, and prop_type serialize when set and are omitted
// entirely for non-prop lines.
func TestLineSnapshotPropFieldsJSON(t *testing.T) {
	line := 2.5
	snap := LineSnapshot{
		ID:               "snap-1",
		GameExternalID:   "game-1",
		SportsbookID:     "book-1",
		MarketType:       MarketPlayerProp,
		Selection:        "Erling Haaland Over 2.5 Shots",
		PlayerExternalID: "player-9",
		StatType:         "SHOTS",
		PropType:         "OVER_UNDER",
		LineValue:        &line,
		OddsAmerican:     -110,
		OddsDecimal:      1.91,
		CapturedAt:       time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}

	got := roundTrip(t, snap)
	if got["player_id"] != "player-9" {
		t.Errorf("player_id = %v, want player-9", got["player_id"])
	}
	if got["stat_type"] != "SHOTS" {
		t.Errorf("stat_type = %v, want SHOTS", got["stat_type"])
	}
	if got["prop_type"] != "OVER_UNDER" {
		t.Errorf("prop_type = %v, want OVER_UNDER", got["prop_type"])
	}
}

func TestLineSnapshotPropFieldsOmittedWhenEmpty(t *testing.T) {
	snap := LineSnapshot{
		ID:             "snap-2",
		GameExternalID: "game-1",
		SportsbookID:   "book-1",
		MarketType:     MarketSpread,
		Selection:      "Los Angeles Lakers -3.5",
		OddsAmerican:   -110,
		OddsDecimal:    1.91,
	}

	got := roundTrip(t, snap)
	for _, key := range []string{"player_id", "stat_type", "prop_type"} {
		if _, present := got[key]; present {
			t.Errorf("%s should be omitted for non-prop lines", key)
		}
	}
}

func TestClosingLineAndBestLinePropFieldsJSON(t *testing.T) {
	cl := ClosingLine{
		ID:               "cl-1",
		GameExternalID:   "game-1",
		SportsbookID:     "book-1",
		MarketType:       MarketPlayerProp,
		Selection:        "Erling Haaland Anytime Goalscorer Yes",
		PlayerExternalID: "player-9",
		StatType:         "GOALS",
		PropType:         "YES_NO",
	}
	got := roundTrip(t, cl)
	if got["player_id"] != "player-9" || got["stat_type"] != "GOALS" || got["prop_type"] != "YES_NO" {
		t.Errorf("closing line prop fields = %v/%v/%v, want player-9/GOALS/YES_NO",
			got["player_id"], got["stat_type"], got["prop_type"])
	}

	got = roundTrip(t, ClosingLine{ID: "cl-2", MarketType: MarketTotal, Selection: "Over 220.5"})
	for _, key := range []string{"player_id", "stat_type", "prop_type"} {
		if _, present := got[key]; present {
			t.Errorf("closing line %s should be omitted when empty", key)
		}
	}

	best := BestLine{
		MarketType:       MarketPlayerProp,
		Selection:        "Erling Haaland Over 2.5 Shots",
		Side:             "OVER",
		PlayerExternalID: "player-9",
		StatType:         "SHOTS",
		PropType:         "OVER_UNDER",
	}
	got = roundTrip(t, best)
	if got["player_id"] != "player-9" || got["stat_type"] != "SHOTS" || got["prop_type"] != "OVER_UNDER" {
		t.Errorf("best line prop fields = %v/%v/%v, want player-9/SHOTS/OVER_UNDER",
			got["player_id"], got["stat_type"], got["prop_type"])
	}
}

func roundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}
