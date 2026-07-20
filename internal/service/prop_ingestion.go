package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

var propTracer = otel.Tracer("prop-ingestion-service")

const (
	defaultPropCommenceWindow    = 48 * time.Hour
	defaultPropMaxEventsPerCycle = 10
)

// PropMarketsBySport maps Odds API sport keys to the canonical prop market
// keys we request per event. Player props are only served by the per-event
// odds endpoint, so every listed market costs one request per event per
// cycle — keys here are the quota budget, not a wish list. NBA and NFL are
// mapped but dormant: they only ingest once their sport key is added to the
// PROP_SPORTS allow-list (an env change, no code change).
var PropMarketsBySport = map[string][]string{
	"soccer_fifa_world_cup": {"player_goal_scorer_anytime", "player_shots", "player_shots_on_target"},
	"soccer_epl":            {"player_goal_scorer_anytime", "player_shots", "player_shots_on_target"},
	"baseball_mlb":          {"batter_hits", "batter_total_bases", "batter_home_runs", "pitcher_strikeouts"},
	"basketball_nba":        {"player_points", "player_rebounds", "player_assists", "player_threes", "player_points_rebounds_assists"},
	"americanfootball_nfl":  {"player_pass_yds", "player_rush_yds", "player_reception_yds", "player_receptions", "player_anytime_td"},
}

// PropIngestionResult summarizes one prop ingestion cycle for a sport.
type PropIngestionResult struct {
	Sport         string `json:"sport"`
	EventsListed  int    `json:"events_listed"`
	EventsInScope int    `json:"events_in_scope"`
	EventsFetched int    `json:"events_fetched"`
	EventsCapped  int    `json:"events_capped"`
	LinesIngested int    `json:"lines_ingested"`
	LinesSkipped  int    `json:"lines_skipped"`
	DurationMs    int64  `json:"duration_ms"`
}

// PropIngestionService ingests player-prop lines. Props require one Odds API
// request per event, so the service gates quota spend three ways: a sport
// allow-list (PROP_SPORTS, enforced by the scheduler), a commence-time window
// (only events starting within the window and not yet started), and a hard
// per-cycle event cap. Persistence reuses IngestionService.persistSnapshots
// so props share the dedup, cache-invalidation, and publish path.
type PropIngestionService struct {
	client         *oddsapi.Client
	ingestion      *IngestionService
	gameRepo       repository.GameRepository
	sbRepo         repository.SportsbookRepository
	rawRepo        repository.RawResponseRepository
	commenceWindow time.Duration
	maxEvents      int
}

// NewPropIngestionService creates a prop ingestion service. commenceWindow
// <= 0 defaults to 48h; maxEvents <= 0 defaults to 10.
func NewPropIngestionService(
	client *oddsapi.Client,
	ingestion *IngestionService,
	gameRepo repository.GameRepository,
	sbRepo repository.SportsbookRepository,
	rawRepo repository.RawResponseRepository,
	commenceWindow time.Duration,
	maxEvents int,
) *PropIngestionService {
	if commenceWindow <= 0 {
		commenceWindow = defaultPropCommenceWindow
	}
	if maxEvents <= 0 {
		maxEvents = defaultPropMaxEventsPerCycle
	}
	return &PropIngestionService{
		client:         client,
		ingestion:      ingestion,
		gameRepo:       gameRepo,
		sbRepo:         sbRepo,
		rawRepo:        rawRepo,
		commenceWindow: commenceWindow,
		maxEvents:      maxEvents,
	}
}

// IngestProps runs one prop ingestion cycle for a sport: list events, filter
// to the commence window, cap the per-cycle spend, fetch per-event prop odds,
// normalize, and persist through the shared write path (one publish per
// cycle).
func (s *PropIngestionService) IngestProps(ctx context.Context, sportKey string) (*PropIngestionResult, error) {
	ctx, span := propTracer.Start(ctx, "ingestion.IngestProps")
	defer span.End()
	span.SetAttributes(attribute.String("sport_key", sportKey))

	start := time.Now()
	result := &PropIngestionResult{Sport: sportKey}

	markets, ok := PropMarketsBySport[sportKey]
	if !ok {
		slog.Warn("no prop markets configured for sport; skipping", "sport", sportKey)
		result.DurationMs = time.Since(start).Milliseconds()
		return result, nil
	}

	slog.Info("starting prop ingestion cycle", "sport", sportKey, "markets", markets)

	events, err := s.client.GetEvents(ctx, sportKey)
	if err != nil {
		return nil, fmt.Errorf("fetch events for %s: %w", sportKey, err)
	}
	result.EventsListed = len(events.Events)

	selected, capped := filterPropEvents(events.Events, time.Now().UTC(), s.commenceWindow, s.maxEvents)
	result.EventsInScope = len(selected) + capped
	result.EventsCapped = capped
	if capped > 0 {
		slog.Warn("prop event cap reached; truncating cycle",
			"sport", sportKey,
			"in_window", len(selected)+capped,
			"cap", s.maxEvents,
			"dropped", capped,
		)
	}

	if len(selected) == 0 {
		slog.Info("no upcoming events in prop commence window", "sport", sportKey, "window", s.commenceWindow.String())
		result.DurationMs = time.Since(start).Milliseconds()
		return result, nil
	}

	// Build sportsbook key -> ID map once per cycle.
	sportsbooks, err := s.sbRepo.GetAll(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch sportsbooks: %w", err)
	}
	sbMap := make(map[string]string, len(sportsbooks))
	for _, sb := range sportsbooks {
		sbMap[sb.Key] = sb.ID
	}

	// Record game metadata up front so side derivation and the closing-line
	// sweep can resolve prop rows even if the odds fetch below fails.
	games := make([]model.Game, 0, len(selected))
	league := oddsapi.SportKeyToLeague[sportKey]
	for _, ev := range selected {
		games = append(games, model.Game{
			GameExternalID: ev.ID,
			League:         league,
			HomeTeam:       ev.HomeTeam,
			AwayTeam:       ev.AwayTeam,
			CommenceTime:   ev.CommenceTime,
		})
	}
	if err := s.gameRepo.UpsertGames(ctx, games); err != nil {
		return nil, fmt.Errorf("upsert games: %w", err)
	}

	capturedAt := time.Now().UTC()
	var snapshots []model.LineSnapshot
	for _, ev := range selected {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		odds, err := s.client.GetEventOdds(ctx, sportKey, ev.ID, markets)
		if odds != nil {
			s.archiveRaw(sportKey, ev.ID, odds)
		}
		if err != nil {
			// One bad event must not sink the cycle (or waste the requests
			// already spent on the others).
			slog.Warn("failed to fetch event prop odds", "sport", sportKey, "event_id", ev.ID, "error", err)
			continue
		}
		result.EventsFetched++

		snapshots = append(snapshots, oddsapi.NormalizeEventProps(odds.Event, sbMap, capturedAt)...)
	}

	if len(snapshots) == 0 {
		slog.Info("no prop lines to ingest", "sport", sportKey, "events_fetched", result.EventsFetched)
		result.DurationMs = time.Since(start).Milliseconds()
		return result, nil
	}

	inserted, skipped, err := s.ingestion.persistSnapshots(ctx, snapshots, sbMap)
	if err != nil {
		return nil, err
	}
	result.LinesIngested = inserted
	result.LinesSkipped = skipped
	result.DurationMs = time.Since(start).Milliseconds()

	slog.Info("prop ingestion cycle complete",
		"sport", sportKey,
		"events_listed", result.EventsListed,
		"events_fetched", result.EventsFetched,
		"events_capped", result.EventsCapped,
		"lines_inserted", inserted,
		"lines_skipped", skipped,
		"duration_ms", result.DurationMs,
	)

	span.SetAttributes(
		attribute.Int("props.events_fetched", result.EventsFetched),
		attribute.Int("props.events_capped", result.EventsCapped),
		attribute.Int("lines.inserted", inserted),
		attribute.Int("lines.skipped", skipped),
	)

	return result, nil
}

// archiveRaw persists the raw per-event odds response asynchronously,
// mirroring the bulk ingestion path.
func (s *PropIngestionService) archiveRaw(sportKey, eventID string, odds *oddsapi.EventOddsResult) {
	rawBody := string(odds.RawBody)
	status := odds.HTTPStatus
	go func() {
		archiveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.rawRepo.Insert(archiveCtx, model.RawAPIResponse{
			Service:      "lines-service",
			Source:       "the_odds_api",
			Endpoint:     fmt.Sprintf("/v4/sports/%s/events/%s/odds", sportKey, eventID),
			HTTPStatus:   status,
			ResponseBody: rawBody,
			CapturedAt:   time.Now().UTC(),
		}); err != nil {
			slog.Warn("failed to archive raw prop response", "event_id", eventID, "error", err)
		}
	}()
}

// filterPropEvents keeps events that have not started and commence within
// the window, preserving API order (soonest first), then applies the
// per-cycle cap. It returns the selected events and how many in-window
// events the cap dropped.
func filterPropEvents(events []oddsapi.EventStub, now time.Time, window time.Duration, maxEvents int) (selected []oddsapi.EventStub, capped int) {
	inWindow := make([]oddsapi.EventStub, 0, len(events))
	for _, ev := range events {
		if !ev.CommenceTime.After(now) {
			continue // already started (or starting this instant)
		}
		if ev.CommenceTime.After(now.Add(window)) {
			continue // too far out; a later cycle will catch it
		}
		inWindow = append(inWindow, ev)
	}
	if maxEvents > 0 && len(inWindow) > maxEvents {
		return inWindow[:maxEvents], len(inWindow) - maxEvents
	}
	return inWindow, 0
}
