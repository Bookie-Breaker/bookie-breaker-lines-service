package repository

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

// ErrInvalidCursor is returned when a pagination cursor cannot be decoded.
var ErrInvalidCursor = errors.New("invalid pagination cursor")

// cursorPayload is the wire form of a keyset cursor: the sort key of the last
// row on the previous page. Opaque to clients.
type cursorPayload struct {
	Game       string `json:"g"`
	Sportsbook string `json:"b"`
	Market     string `json:"m"`
	Selection  string `json:"s"`
}

// EncodeCursor serializes a line key into an opaque pagination cursor.
func EncodeCursor(k LineKey) string {
	b, err := json.Marshal(cursorPayload{
		Game:       k.GameExternalID,
		Sportsbook: k.SportsbookID,
		Market:     string(k.MarketType),
		Selection:  k.Selection,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses an opaque pagination cursor back into a line key.
func DecodeCursor(s string) (LineKey, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return LineKey{}, ErrInvalidCursor
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return LineKey{}, ErrInvalidCursor
	}
	if p.Game == "" || p.Sportsbook == "" || p.Market == "" || p.Selection == "" {
		return LineKey{}, ErrInvalidCursor
	}
	return LineKey{
		GameExternalID: p.Game,
		SportsbookID:   p.Sportsbook,
		MarketType:     model.MarketType(p.Market),
		Selection:      p.Selection,
	}, nil
}
