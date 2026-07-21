// In-package repository tests against real Postgres (TimescaleDB) via
// testcontainers, mirroring tests/integration/setup_test.go. Skipped when
// Docker is unavailable so local pre-push hooks pass on Docker-less machines.
package postgres

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/database"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
)

var testPool *pgxpool.Pool

func dockerAvailable(ctx context.Context) bool {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return false
	}
	defer func() { _ = provider.Close() }()
	return provider.Health(ctx) == nil
}

func TestMain(m *testing.M) {
	ctx := context.Background()

	if !dockerAvailable(ctx) {
		log.Println("skipping repository tests: Docker is not available")
		os.Exit(0)
	}

	code, err := run(ctx, m)
	if err != nil {
		log.Fatalf("repository test setup failed: %v", err)
	}
	os.Exit(code)
}

func run(ctx context.Context, m *testing.M) (int, error) {
	pgContainer, err := tcpostgres.Run(ctx, "timescale/timescaledb:latest-pg16",
		tcpostgres.WithDatabase("bookiebreaker"),
		tcpostgres.WithUsername("bookiebreaker"),
		tcpostgres.WithPassword("localdev"),
		tcpostgres.WithInitScripts("../../../tests/integration/testdata/00-init.sql"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return 0, fmt.Errorf("start postgres container: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(pgContainer) }()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, fmt.Errorf("postgres connection string: %w", err)
	}

	migrateURL := strings.Replace(connStr, "postgres://", "pgx5://", 1)
	mig, err := migrate.New("file://../../../migrations", migrateURL)
	if err != nil {
		return 0, fmt.Errorf("create migrate instance: %w", err)
	}
	if err := mig.Up(); err != nil && err != migrate.ErrNoChange {
		return 0, fmt.Errorf("run migrations: %w", err)
	}
	_, _ = mig.Close()

	poolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	testPool, err = database.NewPool(poolCtx, connStr+"&search_path=lines,public")
	if err != nil {
		return 0, fmt.Errorf("connect pgx pool: %w", err)
	}
	defer testPool.Close()

	return m.Run(), nil
}

// seedSportsbook inserts a sportsbook and returns its generated UUID.
func seedSportsbook(t *testing.T, key, name string, isSharp, isActive bool) string {
	t.Helper()
	var id string
	err := testPool.QueryRow(context.Background(),
		`INSERT INTO lines.sportsbooks (name, key, is_sharp, is_active) VALUES ($1, $2, $3, $4) RETURNING id`,
		name, key, isSharp, isActive,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed sportsbook %s: %v", key, err)
	}
	return id
}

// seedGame upserts one game row through the repository under test.
func seedGame(t *testing.T, gameID string, commence time.Time) {
	t.Helper()
	repo := NewGameRepo(testPool)
	err := repo.UpsertGames(context.Background(), []model.Game{{
		GameExternalID: gameID,
		League:         model.LeagueNBA,
		HomeTeam:       "Los Angeles Lakers",
		AwayTeam:       "Boston Celtics",
		CommenceTime:   commence,
	}})
	if err != nil {
		t.Fatalf("seed game %s: %v", gameID, err)
	}
}

// snap builds a moneyline snapshot for insertion tests.
func snap(gameID, sbID, selection string, lineValue *float64, oddsAmerican int, capturedAt time.Time) model.LineSnapshot {
	marketType := model.MarketMoneyline
	if lineValue != nil {
		marketType = model.MarketSpread
	}
	return model.LineSnapshot{
		GameExternalID: gameID,
		SportsbookID:   sbID,
		League:         model.LeagueNBA,
		MarketType:     marketType,
		Selection:      selection,
		LineValue:      lineValue,
		OddsAmerican:   oddsAmerican,
		OddsDecimal:    1.91,
		CapturedAt:     capturedAt,
		Source:         "the_odds_api",
	}
}

func fptr(f float64) *float64 { return &f }
