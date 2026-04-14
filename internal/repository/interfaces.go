package repository

import (
	"context"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

type CurrentLineFilters struct {
	League     string
	GameID     string
	Sportsbook string
	MarketType string
	Date       string
	Limit      int
	Cursor     string
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

type LineRepository interface {
	InsertLineSnapshots(ctx context.Context, snapshots []model.LineSnapshot) (int, error)
	GetCurrentLines(ctx context.Context, filters CurrentLineFilters) ([]model.LineSnapshot, bool, error)
	GetLineByID(ctx context.Context, id string) (*model.LineSnapshot, error)
	GetGameLines(ctx context.Context, gameID string, filters CurrentLineFilters) ([]model.LineSnapshot, bool, error)
	GetLineMovement(ctx context.Context, gameID string, filters MovementFilters) ([]model.LineSnapshot, error)
	GetClosingLines(ctx context.Context, gameID string, filters ClosingLineFilters) ([]model.ClosingLine, error)
}

type SportsbookRepository interface {
	GetAll(ctx context.Context, isSharp *bool, isActive *bool) ([]model.Sportsbook, error)
	GetByKey(ctx context.Context, key string) (*model.Sportsbook, error)
	GetByID(ctx context.Context, id string) (*model.Sportsbook, error)
}

type RawResponseRepository interface {
	Insert(ctx context.Context, resp model.RawAPIResponse) error
}
