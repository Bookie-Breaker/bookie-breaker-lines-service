# bookie-breaker-lines-service

Ingests and serves betting lines from The Odds API and a SharpAPI live SSE feed, tracking movement history and
opening/closing lines in a TimescaleDB hypertable. Normalizes player-prop lines across sportsbooks and publishes
`events:lines.updated` when line changes are detected. Without `ODDS_API_KEY` the ingestion scheduler is disabled,
but the read API still serves seeded data.

Operational runbooks live in the
[daily operations playbook](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/playbooks/02-daily-operations.md).

## Quickstart

### With Docker Compose (recommended)

```bash
task up # from BookieBreaker/ root
```

### Standalone

```bash
cp .env.example .env # fill in values
task bootstrap
task dev
```

## API

The service listens on port 8001 with base path `/api/v1/lines`; health is at `/api/v1/lines/health`.
Interactive docs are not served — see the
[lines-service API contract](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/api-contracts/lines-service-api.md)
for the full endpoint reference.

## Architecture Decisions

- [Lines Data Sources (ADR-007)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/007-lines-data-sources.md)
- [Tech Stack Selection (ADR-010)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/010-tech-stack-selection.md)
- [Prop Line Representation (ADR-029)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/029-prop-line-representation.md)
- [Live Ingestion Transport (ADR-031)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/031-live-ingestion-transport.md)

## Environment Variables

See `.env.example` for all variables with descriptions.
