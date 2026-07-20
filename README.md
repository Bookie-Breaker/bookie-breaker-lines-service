# bookie-breaker-lines-service

[![CI](https://img.shields.io/github/actions/workflow/status/Bookie-Breaker/bookie-breaker-lines-service/ci.yml?branch=main&label=CI&logo=githubactions&logoColor=white)](https://github.com/Bookie-Breaker/bookie-breaker-lines-service/actions/workflows/ci.yml)
[![coverage](https://img.shields.io/codecov/c/github/Bookie-Breaker/bookie-breaker-lines-service?logo=codecov&logoColor=white)](https://app.codecov.io/gh/Bookie-Breaker/bookie-breaker-lines-service)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Echo](https://img.shields.io/badge/Echo-v4-1D9BF0)
![TimescaleDB](https://img.shields.io/badge/TimescaleDB-PG16-FDB515?logo=timescale&logoColor=black)
![Redis](https://img.shields.io/badge/Redis-7-DC382D?logo=redis&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)

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
