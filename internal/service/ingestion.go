package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

var ingestionTracer = otel.Tracer("ingestion-service")

// IngestionResult summarizes the outcome of an ingestion cycle.
type IngestionResult struct {
	League        string `json:"league"`
	GamesFound    int    `json:"games_found"`
	LinesIngested int    `json:"lines_ingested"`
	LinesSkipped  int    `json:"lines_skipped"`
	DurationMs    int64  `json:"duration_ms"`
}

// IngestionService orchestrates fetching, normalizing, and persisting lines.
type IngestionService struct {
	client    *oddsapi.Client
	lineRepo  repository.LineRepository
	gameRepo  repository.GameRepository
	sbRepo    repository.SportsbookRepository
	rawRepo   repository.RawResponseRepository
	lineCache *cache.LineCache
	publisher *pubsub.Publisher

	// mu serializes persistSnapshots so a manual trigger, a scheduler tick,
	// and the live consumer cannot interleave their dedup reads and inserts.
	mu sync.Mutex
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(
	client *oddsapi.Client,
	lineRepo repository.LineRepository,
	gameRepo repository.GameRepository,
	sbRepo repository.SportsbookRepository,
	rawRepo repository.RawResponseRepository,
	lineCache *cache.LineCache,
	publisher *pubsub.Publisher,
) *IngestionService {
	return &IngestionService{
		client:    client,
		lineRepo:  lineRepo,
		gameRepo:  gameRepo,
		sbRepo:    sbRepo,
		rawRepo:   rawRepo,
		lineCache: lineCache,
		publisher: publisher,
	}
}

// Ingest fetches and processes lines for a sport from The Odds API.
func (s *IngestionService) Ingest(ctx context.Context, sportKey string) (*IngestionResult, error) {
	ctx, span := ingestionTracer.Start(ctx, "ingestion.Ingest")
	defer span.End()
	span.SetAttributes(attribute.String("sport_key", sportKey))

	start := time.Now()

	slog.Info("starting ingestion cycle", "sport", sportKey)

	result, err := s.client.GetOdds(ctx, sportKey, []string{"h2h", "spreads", "totals"})
	if err != nil {
		return nil, fmt.Errorf("fetch odds for %s: %w", sportKey, err)
	}

	// Archive raw response
	go func() {
		archiveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rawBody := string(result.RawBody)
		if archiveErr := s.rawRepo.Insert(archiveCtx, model.RawAPIResponse{
			Service:      "lines-service",
			Source:       "the_odds_api",
			Endpoint:     fmt.Sprintf("/v4/sports/%s/odds", sportKey),
			HTTPStatus:   result.HTTPStatus,
			ResponseBody: rawBody,
			CapturedAt:   time.Now().UTC(),
		}); archiveErr != nil {
			slog.Warn("failed to archive raw response", "error", archiveErr)
		}
	}()

	// Build sportsbook key -> ID map
	sportsbooks, err := s.sbRepo.GetAll(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch sportsbooks: %w", err)
	}
	sbMap := make(map[string]string, len(sportsbooks))
	for _, sb := range sportsbooks {
		sbMap[sb.Key] = sb.ID
	}

	// Normalize
	normalized := oddsapi.Normalize(result.Events, sbMap, time.Now().UTC())

	// Record game metadata (commence time, teams) for side derivation,
	// date filtering, and the closing-line sweep.
	if err := s.gameRepo.UpsertGames(ctx, normalized.Games); err != nil {
		return nil, fmt.Errorf("upsert games: %w", err)
	}

	if len(normalized.Snapshots) == 0 {
		slog.Info("no lines to ingest", "sport", sportKey)
		return &IngestionResult{
			League:     sportKey,
			GamesFound: normalized.GameCount,
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	inserted, skipped, err := s.persistSnapshots(ctx, normalized.Snapshots, sbMap)
	if err != nil {
		return nil, err
	}

	if inserted == 0 {
		slog.Info("no line changes detected", "sport", sportKey, "lines_skipped", skipped)
		return &IngestionResult{
			League:       sportKey,
			GamesFound:   normalized.GameCount,
			LinesSkipped: skipped,
			DurationMs:   time.Since(start).Milliseconds(),
		}, nil
	}

	duration := time.Since(start)
	slog.Info("ingestion cycle complete",
		"sport", sportKey,
		"games", normalized.GameCount,
		"lines_inserted", inserted,
		"lines_skipped", skipped,
		"duration_ms", duration.Milliseconds(),
	)

	span.SetAttributes(
		attribute.Int("lines.inserted", inserted),
		attribute.Int("lines.skipped", skipped),
		attribute.Int("games.count", normalized.GameCount),
	)

	return &IngestionResult{
		League:        sportKey,
		GamesFound:    normalized.GameCount,
		LinesIngested: inserted,
		LinesSkipped:  skipped,
		DurationMs:    duration.Milliseconds(),
	}, nil
}

// persistSnapshots is the single write path for line snapshots: it dedups
// against the latest persisted values, inserts the changed rows, invalidates
// the per-game cache, and publishes a lines.updated event whose is_live and
// source fields are taken from the snapshots themselves. Both REST polling
// ingestion and the SharpAPI live consumer go through here; the mutex keeps
// their dedup reads and inserts from interleaving.
func (s *IngestionService) persistSnapshots(ctx context.Context, snapshots []model.LineSnapshot, sbMap map[string]string) (inserted, skipped int, err error) {
	if len(snapshots) == 0 {
		return 0, 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	latest, err := s.lineRepo.GetLatestLineValues(ctx, uniqueGameIDs(snapshots))
	if err != nil {
		return 0, 0, fmt.Errorf("fetch latest line values: %w", err)
	}
	changed := FilterChanged(latest, snapshots)
	skipped = len(snapshots) - len(changed)

	if len(changed) == 0 {
		return 0, skipped, nil
	}

	inserted, err = s.lineRepo.InsertLineSnapshots(ctx, changed)
	if err != nil {
		return 0, skipped, fmt.Errorf("insert line snapshots: %w", err)
	}

	// Invalidate cache for affected games
	gameIDs := uniqueGameIDs(changed)
	for _, gid := range gameIDs {
		if cacheErr := s.lineCache.InvalidateGame(ctx, gid); cacheErr != nil {
			slog.Warn("failed to invalidate cache", "game_id", gid, "error", cacheErr)
		}
	}

	first := changed[0]
	if pubErr := s.publisher.PublishLinesUpdated(ctx, pubsub.LinesUpdatedEvent{
		League:             string(first.League),
		GameIDs:            gameIDs,
		MarketTypes:        uniqueMarketTypes(changed),
		SportsbooksUpdated: uniqueSportsbooks(changed, sbMap),
		ChangeCount:        inserted,
		IsLive:             first.IsLive,
		Source:             first.Source,
	}); pubErr != nil {
		slog.Warn("failed to publish lines.updated event", "error", pubErr)
	}

	return inserted, skipped, nil
}

func uniqueGameIDs(snapshots []model.LineSnapshot) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, s := range snapshots {
		if _, ok := seen[s.GameExternalID]; !ok {
			seen[s.GameExternalID] = struct{}{}
			ids = append(ids, s.GameExternalID)
		}
	}
	return ids
}

func uniqueMarketTypes(snapshots []model.LineSnapshot) []string {
	seen := make(map[model.MarketType]struct{})
	var types []string
	for _, s := range snapshots {
		if _, ok := seen[s.MarketType]; !ok {
			seen[s.MarketType] = struct{}{}
			types = append(types, string(s.MarketType))
		}
	}
	return types
}

func uniqueSportsbooks(snapshots []model.LineSnapshot, sbMap map[string]string) []string {
	idToKey := make(map[string]string, len(sbMap))
	for k, v := range sbMap {
		idToKey[v] = k
	}
	seen := make(map[string]struct{})
	var keys []string
	for _, s := range snapshots {
		key := idToKey[s.SportsbookID]
		if _, ok := seen[key]; !ok && key != "" {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	return keys
}
