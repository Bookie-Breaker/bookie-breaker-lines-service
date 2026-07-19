SET search_path TO lines, public;

-- ADR-029: structured prop columns. Player props store their identity in
-- dedicated columns (player_external_id, stat_type, prop_type) so downstream
-- consumers can join and filter without parsing selection text. The selection
-- string still disambiguates row uniqueness (uq_line_snapshots_composite and
-- uq_closing_lines_composite are unchanged); these columns are additive
-- metadata and remain NULL for non-prop markets.
--
-- lines.line_snapshots is a TimescaleDB hypertable with compression enabled
-- (see migration 002). Adding NULLable columns without defaults is safe on
-- compressed hypertables; compression settings (compress_segmentby /
-- compress_orderby) are intentionally left untouched.

ALTER TABLE lines.line_snapshots
    ADD COLUMN player_external_id TEXT,
    ADD COLUMN stat_type TEXT,
    ADD COLUMN prop_type TEXT;

COMMENT ON COLUMN lines.line_snapshots.player_external_id IS 'External player identifier for PLAYER_PROP markets. NULL for non-player markets (ADR-029).';
COMMENT ON COLUMN lines.line_snapshots.stat_type IS 'Normalized stat category for prop markets (e.g., "SHOTS", "GOALS"). NULL for non-prop markets (ADR-029).';
COMMENT ON COLUMN lines.line_snapshots.prop_type IS 'Prop structure (e.g., "OVER_UNDER", "YES_NO"). NULL for non-prop markets (ADR-029).';

ALTER TABLE lines.closing_lines
    ADD COLUMN player_external_id TEXT,
    ADD COLUMN stat_type TEXT,
    ADD COLUMN prop_type TEXT;

COMMENT ON COLUMN lines.closing_lines.player_external_id IS 'External player identifier for PLAYER_PROP markets. NULL for non-player markets (ADR-029).';
COMMENT ON COLUMN lines.closing_lines.stat_type IS 'Normalized stat category for prop markets. NULL for non-prop markets (ADR-029).';
COMMENT ON COLUMN lines.closing_lines.prop_type IS 'Prop structure. NULL for non-prop markets (ADR-029).';

CREATE INDEX idx_line_snapshots_player
    ON lines.line_snapshots (game_external_id, stat_type, player_external_id, captured_at DESC)
    WHERE market_type = 'PLAYER_PROP';
