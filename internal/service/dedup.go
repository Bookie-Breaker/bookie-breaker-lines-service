package service

import (
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

// FilterChanged returns only the snapshots whose value-bearing fields differ
// from the latest persisted snapshot for the same line key. Keys absent from
// latest are new lines and always kept.
func FilterChanged(latest map[repository.LineKey]repository.LineValues, snapshots []model.LineSnapshot) []model.LineSnapshot {
	if len(latest) == 0 {
		return snapshots
	}

	changed := make([]model.LineSnapshot, 0, len(snapshots))
	for _, s := range snapshots {
		key := repository.LineKey{
			GameExternalID: s.GameExternalID,
			SportsbookID:   s.SportsbookID,
			MarketType:     s.MarketType,
			Selection:      s.Selection,
		}
		prev, ok := latest[key]
		if !ok || !sameLineValues(prev, s) {
			changed = append(changed, s)
		}
	}
	return changed
}

func sameLineValues(prev repository.LineValues, s model.LineSnapshot) bool {
	if prev.OddsAmerican != s.OddsAmerican || prev.IsLive != s.IsLive {
		return false
	}
	switch {
	case prev.LineValue == nil && s.LineValue == nil:
		return true
	case prev.LineValue == nil || s.LineValue == nil:
		return false
	default:
		return *prev.LineValue == *s.LineValue
	}
}
