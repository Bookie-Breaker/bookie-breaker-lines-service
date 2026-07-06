-- Minimal replica of the infra-ops init-db scripts needed by the lines-service
-- migrations and repositories (see bookie-breaker-infra-ops/init-db/).
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TYPE league_enum AS ENUM (
    'NFL', 'NBA', 'MLB', 'NCAA_FB', 'NCAA_BB', 'NCAA_BSB',
    'FIFA_WC', 'EPL', 'NHL', 'NCAA_HKY'
);

CREATE TYPE market_type_enum AS ENUM (
    'SPREAD', 'TOTAL', 'MONEYLINE', 'PLAYER_PROP', 'TEAM_PROP', 'GAME_PROP', 'FUTURE', 'LIVE'
);

CREATE TYPE sport_enum AS ENUM (
    'FOOTBALL', 'BASKETBALL', 'BASEBALL', 'SOCCER', 'HOCKEY'
);

CREATE TYPE bet_result_enum AS ENUM (
    'OPEN', 'WON', 'LOST', 'PUSH', 'VOID'
);

CREATE SCHEMA lines;

CREATE TABLE public.raw_api_responses (
    id            UUID NOT NULL DEFAULT gen_random_uuid(),
    service       TEXT NOT NULL,
    source        TEXT NOT NULL,
    endpoint      TEXT NOT NULL,
    http_status   INTEGER NOT NULL,
    request_body  TEXT,
    response_body TEXT NOT NULL,
    captured_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, captured_at)
);

SELECT create_hypertable(
    'public.raw_api_responses',
    by_range('captured_at', INTERVAL '1 day')
);
