package oddsapi

import (
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

// Prop structure enums persisted in the prop_type column (ADR-029).
const (
	PropTypeOverUnder = "OVER_UNDER"
	PropTypeYesNo     = "YES_NO"
)

// yesNoPropMarkets are the prop market keys whose outcomes are Yes/No pairs
// rather than Over/Under with a point. Everything else defaults to
// OVER_UNDER.
var yesNoPropMarkets = map[string]struct{}{
	"player_goal_scorer_anytime": {},
	"player_anytime_td":          {},
}

// yesNoSelectionLabels are the human-readable labels embedded in Yes/No prop
// selections ("{Player} {label} Yes|No"). Unknown Yes/No markets fall back to
// a humanized market key.
var yesNoSelectionLabels = map[string]string{
	"player_goal_scorer_anytime": "Anytime Goalscorer",
	"player_anytime_td":          "Anytime TD",
}

// ClassifyMarketKey maps an Odds API market key to our market type enum.
// Exact keys (h2h, spreads, totals) win first; otherwise player-prop
// prefixes (player_*, plus The Odds API's baseball batter_*/pitcher_* keys)
// classify as PLAYER_PROP and team_* as TEAM_PROP. Unrecognized keys return
// false and are skipped by normalization.
func ClassifyMarketKey(key string) (model.MarketType, bool) {
	if mt, ok := MarketKeyToType[key]; ok {
		return mt, true
	}
	switch {
	case strings.HasPrefix(key, "player_"),
		strings.HasPrefix(key, "batter_"),
		strings.HasPrefix(key, "pitcher_"):
		return model.MarketPlayerProp, true
	case strings.HasPrefix(key, "team_"):
		return model.MarketTeamProp, true
	}
	return "", false
}

// PropTypeForMarket returns the prop structure for a prop market key.
func PropTypeForMarket(marketKey string) string {
	if _, ok := yesNoPropMarkets[marketKey]; ok {
		return PropTypeYesNo
	}
	return PropTypeOverUnder
}

// SlugifyPlayerName normalizes a player display name into a stable slug:
// NFKD-decomposed with combining marks (diacritics) folded away, lowercased,
// with every run of non-alphanumeric characters collapsed to a single "-".
// "Kylian Mbappé" -> "kylian-mbappe". The Odds API exposes no stable player
// identifier, so this slug is the player_external_id per ADR-029; downstream
// consumers must treat it as a display-name-derived key, not a roster ID.
func SlugifyPlayerName(name string) string {
	decomposed := norm.NFKD.String(name)

	var b strings.Builder
	b.Grow(len(decomposed))
	lastDash := true // suppress leading dashes
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue // strip combining marks left by NFKD decomposition
		}
		r = unicode.ToLower(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// NormalizeEventProps converts a single event's per-event odds response into
// prop LineSnapshots. Non-prop markets in the response are ignored (the bulk
// ingestion path owns those), as are outcomes missing a player description.
//
// Selections are built so the service layer's deriveSide round-trips them:
//   - OVER_UNDER: "{Player} Over {point}" / "{Player} Under {point}"
//   - YES_NO:     "{Player} Anytime Goalscorer Yes" / "... No"
//
// Per ADR-029 the stat_type column stores the raw Odds API market key and
// selection remains the display string.
func NormalizeEventProps(event Event, sportsbookIDs map[string]string, capturedAt time.Time) []model.LineSnapshot {
	league, ok := SportKeyToLeague[event.SportKey]
	if !ok {
		return nil
	}

	var snapshots []model.LineSnapshot
	for _, bm := range event.Bookmakers {
		sbID, ok := sportsbookIDs[bm.Key]
		if !ok {
			continue
		}

		for _, market := range bm.Markets {
			marketType, ok := ClassifyMarketKey(market.Key)
			if !ok || !isPropMarketType(marketType) {
				continue
			}
			propType := PropTypeForMarket(market.Key)

			for _, outcome := range market.Outcomes {
				if outcome.Description == "" {
					continue // player props without a player are unusable
				}
				selection := buildPropSelection(market.Key, propType, outcome)

				snapshots = append(snapshots, model.LineSnapshot{
					GameExternalID:   event.ID,
					SportsbookID:     sbID,
					League:           league,
					MarketType:       marketType,
					Selection:        selection,
					PlayerExternalID: SlugifyPlayerName(outcome.Description),
					StatType:         market.Key,
					PropType:         propType,
					LineValue:        outcome.Point,
					OddsAmerican:     DecimalToAmerican(outcome.Price),
					OddsDecimal:      outcome.Price,
					ImpliedProb:      ImpliedProbability(outcome.Price),
					IsLive:           false,
					CapturedAt:       capturedAt,
					Source:           "the_odds_api",
				})
			}
		}
	}
	return snapshots
}

func isPropMarketType(mt model.MarketType) bool {
	switch mt {
	case model.MarketPlayerProp, model.MarketTeamProp, model.MarketGameProp:
		return true
	}
	return false
}

// buildPropSelection builds the display selection string for a prop outcome.
// The outcome keyword placement matches deriveSide's prop branch: Over/Under
// as whole interior tokens, Yes/No as trailing tokens.
func buildPropSelection(marketKey, propType string, outcome Outcome) string {
	player := strings.TrimSpace(outcome.Description)
	if propType == PropTypeYesNo {
		label, ok := yesNoSelectionLabels[marketKey]
		if !ok {
			label = humanizeMarketKey(marketKey)
		}
		return player + " " + label + " " + outcome.Name
	}
	if outcome.Point != nil {
		return player + " " + outcome.Name + " " + formatFloat(*outcome.Point)
	}
	return player + " " + outcome.Name
}

// humanizeMarketKey turns a market key like "player_first_td" into
// "First Td" (prop prefix stripped, words title-cased).
func humanizeMarketKey(key string) string {
	for _, prefix := range []string{"player_", "batter_", "pitcher_", "team_"} {
		if strings.HasPrefix(key, prefix) {
			key = strings.TrimPrefix(key, prefix)
			break
		}
	}
	words := strings.Split(key, "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
