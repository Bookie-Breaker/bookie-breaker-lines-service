SET search_path TO lines, public;

CREATE TABLE lines.games (
    game_external_id    TEXT PRIMARY KEY,
    league              public.league_enum NOT NULL,
    home_team           TEXT NOT NULL,
    away_team           TEXT NOT NULL,
    commence_time       TIMESTAMPTZ NOT NULL,
    closing_captured_at TIMESTAMPTZ,
    first_seen_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE lines.games IS 'Ingestion metadata only (commence time, teams) keyed by external id. Canonical game entities are owned by statistics-service.';
COMMENT ON COLUMN lines.games.closing_captured_at IS 'When closing lines were materialized for this game. NULL means the closing sweep has not run yet.';

CREATE INDEX idx_games_closing_due ON lines.games (commence_time)
    WHERE closing_captured_at IS NULL;
