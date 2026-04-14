package service

import (
	"context"
	"fmt"
	"log/slog"
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
	DurationMs    int64  `json:"duration_ms"`
}

// IngestionService orchestrates fetching, normalizing, and persisting lines.
type IngestionService struct {
	client    *oddsapi.Client
	lineRepo  repository.LineRepository
	sbRepo    repository.SportsbookRepository
	rawRepo   repository.RawResponseRepository
	lineCache *cache.LineCache
	publisher *pubsub.Publisher
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(
	client *oddsapi.Client,
	lineRepo repository.LineRepository,
	sbRepo repository.SportsbookRepository,
	rawRepo repository.RawResponseRepository,
	lineCache *cache.LineCache,
	publisher *pubsub.Publisher,
) *IngestionService {
	return &IngestionService{
		client:    client,
		lineRepo:  lineRepo,
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

	if len(normalized.Snapshots) == 0 {
		slog.Info("no lines to ingest", "sport", sportKey)
		return &IngestionResult{
			League:     sportKey,
			GamesFound: normalized.GameCount,
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	// Persist
	inserted, err := s.lineRepo.InsertLineSnapshots(ctx, normalized.Snapshots)
	if err != nil {
		return nil, fmt.Errorf("insert line snapshots: %w", err)
	}

	// Invalidate cache for affected games
	gameIDs := uniqueGameIDs(normalized.Snapshots)
	for _, gid := range gameIDs {
		if cacheErr := s.lineCache.InvalidateGame(ctx, gid); cacheErr != nil {
			slog.Warn("failed to invalidate cache", "game_id", gid, "error", cacheErr)
		}
	}

	// Publish event
	league := ""
	if len(normalized.Snapshots) > 0 {
		league = string(normalized.Snapshots[0].League)
	}
	if pubErr := s.publisher.PublishLinesUpdated(ctx, pubsub.LinesUpdatedEvent{
		League:             league,
		GameIDs:            gameIDs,
		MarketTypes:        uniqueMarketTypes(normalized.Snapshots),
		SportsbooksUpdated: uniqueSportsbooks(normalized.Snapshots, sbMap),
		ChangeCount:        inserted,
		Source:             "the_odds_api",
	}); pubErr != nil {
		slog.Warn("failed to publish lines.updated event", "error", pubErr)
	}

	duration := time.Since(start)
	slog.Info("ingestion cycle complete",
		"sport", sportKey,
		"games", normalized.GameCount,
		"lines_inserted", inserted,
		"duration_ms", duration.Milliseconds(),
	)

	span.SetAttributes(
		attribute.Int("lines.inserted", inserted),
		attribute.Int("games.count", normalized.GameCount),
	)

	return &IngestionResult{
		League:        sportKey,
		GamesFound:    normalized.GameCount,
		LinesIngested: inserted,
		DurationMs:    duration.Milliseconds(),
	}, nil
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
