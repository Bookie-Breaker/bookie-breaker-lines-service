package service

import (
	"testing"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

func ptr(f float64) *float64 { return &f }

func snap(game, book string, market model.MarketType, sel string, line *float64, odds int, live bool) model.LineSnapshot {
	return model.LineSnapshot{
		GameExternalID: game,
		SportsbookID:   book,
		MarketType:     market,
		Selection:      sel,
		LineValue:      line,
		OddsAmerican:   odds,
		IsLive:         live,
	}
}

func key(game, book string, market model.MarketType, sel string) repository.LineKey {
	return repository.LineKey{GameExternalID: game, SportsbookID: book, MarketType: market, Selection: sel}
}

func TestFilterChanged(t *testing.T) {
	base := snap("g1", "b1", model.MarketSpread, "Lakers -3.5", ptr(-3.5), -110, false)

	tests := []struct {
		name     string
		latest   map[repository.LineKey]repository.LineValues
		snapshot model.LineSnapshot
		want     bool // kept?
	}{
		{
			name:     "new key is kept",
			latest:   map[repository.LineKey]repository.LineValues{},
			snapshot: base,
			want:     true,
		},
		{
			name: "unchanged values are skipped",
			latest: map[repository.LineKey]repository.LineValues{
				key("g1", "b1", model.MarketSpread, "Lakers -3.5"): {LineValue: ptr(-3.5), OddsAmerican: -110},
			},
			snapshot: base,
			want:     false,
		},
		{
			name: "changed line value is kept",
			latest: map[repository.LineKey]repository.LineValues{
				key("g1", "b1", model.MarketSpread, "Lakers -3.5"): {LineValue: ptr(-3.0), OddsAmerican: -110},
			},
			snapshot: base,
			want:     true,
		},
		{
			name: "changed odds are kept",
			latest: map[repository.LineKey]repository.LineValues{
				key("g1", "b1", model.MarketSpread, "Lakers -3.5"): {LineValue: ptr(-3.5), OddsAmerican: -115},
			},
			snapshot: base,
			want:     true,
		},
		{
			name: "changed live flag is kept",
			latest: map[repository.LineKey]repository.LineValues{
				key("g1", "b1", model.MarketSpread, "Lakers -3.5"): {LineValue: ptr(-3.5), OddsAmerican: -110, IsLive: true},
			},
			snapshot: base,
			want:     true,
		},
		{
			name: "nil vs non-nil line value is kept",
			latest: map[repository.LineKey]repository.LineValues{
				key("g1", "b1", model.MarketSpread, "Lakers -3.5"): {LineValue: nil, OddsAmerican: -110},
			},
			snapshot: base,
			want:     true,
		},
		{
			name: "both nil line values with same odds are skipped",
			latest: map[repository.LineKey]repository.LineValues{
				key("g1", "b1", model.MarketMoneyline, "Lakers"): {LineValue: nil, OddsAmerican: 150},
			},
			snapshot: snap("g1", "b1", model.MarketMoneyline, "Lakers", nil, 150, false),
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterChanged(tt.latest, []model.LineSnapshot{tt.snapshot})
			kept := len(got) == 1
			if kept != tt.want {
				t.Errorf("FilterChanged kept=%v, want %v", kept, tt.want)
			}
		})
	}
}

func TestFilterChangedMixedBatch(t *testing.T) {
	latest := map[repository.LineKey]repository.LineValues{
		key("g1", "b1", model.MarketSpread, "Lakers -3.5"): {LineValue: ptr(-3.5), OddsAmerican: -110},
		key("g1", "b1", model.MarketTotal, "Over 220.5"):   {LineValue: ptr(220.5), OddsAmerican: -110},
	}

	batch := []model.LineSnapshot{
		snap("g1", "b1", model.MarketSpread, "Lakers -3.5", ptr(-3.5), -110, false), // unchanged
		snap("g1", "b1", model.MarketTotal, "Over 220.5", ptr(221.0), -110, false),  // line moved
		snap("g2", "b1", model.MarketSpread, "Celtics -1", ptr(-1.0), -105, false),  // new game
	}

	got := FilterChanged(latest, batch)
	if len(got) != 2 {
		t.Fatalf("expected 2 changed snapshots, got %d", len(got))
	}
	if got[0].Selection != "Over 220.5" || got[1].GameExternalID != "g2" {
		t.Errorf("unexpected changed set: %+v", got)
	}
}
