package repository

import (
	"context"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

type CurrentLineFilters struct {
	Leagues     []string
	GameIDs     []string
	Sportsbooks []string
	MarketTypes []string
	Date        string
	Limit       int
	Cursor      string
}

type MovementFilters struct {
	Sportsbook string
	MarketType string
	Selection  string
}

type BestLineFilters struct {
	MarketType string
	Selection  string
}

type ClosingLineFilters struct {
	Sportsbook string
	MarketType string
}

// LineKey identifies a unique line: one selection at one book in one market of one game.
type LineKey struct {
	GameExternalID string
	SportsbookID   string
	MarketType     model.MarketType
	Selection      string
}

// LineValues holds the value-bearing fields of the latest snapshot for a LineKey,
// used to skip persisting unchanged lines.
type LineValues struct {
	LineValue    *float64
	OddsAmerican int
	IsLive       bool
}

type LineRepository interface {
	InsertLineSnapshots(ctx context.Context, snapshots []model.LineSnapshot) (int, error)
	GetLatestLineValues(ctx context.Context, gameIDs []string) (map[LineKey]LineValues, error)
	GetCurrentLines(ctx context.Context, filters CurrentLineFilters) ([]model.LineSnapshot, bool, error)
	GetLineByID(ctx context.Context, id string) (*model.LineSnapshot, error)
	GetGameLines(ctx context.Context, gameID string, filters CurrentLineFilters) ([]model.LineSnapshot, bool, error)
	GetLineMovement(ctx context.Context, gameID string, filters MovementFilters) ([]model.LineSnapshot, error)
	GetClosingLines(ctx context.Context, gameID string, filters ClosingLineFilters) ([]model.ClosingLine, error)
	CaptureClosingLines(ctx context.Context, gameExternalID string, commenceTime time.Time) (int, error)
}

type GameRepository interface {
	UpsertGames(ctx context.Context, games []model.Game) error
	GetGame(ctx context.Context, gameExternalID string) (*model.Game, error)
	GetGames(ctx context.Context, gameExternalIDs []string) (map[string]model.Game, error)
	GetGamesDueForClosing(ctx context.Context, now time.Time) ([]model.Game, error)
	MarkClosingCaptured(ctx context.Context, gameExternalID string, capturedAt time.Time) error
}

type SportsbookRepository interface {
	GetAll(ctx context.Context, isSharp *bool, isActive *bool) ([]model.Sportsbook, error)
	GetByKey(ctx context.Context, key string) (*model.Sportsbook, error)
	GetByID(ctx context.Context, id string) (*model.Sportsbook, error)
}

type RawResponseRepository interface {
	Insert(ctx context.Context, resp model.RawAPIResponse) error
}
