SET search_path TO lines;

CREATE TABLE lines.sportsbooks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    key         TEXT NOT NULL UNIQUE,
    is_sharp    BOOLEAN NOT NULL DEFAULT FALSE,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE lines.sportsbooks IS 'Canonical sportsbook registry. ~50 rows, near-static.';
COMMENT ON COLUMN lines.sportsbooks.key IS 'Unique slug used in external APIs (e.g., "draftkings", "pinnacle").';
COMMENT ON COLUMN lines.sportsbooks.is_sharp IS 'Market-making books (Pinnacle, Circa) whose lines are considered efficient.';

CREATE INDEX idx_sportsbooks_active ON lines.sportsbooks (is_active) WHERE is_active = TRUE;
