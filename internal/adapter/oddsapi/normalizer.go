package oddsapi

import (
	"fmt"
	"math"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

// SportKeyToLeague maps The Odds API sport keys to our league enum.
var SportKeyToLeague = map[string]model.League{
	"basketball_nba":         model.LeagueNBA,
	"basketball_ncaab":       model.LeagueNCAACB,
	"americanfootball_nfl":   model.LeagueNFL,
	"americanfootball_ncaaf": model.LeagueNCAAFB,
	"baseball_mlb":           model.LeagueMLB,
	"baseball_ncaa":          model.LeagueNCAACBB,
}

// MarketKeyToType maps The Odds API market keys to our market type enum.
var MarketKeyToType = map[string]model.MarketType{
	"h2h":     model.MarketMoneyline,
	"spreads": model.MarketSpread,
	"totals":  model.MarketTotal,
}

// DecimalToAmerican converts decimal odds to American format.
func DecimalToAmerican(decimal float64) int {
	if decimal >= 2.0 {
		return int(math.Round((decimal - 1) * 100))
	}
	return int(math.Round(-100 / (decimal - 1)))
}

// AmericanToDecimal converts American odds to decimal format.
func AmericanToDecimal(american int) float64 {
	if american > 0 {
		return 1 + float64(american)/100
	}
	return 1 + 100/math.Abs(float64(american))
}

// ImpliedProbability computes raw implied probability from decimal odds.
func ImpliedProbability(decimal float64) float64 {
	if decimal <= 0 {
		return 0
	}
	return 1 / decimal
}

// NormalizeResult holds the normalized snapshots from a single API response.
type NormalizeResult struct {
	Snapshots []model.LineSnapshot
	GameCount int
}

// Normalize converts Odds API events into domain LineSnapshot models.
// sportsbookIDs maps sportsbook key -> UUID string.
func Normalize(events OddsResponse, sportsbookIDs map[string]string, capturedAt time.Time) NormalizeResult {
	var snapshots []model.LineSnapshot

	for _, event := range events {
		league, ok := SportKeyToLeague[event.SportKey]
		if !ok {
			continue
		}

		for _, bm := range event.Bookmakers {
			sbID, ok := sportsbookIDs[bm.Key]
			if !ok {
				continue
			}

			for _, market := range bm.Markets {
				marketType, ok := MarketKeyToType[market.Key]
				if !ok {
					continue
				}

				for _, outcome := range market.Outcomes {
					selection := buildSelection(outcome, marketType, event.HomeTeam)
					american := DecimalToAmerican(outcome.Price)

					snap := model.LineSnapshot{
						GameExternalID: event.ID,
						SportsbookID:   sbID,
						League:         league,
						MarketType:     marketType,
						Selection:      selection,
						LineValue:      outcome.Point,
						OddsAmerican:   american,
						OddsDecimal:    outcome.Price,
						ImpliedProb:    ImpliedProbability(outcome.Price),
						IsLive:         false,
						CapturedAt:     capturedAt,
						Source:         "the_odds_api",
					}

					snapshots = append(snapshots, snap)
				}
			}
		}
	}

	return NormalizeResult{
		Snapshots: snapshots,
		GameCount: len(events),
	}
}

// buildSelection creates human-readable selection strings.
func buildSelection(outcome Outcome, marketType model.MarketType, homeTeam string) string {
	switch marketType {
	case model.MarketSpread:
		if outcome.Point != nil {
			sign := "+"
			if *outcome.Point < 0 {
				sign = ""
			}
			return outcome.Name + " " + sign + formatFloat(*outcome.Point)
		}
		return outcome.Name
	case model.MarketTotal:
		if outcome.Point != nil {
			if outcome.Name == "Over" || outcome.Name == "Under" {
				return outcome.Name + " " + formatFloat(*outcome.Point)
			}
		}
		return outcome.Name
	case model.MarketMoneyline:
		return outcome.Name
	default:
		return outcome.Name
	}
}

func formatFloat(f float64) string {
	if f == float64(int(f)) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.1f", f)
}
