# bookie-breaker-lines-service

Ingests and serves betting lines from external APIs (The Odds API, SharpAPI) with real-time line tracking and movement history.

## Quickstart

### With Docker Compose (recommended)
task up  # from BookieBreaker/ root

### Standalone
cp .env.example .env  # fill in values
task bootstrap
task dev

## API

[API documentation to be added]

## Architecture Decisions

- [Tech Stack Selection (ADR-010)](https://github.com/Bookie-Breaker/bookie-breaker-docs/blob/main/decisions/010-tech-stack-selection.md)

## Environment Variables

See `.env.example` for all variables with descriptions.
