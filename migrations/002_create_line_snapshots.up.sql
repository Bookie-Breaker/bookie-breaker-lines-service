SET search_path TO lines;

CREATE TABLE lines.line_snapshots (
    id               UUID NOT NULL DEFAULT gen_random_uuid(),
    game_external_id TEXT NOT NULL,
    sportsbook_id    UUID NOT NULL REFERENCES lines.sportsbooks(id),
    league           league_enum NOT NULL,
    market_type      market_type_enum NOT NULL,
    selection        TEXT NOT NULL,
    line_value       DECIMAL(8,2),
    odds_american    INTEGER NOT NULL,
    odds_decimal     DECIMAL(8,4) NOT NULL,
    is_live          BOOLEAN NOT NULL DEFAULT FALSE,
    captured_at      TIMESTAMPTZ NOT NULL,
    source           TEXT NOT NULL,

    PRIMARY KEY (id, captured_at)
);

COMMENT ON TABLE lines.line_snapshots IS 'Immutable line snapshots. TimescaleDB hypertable partitioned by captured_at. ~5-10M rows/year.';
COMMENT ON COLUMN lines.line_snapshots.game_external_id IS 'External game identifier from the odds API.';
COMMENT ON COLUMN lines.line_snapshots.selection IS 'Human-readable selection (e.g., "KC -3.5", "Over 47.5").';
COMMENT ON COLUMN lines.line_snapshots.line_value IS 'Spread, total, or prop number. NULL for moneylines.';
COMMENT ON COLUMN lines.line_snapshots.odds_american IS 'American odds (e.g., -110, +150). Canonical format.';
COMMENT ON COLUMN lines.line_snapshots.odds_decimal IS 'Decimal odds (e.g., 1.91, 2.50). Denormalized for convenience.';
COMMENT ON COLUMN lines.line_snapshots.source IS 'Which API provided this line (e.g., "the_odds_api").';

SELECT create_hypertable(
    'lines.line_snapshots',
    by_range('captured_at', INTERVAL '1 day')
);

CREATE UNIQUE INDEX uq_line_snapshots_composite
    ON lines.line_snapshots (game_external_id, sportsbook_id, market_type, selection, captured_at);

CREATE INDEX idx_line_snapshots_game_market
    ON lines.line_snapshots (game_external_id, market_type, sportsbook_id, captured_at DESC);

CREATE INDEX idx_line_snapshots_league_time
    ON lines.line_snapshots (league, captured_at DESC);

CREATE INDEX idx_line_snapshots_sportsbook
    ON lines.line_snapshots (sportsbook_id, market_type, captured_at DESC);

CREATE INDEX idx_line_snapshots_live
    ON lines.line_snapshots (game_external_id, captured_at DESC)
    WHERE is_live = TRUE;

ALTER TABLE lines.line_snapshots SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'game_external_id, sportsbook_id',
    timescaledb.compress_orderby = 'captured_at DESC'
);

SELECT add_compression_policy('lines.line_snapshots', INTERVAL '7 days');

SELECT add_retention_policy('lines.line_snapshots', INTERVAL '18 months');
