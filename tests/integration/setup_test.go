// Package integration exercises the service against real Postgres
// (TimescaleDB) and Redis via testcontainers. Tests are skipped when Docker
// is unavailable so local pre-push hooks still pass on Docker-less machines.
package integration

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
	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/database"
)

var (
	testPool  *pgxpool.Pool
	testRedis *goredis.Client
)

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
		log.Println("skipping integration tests: Docker is not available")
		os.Exit(0)
	}

	code, err := run(ctx, m)
	if err != nil {
		log.Fatalf("integration setup failed: %v", err)
	}
	os.Exit(code)
}

func run(ctx context.Context, m *testing.M) (int, error) {
	pgContainer, err := tcpostgres.Run(ctx, "timescale/timescaledb:latest-pg16",
		tcpostgres.WithDatabase("bookiebreaker"),
		tcpostgres.WithUsername("bookiebreaker"),
		tcpostgres.WithPassword("localdev"),
		tcpostgres.WithInitScripts("testdata/00-init.sql"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return 0, fmt.Errorf("start postgres container: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(pgContainer) }()

	redisContainer, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		return 0, fmt.Errorf("start redis container: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(redisContainer) }()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, fmt.Errorf("postgres connection string: %w", err)
	}

	if err := runMigrations(connStr); err != nil {
		return 0, err
	}

	poolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	testPool, err = database.NewPool(poolCtx, connStr+"&search_path=lines,public")
	if err != nil {
		return 0, fmt.Errorf("connect pgx pool: %w", err)
	}
	defer testPool.Close()

	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		return 0, fmt.Errorf("redis connection string: %w", err)
	}
	testRedis, err = cache.NewClient(ctx, redisURL)
	if err != nil {
		return 0, fmt.Errorf("connect redis: %w", err)
	}
	defer func() { _ = testRedis.Close() }()

	return m.Run(), nil
}

func runMigrations(connStr string) error {
	migrateURL := strings.Replace(connStr, "postgres://", "pgx5://", 1)
	mig, err := migrate.New("file://../../migrations", migrateURL)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer func() { _, _ = mig.Close() }()

	if err := mig.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
