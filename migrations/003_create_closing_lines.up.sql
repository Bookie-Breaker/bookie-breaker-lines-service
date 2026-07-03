SET search_path TO lines;

CREATE TABLE lines.closing_lines (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    game_external_id TEXT NOT NULL,
    sportsbook_id    UUID NOT NULL REFERENCES lines.sportsbooks(id),
    league           league_enum NOT NULL,
    market_type      market_type_enum NOT NULL,
    selection        TEXT NOT NULL,
    line_value       DECIMAL(8,2),
    odds_american    INTEGER NOT NULL,
    odds_decimal     DECIMAL(8,4) NOT NULL,
    captured_at      TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_closing_lines_composite
        UNIQUE (game_external_id, sportsbook_id, market_type, selection)
);

COMMENT ON TABLE lines.closing_lines IS 'Materialized closing lines for CLV calculations. ~100K rows/year.';

CREATE INDEX idx_closing_lines_game
    ON lines.closing_lines (game_external_id, market_type, sportsbook_id);

CREATE INDEX idx_closing_lines_league
    ON lines.closing_lines (league, created_at DESC);
