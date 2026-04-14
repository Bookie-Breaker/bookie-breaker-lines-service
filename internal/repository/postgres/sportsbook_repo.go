package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

type SportsbookRepo struct {
	db *pgxpool.Pool
}

func NewSportsbookRepo(db *pgxpool.Pool) *SportsbookRepo {
	return &SportsbookRepo{db: db}
}

func (r *SportsbookRepo) GetAll(ctx context.Context, isSharp *bool, isActive *bool) ([]model.Sportsbook, error) {
	query := `SELECT id, name, key, is_sharp, is_active, created_at, updated_at FROM lines.sportsbooks WHERE 1=1`
	args := []any{}
	argIdx := 1

	if isSharp != nil {
		query += fmt.Sprintf(` AND is_sharp = $%d`, argIdx)
		args = append(args, *isSharp)
		argIdx++
	}
	if isActive != nil {
		query += fmt.Sprintf(` AND is_active = $%d`, argIdx)
		args = append(args, *isActive)
	}

	query += ` ORDER BY name`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sportsbooks: %w", err)
	}
	defer rows.Close()

	var sportsbooks []model.Sportsbook
	for rows.Next() {
		var s model.Sportsbook
		if err := rows.Scan(&s.ID, &s.Name, &s.Key, &s.IsSharp, &s.IsActive, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sportsbook: %w", err)
		}
		sportsbooks = append(sportsbooks, s)
	}

	return sportsbooks, rows.Err()
}

func (r *SportsbookRepo) GetByKey(ctx context.Context, key string) (*model.Sportsbook, error) {
	var s model.Sportsbook
	err := r.db.QueryRow(ctx,
		`SELECT id, name, key, is_sharp, is_active, created_at, updated_at FROM lines.sportsbooks WHERE key = $1`,
		key,
	).Scan(&s.ID, &s.Name, &s.Key, &s.IsSharp, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get sportsbook by key %q: %w", key, err)
	}
	return &s, nil
}

func (r *SportsbookRepo) GetByID(ctx context.Context, id string) (*model.Sportsbook, error) {
	var s model.Sportsbook
	err := r.db.QueryRow(ctx,
		`SELECT id, name, key, is_sharp, is_active, created_at, updated_at FROM lines.sportsbooks WHERE id = $1`,
		id,
	).Scan(&s.ID, &s.Name, &s.Key, &s.IsSharp, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get sportsbook by id %q: %w", id, err)
	}
	return &s, nil
}
