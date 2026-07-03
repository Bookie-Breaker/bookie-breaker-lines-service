package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

type GameRepo struct {
	db *pgxpool.Pool
}

func NewGameRepo(db *pgxpool.Pool) *GameRepo {
	return &GameRepo{db: db}
}

func (r *GameRepo) UpsertGames(ctx context.Context, games []model.Game) error {
	if len(games) == 0 {
		return nil
	}

	query := `INSERT INTO lines.games (game_external_id, league, home_team, away_team, commence_time)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (game_external_id) DO UPDATE SET
			home_team = EXCLUDED.home_team,
			away_team = EXCLUDED.away_team,
			commence_time = EXCLUDED.commence_time,
			updated_at = NOW()`

	batch := &pgx.Batch{}
	for _, g := range games {
		batch.Queue(query, g.GameExternalID, g.League, g.HomeTeam, g.AwayTeam, g.CommenceTime)
	}

	br := r.db.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for range games {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("upsert game: %w", err)
		}
	}

	return nil
}

func (r *GameRepo) GetGame(ctx context.Context, gameExternalID string) (*model.Game, error) {
	var g model.Game
	err := r.db.QueryRow(ctx,
		`SELECT game_external_id, league, home_team, away_team, commence_time, closing_captured_at, first_seen_at, updated_at
		FROM lines.games WHERE game_external_id = $1`, gameExternalID,
	).Scan(
		&g.GameExternalID, &g.League, &g.HomeTeam, &g.AwayTeam,
		&g.CommenceTime, &g.ClosingCapturedAt, &g.FirstSeenAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get game %q: %w", gameExternalID, err)
	}
	return &g, nil
}

func (r *GameRepo) GetGames(ctx context.Context, gameExternalIDs []string) (map[string]model.Game, error) {
	games := make(map[string]model.Game, len(gameExternalIDs))
	if len(gameExternalIDs) == 0 {
		return games, nil
	}

	rows, err := r.db.Query(ctx,
		`SELECT game_external_id, league, home_team, away_team, commence_time, closing_captured_at, first_seen_at, updated_at
		FROM lines.games WHERE game_external_id = ANY($1)`, gameExternalIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("query games: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var g model.Game
		if err := rows.Scan(
			&g.GameExternalID, &g.League, &g.HomeTeam, &g.AwayTeam,
			&g.CommenceTime, &g.ClosingCapturedAt, &g.FirstSeenAt, &g.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan game: %w", err)
		}
		games[g.GameExternalID] = g
	}

	return games, rows.Err()
}

// GetGamesDueForClosing returns games that have started but have no closing
// lines captured yet. The 7-day lower bound avoids replaying ancient games
// when the sweep first deploys against existing data.
func (r *GameRepo) GetGamesDueForClosing(ctx context.Context, now time.Time) ([]model.Game, error) {
	rows, err := r.db.Query(ctx,
		`SELECT game_external_id, league, home_team, away_team, commence_time, closing_captured_at, first_seen_at, updated_at
		FROM lines.games
		WHERE closing_captured_at IS NULL
		  AND commence_time <= $1
		  AND commence_time > $1 - INTERVAL '7 days'
		ORDER BY commence_time`, now,
	)
	if err != nil {
		return nil, fmt.Errorf("query games due for closing: %w", err)
	}
	defer rows.Close()

	var games []model.Game
	for rows.Next() {
		var g model.Game
		if err := rows.Scan(
			&g.GameExternalID, &g.League, &g.HomeTeam, &g.AwayTeam,
			&g.CommenceTime, &g.ClosingCapturedAt, &g.FirstSeenAt, &g.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan game due for closing: %w", err)
		}
		games = append(games, g)
	}

	return games, rows.Err()
}

func (r *GameRepo) MarkClosingCaptured(ctx context.Context, gameExternalID string, capturedAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE lines.games SET closing_captured_at = $2, updated_at = NOW() WHERE game_external_id = $1`,
		gameExternalID, capturedAt,
	)
	if err != nil {
		return fmt.Errorf("mark closing captured for %q: %w", gameExternalID, err)
	}
	return nil
}
