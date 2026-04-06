# bookie-breaker-lines-service

## Service Purpose

Go REST API that ingests and serves betting lines from external APIs (The Odds API, SharpAPI). Tracks line movement history, identifies opening/closing lines, and publishes line change events.

## Language & Conventions

- **Language:** Go 1.22
- **Framework:** Echo
- **Project layout:** `cmd/server/main.go` entry point, `internal/` for private code, `pkg/` for public libraries
- **Naming:** `snake_case.go` files, `camelCase` variables, `PascalCase` exports
- **Testing:** `*_test.go` co-located, `tests/integration/` for testcontainers

## Key Files

- `cmd/server/main.go` — HTTP server entry point
- `internal/handler/` — HTTP route handlers
- `internal/service/` — Business logic
- `internal/repository/` — Database access (TimescaleDB)
- `.config/mise.toml` — Tool versions
- `.config/lefthook.yml` — Git hooks

## Service-Specific Commands

```bash
task dev          # Run with air hot reload
task lint         # golangci-lint
task test         # go test -race ./...
task build        # Build to bin/server
```

## Dependencies

- **PostgreSQL** (lines schema) — TimescaleDB hypertable for line snapshots
- **Redis** — Caching and pub/sub (`events:lines.updated`)
- **The Odds API** — External line data source
- No upstream service dependencies

## Environment Variables

See `.env.example`. Key: `ODDS_API_KEY`, `DATABASE_URL`, `REDIS_URL`, `PORT=8001`.
