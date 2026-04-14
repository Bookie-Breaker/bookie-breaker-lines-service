package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

type LineRepo struct {
	db *pgxpool.Pool
}

func NewLineRepo(db *pgxpool.Pool) *LineRepo {
	return &LineRepo{db: db}
}

func (r *LineRepo) InsertLineSnapshots(ctx context.Context, snapshots []model.LineSnapshot) (int, error) {
	if len(snapshots) == 0 {
		return 0, nil
	}

	query := `INSERT INTO lines.line_snapshots
		(game_external_id, sportsbook_id, league, market_type, selection, line_value, odds_american, odds_decimal, is_live, captured_at, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT ON CONSTRAINT uq_line_snapshots_composite DO NOTHING`

	batch := &pgx.Batch{}
	for _, s := range snapshots {
		batch.Queue(query,
			s.GameExternalID, s.SportsbookID, s.League, s.MarketType, s.Selection,
			s.LineValue, s.OddsAmerican, s.OddsDecimal, s.IsLive, s.CapturedAt, s.Source,
		)
	}

	br := r.db.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	inserted := 0
	for range snapshots {
		ct, err := br.Exec()
		if err != nil {
			return inserted, fmt.Errorf("insert line snapshot: %w", err)
		}
		inserted += int(ct.RowsAffected())
	}

	return inserted, nil
}

func (r *LineRepo) GetCurrentLines(ctx context.Context, filters repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	return r.queryLines(ctx, "", filters)
}

func (r *LineRepo) GetGameLines(ctx context.Context, gameID string, filters repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	filters.GameID = gameID
	return r.queryLines(ctx, "", filters)
}

func (r *LineRepo) queryLines(ctx context.Context, _ string, filters repository.CurrentLineFilters) ([]model.LineSnapshot, bool, error) {
	query := `SELECT DISTINCT ON (ls.game_external_id, ls.sportsbook_id, ls.market_type, ls.selection)
		ls.id, ls.game_external_id, ls.sportsbook_id, sb.key, ls.league, ls.market_type,
		ls.selection, ls.line_value, ls.odds_american, ls.odds_decimal, ls.is_live, ls.captured_at, ls.source
		FROM lines.line_snapshots ls
		JOIN lines.sportsbooks sb ON sb.id = ls.sportsbook_id
		WHERE 1=1`

	args := []any{}
	argIdx := 1
	var conditions []string

	if filters.GameID != "" {
		conditions = append(conditions, fmt.Sprintf(`ls.game_external_id = $%d`, argIdx))
		args = append(args, filters.GameID)
		argIdx++
	}
	if filters.League != "" {
		conditions = append(conditions, fmt.Sprintf(`ls.league = $%d`, argIdx))
		args = append(args, filters.League)
		argIdx++
	}
	if filters.Sportsbook != "" {
		conditions = append(conditions, fmt.Sprintf(`sb.key = $%d`, argIdx))
		args = append(args, filters.Sportsbook)
		argIdx++
	}
	if filters.MarketType != "" {
		conditions = append(conditions, fmt.Sprintf(`ls.market_type = $%d`, argIdx))
		args = append(args, filters.MarketType)
		argIdx++
	}

	for _, c := range conditions {
		query += " AND " + c
	}

	query += ` ORDER BY ls.game_external_id, ls.sportsbook_id, ls.market_type, ls.selection, ls.captured_at DESC`

	limit := filters.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(` LIMIT $%d`, argIdx)
	args = append(args, limit+1) // fetch one extra for has_more

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query current lines: %w", err)
	}
	defer rows.Close()

	var lines []model.LineSnapshot
	for rows.Next() {
		var l model.LineSnapshot
		if err := rows.Scan(
			&l.ID, &l.GameExternalID, &l.SportsbookID, &l.SportsbookKey, &l.League, &l.MarketType,
			&l.Selection, &l.LineValue, &l.OddsAmerican, &l.OddsDecimal, &l.IsLive, &l.CapturedAt, &l.Source,
		); err != nil {
			return nil, false, fmt.Errorf("scan line: %w", err)
		}
		lines = append(lines, l)
	}

	hasMore := len(lines) > limit
	if hasMore {
		lines = lines[:limit]
	}

	return lines, hasMore, rows.Err()
}

func (r *LineRepo) GetLineByID(ctx context.Context, id string) (*model.LineSnapshot, error) {
	var l model.LineSnapshot
	err := r.db.QueryRow(ctx,
		`SELECT ls.id, ls.game_external_id, ls.sportsbook_id, sb.key, ls.league, ls.market_type,
			ls.selection, ls.line_value, ls.odds_american, ls.odds_decimal, ls.is_live, ls.captured_at, ls.source
		FROM lines.line_snapshots ls
		JOIN lines.sportsbooks sb ON sb.id = ls.sportsbook_id
		WHERE ls.id = $1`, id,
	).Scan(
		&l.ID, &l.GameExternalID, &l.SportsbookID, &l.SportsbookKey, &l.League, &l.MarketType,
		&l.Selection, &l.LineValue, &l.OddsAmerican, &l.OddsDecimal, &l.IsLive, &l.CapturedAt, &l.Source,
	)
	if err != nil {
		return nil, fmt.Errorf("get line by id %q: %w", id, err)
	}
	return &l, nil
}

func (r *LineRepo) GetLineMovement(ctx context.Context, gameID string, filters repository.MovementFilters) ([]model.LineSnapshot, error) {
	query := `SELECT ls.id, ls.game_external_id, ls.sportsbook_id, sb.key, ls.league, ls.market_type,
		ls.selection, ls.line_value, ls.odds_american, ls.odds_decimal, ls.is_live, ls.captured_at, ls.source
		FROM lines.line_snapshots ls
		JOIN lines.sportsbooks sb ON sb.id = ls.sportsbook_id
		WHERE ls.game_external_id = $1`

	args := []any{gameID}
	argIdx := 2

	if filters.MarketType != "" {
		query += fmt.Sprintf(` AND ls.market_type = $%d`, argIdx)
		args = append(args, filters.MarketType)
		argIdx++
	}
	if filters.Sportsbook != "" {
		query += fmt.Sprintf(` AND sb.key = $%d`, argIdx)
		args = append(args, filters.Sportsbook)
		argIdx++
	}
	if filters.Selection != "" {
		query += fmt.Sprintf(` AND ls.selection = $%d`, argIdx)
		args = append(args, filters.Selection)
	}

	query += ` ORDER BY ls.captured_at ASC`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query line movement: %w", err)
	}
	defer rows.Close()

	var lines []model.LineSnapshot
	for rows.Next() {
		var l model.LineSnapshot
		if err := rows.Scan(
			&l.ID, &l.GameExternalID, &l.SportsbookID, &l.SportsbookKey, &l.League, &l.MarketType,
			&l.Selection, &l.LineValue, &l.OddsAmerican, &l.OddsDecimal, &l.IsLive, &l.CapturedAt, &l.Source,
		); err != nil {
			return nil, fmt.Errorf("scan movement line: %w", err)
		}
		lines = append(lines, l)
	}

	return lines, rows.Err()
}

func (r *LineRepo) GetClosingLines(ctx context.Context, gameID string, filters repository.ClosingLineFilters) ([]model.ClosingLine, error) {
	query := `SELECT cl.id, cl.game_external_id, cl.sportsbook_id, sb.key, cl.league, cl.market_type,
		cl.selection, cl.line_value, cl.odds_american, cl.odds_decimal, cl.captured_at, cl.created_at
		FROM lines.closing_lines cl
		JOIN lines.sportsbooks sb ON sb.id = cl.sportsbook_id
		WHERE cl.game_external_id = $1`

	args := []any{gameID}
	argIdx := 2

	if filters.Sportsbook != "" {
		query += fmt.Sprintf(` AND sb.key = $%d`, argIdx)
		args = append(args, filters.Sportsbook)
		argIdx++
	}
	if filters.MarketType != "" {
		query += fmt.Sprintf(` AND cl.market_type = $%d`, argIdx)
		args = append(args, filters.MarketType)
	}

	query += ` ORDER BY cl.market_type, cl.selection`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query closing lines: %w", err)
	}
	defer rows.Close()

	var lines []model.ClosingLine
	for rows.Next() {
		var cl model.ClosingLine
		if err := rows.Scan(
			&cl.ID, &cl.GameExternalID, &cl.SportsbookID, &cl.SportsbookKey, &cl.League, &cl.MarketType,
			&cl.Selection, &cl.LineValue, &cl.OddsAmerican, &cl.OddsDecimal, &cl.CapturedAt, &cl.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan closing line: %w", err)
		}
		lines = append(lines, cl)
	}

	return lines, rows.Err()
}
