SET search_path TO lines, public;

DROP INDEX IF EXISTS lines.idx_line_snapshots_player;

ALTER TABLE lines.line_snapshots
    DROP COLUMN IF EXISTS player_external_id,
    DROP COLUMN IF EXISTS stat_type,
    DROP COLUMN IF EXISTS prop_type;

ALTER TABLE lines.closing_lines
    DROP COLUMN IF EXISTS player_external_id,
    DROP COLUMN IF EXISTS stat_type,
    DROP COLUMN IF EXISTS prop_type;
