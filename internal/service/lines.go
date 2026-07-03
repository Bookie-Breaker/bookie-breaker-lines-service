package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/adapter/oddsapi"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-lines-service/internal/repository"
)

// ErrGameNotFound is returned when a game id is unknown to the service.
var ErrGameNotFound = errors.New("game not found")

// ErrSnapshotNotFound is returned when a line snapshot id does not exist.
var ErrSnapshotNotFound = errors.New("line snapshot not found")

// LineQueryService serves the read API: it enriches persisted snapshots with
// derived fields (implied probability, side, opening/closing flags) that are
// never stored.
type LineQueryService struct {
	lineRepo  repository.LineRepository
	gameRepo  repository.GameRepository
	sbRepo    repository.SportsbookRepository
	lineCache *cache.LineCache
}

// NewLineQueryService creates a new line query service.
func NewLineQueryService(
	lineRepo repository.LineRepository,
	gameRepo repository.GameRepository,
	sbRepo repository.SportsbookRepository,
	lineCache *cache.LineCache,
) *LineQueryService {
	return &LineQueryService{
		lineRepo:  lineRepo,
		gameRepo:  gameRepo,
		sbRepo:    sbRepo,
		lineCache: lineCache,
	}
}

// CurrentLines returns the latest snapshot per line key across games.
func (s *LineQueryService) CurrentLines(ctx context.Context, filters repository.CurrentLineFilters) ([]model.LineSnapshot, bool, string, error) {
	lines, hasMore, err := s.lineRepo.GetCurrentLines(ctx, filters)
	if err != nil {
		return nil, false, "", err
	}
	if err := s.enrich(ctx, lines); err != nil {
		return nil, false, "", err
	}
	return lines, hasMore, nextCursor(lines, hasMore), nil
}

// GameLines returns the latest lines for a single game. The default query
// (no filters, no cursor) is served from cache when possible.
func (s *LineQueryService) GameLines(ctx context.Context, gameID string, filters repository.CurrentLineFilters, side string) ([]model.LineSnapshot, bool, string, error) {
	game, err := s.getGame(ctx, gameID)
	if err != nil {
		return nil, false, "", err
	}

	cacheable := side == "" && filters.Cursor == "" && len(filters.MarketTypes) == 0 && len(filters.Sportsbooks) == 0

	if cacheable {
		cached, cacheErr := s.lineCache.GetCurrentLines(ctx, gameID)
		if cacheErr != nil {
			slog.Warn("line cache read failed", "game_id", gameID, "error", cacheErr)
		}
		if len(cached) > 0 {
			return paginateCached(cached, filters.Limit)
		}
	}

	lines, hasMore, err := s.lineRepo.GetGameLines(ctx, gameID, filters)
	if err != nil {
		return nil, false, "", err
	}
	s.enrichForGame(lines, game)

	if cacheable && !hasMore {
		if cacheErr := s.lineCache.SetCurrentLines(ctx, gameID, lines); cacheErr != nil {
			slog.Warn("line cache write failed", "game_id", gameID, "error", cacheErr)
		}
	}

	if side != "" {
		lines = filterBySide(lines, side)
	}

	return lines, hasMore, nextCursor(lines, hasMore), nil
}

// Snapshot returns a single line snapshot with derived fields populated.
func (s *LineQueryService) Snapshot(ctx context.Context, id string) (*model.LineSnapshot, error) {
	snap, err := s.lineRepo.GetLineByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSnapshotNotFound
		}
		return nil, err
	}

	game, err := s.gameRepo.GetGame(ctx, snap.GameExternalID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	snap.ImpliedProb = oddsapi.ImpliedProbability(snap.OddsDecimal)
	snap.Side = deriveSide(*snap, game)

	// Opening/closing flags: compare against the key's history and any
	// materialized closing line.
	history, err := s.lineRepo.GetLineMovement(ctx, snap.GameExternalID, repository.MovementFilters{
		Sportsbook: snap.SportsbookKey,
		MarketType: string(snap.MarketType),
		Selection:  snap.Selection,
	})
	if err != nil {
		return nil, err
	}
	if len(history) > 0 {
		snap.IsOpening = history[0].ID == snap.ID
	}

	closing, err := s.lineRepo.GetClosingLines(ctx, snap.GameExternalID, repository.ClosingLineFilters{
		Sportsbook: snap.SportsbookKey,
		MarketType: string(snap.MarketType),
	})
	if err != nil {
		return nil, err
	}
	for _, cl := range closing {
		if cl.Selection == snap.Selection && cl.CapturedAt.Equal(snap.CapturedAt) {
			snap.IsClosing = true
			break
		}
	}

	return snap, nil
}

// Movement returns the aggregated line movement per sportsbook and selection
// for one market of a game, chronological snapshots included.
func (s *LineQueryService) Movement(ctx context.Context, gameID string, filters repository.MovementFilters) ([]model.LineMovement, error) {
	if _, err := s.getGame(ctx, gameID); err != nil {
		return nil, err
	}

	snapshots, err := s.lineRepo.GetLineMovement(ctx, gameID, filters)
	if err != nil {
		return nil, err
	}

	closing, err := s.lineRepo.GetClosingLines(ctx, gameID, repository.ClosingLineFilters{
		Sportsbook: filters.Sportsbook,
		MarketType: filters.MarketType,
	})
	if err != nil {
		return nil, err
	}
	closingByKey := make(map[string]model.ClosingLine, len(closing))
	for _, cl := range closing {
		closingByKey[cl.SportsbookID+"|"+cl.Selection] = cl
	}

	// Group chronological snapshots per (sportsbook, selection), preserving
	// first-seen order of groups.
	type group struct {
		first model.LineSnapshot
		last  model.LineSnapshot
		snaps []model.MovementSnapshot
	}
	var order []string
	groups := make(map[string]*group)

	for _, snap := range snapshots {
		key := snap.SportsbookID + "|" + snap.Selection
		g, ok := groups[key]
		if !ok {
			g = &group{first: snap}
			groups[key] = g
			order = append(order, key)
		}
		g.last = snap
		g.snaps = append(g.snaps, model.MovementSnapshot{
			LineValue:    snap.LineValue,
			OddsAmerican: snap.OddsAmerican,
			Timestamp:    snap.CapturedAt,
			IsOpening:    len(g.snaps) == 0,
		})
	}

	movements := make([]model.LineMovement, 0, len(order))
	for _, key := range order {
		g := groups[key]
		openingOdds := g.first.OddsAmerican

		m := model.LineMovement{
			GameID:        gameID,
			SportsbookID:  g.first.SportsbookID,
			SportsbookKey: g.first.SportsbookKey,
			MarketType:    g.first.MarketType,
			Selection:     g.first.Selection,
			OpeningLine:   g.first.LineValue,
			OpeningOdds:   &openingOdds,
			CurrentLine:   g.last.LineValue,
			CurrentOdds:   g.last.OddsAmerican,
			Snapshots:     g.snaps,
		}

		if cl, ok := closingByKey[key]; ok {
			closingOdds := cl.OddsAmerican
			m.ClosingLine = cl.LineValue
			m.ClosingOdds = &closingOdds
		}

		if g.first.LineValue != nil && g.last.LineValue != nil {
			delta := *g.last.LineValue - *g.first.LineValue
			m.TotalMovement = &delta

			// Approximation: a line normally moves inversely to its odds
			// (points given up are paid back in price). Line and odds moving
			// in the same direction suggests reverse movement. True RLM
			// detection requires public betting percentages, not ingested.
			oddsDelta := g.last.OddsAmerican - g.first.OddsAmerican
			m.IsReverseMovement = delta != 0 && oddsDelta != 0 && (delta > 0) == (oddsDelta > 0)
		} else {
			oddsDelta := float64(g.last.OddsAmerican - g.first.OddsAmerican)
			if oddsDelta != 0 {
				m.TotalMovement = &oddsDelta
			}
		}

		movements = append(movements, m)
	}

	return movements, nil
}

// Best returns the most favorable current price per market and side across
// all sportsbooks for a game.
func (s *LineQueryService) Best(ctx context.Context, gameID, marketType, selection string) ([]model.BestLine, error) {
	game, err := s.getGame(ctx, gameID)
	if err != nil {
		return nil, err
	}

	filters := repository.CurrentLineFilters{Limit: 200}
	if marketType != "" {
		filters.MarketTypes = []string{marketType}
	}
	lines, _, err := s.lineRepo.GetGameLines(ctx, gameID, filters)
	if err != nil {
		return nil, err
	}
	s.enrichForGame(lines, game)

	if selection != "" {
		if isSideKeyword(selection) {
			lines = filterBySide(lines, selection)
		} else {
			var matched []model.LineSnapshot
			for _, l := range lines {
				if l.Selection == selection {
					matched = append(matched, l)
				}
			}
			lines = matched
		}
	}

	sbNames, err := s.sportsbookNames(ctx)
	if err != nil {
		return nil, err
	}

	// Pick the highest decimal odds per (market, side); selection is the
	// grouping fallback when a side cannot be derived.
	var order []string
	best := make(map[string]model.LineSnapshot)
	for _, l := range lines {
		groupKey := string(l.MarketType) + "|" + l.Side
		if l.Side == "" {
			groupKey = string(l.MarketType) + "|" + l.Selection
		}
		cur, ok := best[groupKey]
		if !ok {
			best[groupKey] = l
			order = append(order, groupKey)
			continue
		}
		if l.OddsDecimal > cur.OddsDecimal {
			best[groupKey] = l
		}
	}

	result := make([]model.BestLine, 0, len(order))
	for _, key := range order {
		l := best[key]
		result = append(result, model.BestLine{
			MarketType:       l.MarketType,
			Selection:        l.Selection,
			Side:             l.Side,
			LineValue:        l.LineValue,
			BestOddsAmerican: l.OddsAmerican,
			BestOddsDecimal:  l.OddsDecimal,
			ImpliedProb:      l.ImpliedProb,
			SportsbookID:     l.SportsbookID,
			SportsbookKey:    l.SportsbookKey,
			SportsbookName:   sbNames[l.SportsbookID],
			Timestamp:        l.CapturedAt,
			LineID:           l.ID,
		})
	}

	return result, nil
}

// Closing returns the materialized closing lines for a game.
func (s *LineQueryService) Closing(ctx context.Context, gameID string, filters repository.ClosingLineFilters) ([]model.ClosingLine, error) {
	if _, err := s.getGame(ctx, gameID); err != nil {
		return nil, err
	}
	return s.lineRepo.GetClosingLines(ctx, gameID, filters)
}

func (s *LineQueryService) getGame(ctx context.Context, gameID string) (*model.Game, error) {
	game, err := s.gameRepo.GetGame(ctx, gameID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameNotFound
		}
		return nil, fmt.Errorf("lookup game %q: %w", gameID, err)
	}
	return game, nil
}

// enrich populates derived fields on a mixed-game result set.
func (s *LineQueryService) enrich(ctx context.Context, lines []model.LineSnapshot) error {
	if len(lines) == 0 {
		return nil
	}

	gameIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, l := range lines {
		if _, ok := seen[l.GameExternalID]; !ok {
			seen[l.GameExternalID] = struct{}{}
			gameIDs = append(gameIDs, l.GameExternalID)
		}
	}

	games, err := s.gameRepo.GetGames(ctx, gameIDs)
	if err != nil {
		return err
	}

	for i := range lines {
		lines[i].ImpliedProb = oddsapi.ImpliedProbability(lines[i].OddsDecimal)
		if g, ok := games[lines[i].GameExternalID]; ok {
			lines[i].Side = deriveSide(lines[i], &g)
		} else {
			lines[i].Side = deriveSide(lines[i], nil)
		}
	}
	return nil
}

// enrichForGame populates derived fields when all lines belong to one game.
func (s *LineQueryService) enrichForGame(lines []model.LineSnapshot, game *model.Game) {
	for i := range lines {
		lines[i].ImpliedProb = oddsapi.ImpliedProbability(lines[i].OddsDecimal)
		lines[i].Side = deriveSide(lines[i], game)
	}
}

func (s *LineQueryService) sportsbookNames(ctx context.Context) (map[string]string, error) {
	books, err := s.sbRepo.GetAll(ctx, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch sportsbooks: %w", err)
	}
	names := make(map[string]string, len(books))
	for _, b := range books {
		names[b.ID] = b.Name
	}
	return names, nil
}

// deriveSide maps a selection onto the contract's side enum. Totals derive
// from the selection prefix; spreads and moneylines compare the selection's
// team against the game's home/away teams.
func deriveSide(snap model.LineSnapshot, game *model.Game) string {
	switch snap.MarketType {
	case model.MarketTotal:
		switch {
		case strings.HasPrefix(snap.Selection, "Over"):
			return "OVER"
		case strings.HasPrefix(snap.Selection, "Under"):
			return "UNDER"
		}
		return ""
	case model.MarketSpread, model.MarketMoneyline:
		if game == nil {
			return ""
		}
		switch {
		case strings.HasPrefix(snap.Selection, game.HomeTeam):
			return "HOME"
		case strings.HasPrefix(snap.Selection, game.AwayTeam):
			return "AWAY"
		}
		return ""
	default:
		return ""
	}
}

func isSideKeyword(v string) bool {
	switch strings.ToUpper(v) {
	case "HOME", "AWAY", "OVER", "UNDER", "YES", "NO":
		return true
	}
	return false
}

func filterBySide(lines []model.LineSnapshot, side string) []model.LineSnapshot {
	want := strings.ToUpper(side)
	filtered := make([]model.LineSnapshot, 0, len(lines))
	for _, l := range lines {
		if l.Side == want {
			filtered = append(filtered, l)
		}
	}
	return filtered
}

func nextCursor(lines []model.LineSnapshot, hasMore bool) string {
	if !hasMore || len(lines) == 0 {
		return ""
	}
	last := lines[len(lines)-1]
	return repository.EncodeCursor(repository.LineKey{
		GameExternalID: last.GameExternalID,
		SportsbookID:   last.SportsbookID,
		MarketType:     last.MarketType,
		Selection:      last.Selection,
	})
}

func paginateCached(cached []model.LineSnapshot, limit int) ([]model.LineSnapshot, bool, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if len(cached) <= limit {
		return cached, false, "", nil
	}
	page := cached[:limit]
	return page, true, nextCursor(page, true), nil
}
