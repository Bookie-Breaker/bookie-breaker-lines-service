package sharpapi

import (
	"errors"
	"fmt"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

// Source is the snapshot/event source identifier for SharpAPI live lines.
const Source = "sharpapi"

// ErrUnknownSportsbook indicates the frame's bookmaker key is not present in
// the supplied sportsbook map; callers may refresh the map and retry.
var ErrUnknownSportsbook = errors.New("unknown sportsbook key")

// Normalize converts one SSE frame into live LineSnapshots plus the game
// metadata the frame carries. SharpAPI shares Odds API sport keys, so the
// league, market, odds-conversion, and selection-building semantics are
// reused from the oddsapi adapter.
func Normalize(f Frame, sportsbookIDs map[string]string) ([]model.LineSnapshot, *model.Game, error) {
	league, ok := oddsapi.SportKeyToLeague[f.SportKey]
	if !ok {
		return nil, nil, fmt.Errorf("unknown sport_key %q", f.SportKey)
	}

	game := &model.Game{
		GameExternalID: f.EventID,
		League:         league,
		HomeTeam:       f.HomeTeam,
		AwayTeam:       f.AwayTeam,
		CommenceTime:   f.CommenceTime,
	}

	marketType, ok := oddsapi.MarketKeyToType[f.Market.Key]
	if !ok {
		return nil, game, fmt.Errorf("unknown market key %q", f.Market.Key)
	}

	sbID, ok := sportsbookIDs[f.Bookmaker.Key]
	if !ok {
		return nil, game, fmt.Errorf("%w: %q", ErrUnknownSportsbook, f.Bookmaker.Key)
	}

	capturedAt := f.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	}

	snapshots := make([]model.LineSnapshot, 0, len(f.Market.Outcomes))
	for _, outcome := range f.Market.Outcomes {
		snapshots = append(snapshots, model.LineSnapshot{
			GameExternalID: f.EventID,
			SportsbookID:   sbID,
			League:         league,
			MarketType:     marketType,
			Selection:      oddsapi.BuildSelection(outcome.Name, outcome.Point, marketType),
			LineValue:      outcome.Point,
			OddsAmerican:   oddsapi.DecimalToAmerican(outcome.Price),
			OddsDecimal:    outcome.Price,
			ImpliedProb:    oddsapi.ImpliedProbability(outcome.Price),
			IsLive:         true,
			CapturedAt:     capturedAt,
			Source:         Source,
		})
	}

	return snapshots, game, nil
}
