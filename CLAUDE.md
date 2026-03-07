# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Go server that ingests GPS location reports from transit vehicle drivers' Android phones and serves standard GTFS-RT Vehicle Positions protobuf feeds over HTTP. Part of the OneBusAway ecosystem, targeting transit agencies in developing countries that lack AVL hardware.

## Build & Run Commands

- **Build:** `go build ./...`
- **Vet:** `go vet ./...`
- **Run all tests:** `go test ./...`
- **Run a single test:** `go test -run TestName ./...`
- **Run with Docker:** `docker compose up` (starts PostgreSQL + server)
- **CI runs:** `go vet ./...` then `go test ./...`

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP listen port |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable` | PostgreSQL connection string |
| `STALENESS_THRESHOLD` | `5m` | Go duration; vehicles older than this are excluded from the GTFS-RT feed |
| `READ_TIMEOUT` / `WRITE_TIMEOUT` / `IDLE_TIMEOUT` | `15s` / `15s` / `60s` | HTTP server timeouts |

## Architecture

All Go source files are in the root package `main` — there are no sub-packages.

**Core flow:** Android app → `POST /api/v1/locations` → handler validates & persists to PostgreSQL via `Store`, then updates in-memory `Tracker` → `GET /gtfs-rt/vehicle-positions` reads from `Tracker` and serializes a GTFS-RT protobuf `FeedMessage`.

### Key types and files

- **`handlers.go`** — HTTP handlers, `LocationReport` struct (JSON payload), `LocationSaver` interface, `buildFeed()` for GTFS-RT protobuf construction, `writeJSON()` helper
- **`tracker.go`** — `Tracker` (thread-safe in-memory map of `VehicleState`), keyed by vehicle ID; filters stale entries via `maxAge`
- **`store.go`** — `Store` wraps a `pgxpool.Pool`; auto-creates `vehicles` and `location_points` tables on startup; `SaveLocation` upserts vehicle + inserts point in a single transaction
- **`main.go`** — wiring: env config, store init, tracker seeding from DB, route registration, graceful shutdown

### Database

PostgreSQL with two tables: `vehicles` (id, label, timestamps) and `location_points` (vehicle_id FK, GPS fields, received_at). Schema is auto-migrated via `CREATE TABLE IF NOT EXISTS` in `NewStore`.

On startup, the tracker is seeded with recent locations from the DB so the feed is immediately populated.

### Testing

- `tracker_test.go` and `handlers_test.go` are pure unit tests (no DB required). Handlers use a `mockStore` implementing `LocationSaver`.
- `store_test.go` requires a running PostgreSQL instance; tests are skipped if `DATABASE_URL` is not set.
- Tests use `testify` (`require` + `assert`).

### API Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/api/v1/locations` | POST | Submit a vehicle location report |
| `/gtfs-rt/vehicle-positions` | GET | GTFS-RT feed (protobuf default, `?format=json` for debug) |
| `/health` | GET | Health check |
