# Development Guide

This guide is for contributors who want to run, verify, and iterate on the current Go server in this repository.

## Scope

The current implementation focuses on:

- ingesting location reports (`POST /api/v1/locations`)
- serving GTFS-RT vehicle positions (`GET /gtfs-rt/vehicle-positions`)
- exposing basic server status (`GET /api/v1/admin/status`)

## Prerequisites

- Go (matching `go.mod` toolchain)
- Docker + Docker Compose
- `curl`

## Quick Start (Docker)

From the repository root:

1. Start the stack:

   ```bash
   make up
   ```

2. Verify server health:

   ```bash
   curl http://localhost:8080/health
   ```

3. Run a smoke test (posts one location, then fetches status + feed JSON):

   ```bash
   make smoke
   ```

4. Stop the stack when done:

   ```bash
   make down
   ```

## Local Server Run (without Docker server container)

You can run Postgres in Docker and run the Go server directly:

1. Start only database:

   ```bash
   docker compose up -d db
   ```

2. Export environment variables:

   ```bash
   export PORT=8080
   export DATABASE_URL='postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable'
   export STALENESS_THRESHOLD=5m
   ```

3. Run server:

   ```bash
   make run
   ```

Migrations are applied automatically on server startup.

## Running Tests

Run all tests:

```bash
make test
```

Notes:

- most tests are unit tests and run without external services
- DB integration tests in `store_test.go` require `DATABASE_URL` and are skipped when it is not set

## Simulating Vehicle Traffic

Use the built-in simulator to generate multiple moving vehicles:

```bash
make simulate
```

Custom example:

```bash
go run ./cmd/simulator -url http://localhost:8080 -vehicles 20 -interval 2s -duration 2m
```

## API Sanity Checks

### Submit one location

```bash
curl -X POST http://localhost:8080/api/v1/locations \
  -H 'Content-Type: application/json' \
  -d '{
    "vehicle_id": "demo-vehicle-42",
    "trip_id": "route-5-0830",
    "latitude": -1.2921,
    "longitude": 36.8219,
    "bearing": 180,
    "speed": 8.5,
    "accuracy": 12,
    "timestamp": '"$(date +%s)"'
  }'
```

### Get feed (JSON debug format)

```bash
curl 'http://localhost:8080/gtfs-rt/vehicle-positions?format=json'
```

### Get admin status

```bash
curl http://localhost:8080/api/v1/admin/status
```

## Troubleshooting

- `connection refused` when posting locations:
  - confirm server is running on `localhost:8080`
- DB connection/migration errors:
  - check `DATABASE_URL`
  - verify Postgres container is healthy (`docker compose ps`)
- `address already in use` for `0.0.0.0:5432` when running `make up`:
   - another local Postgres is using port `5432`
   - stop that service, or update [docker-compose.yml](docker-compose.yml) to map a different host port and adjust `DATABASE_URL` accordingly
- empty feed:
   - make sure timestamp is within 5 minutes of server time (this is request validation in `handlers.go`, independent of `STALENESS_THRESHOLD`)
  - ensure coordinates are valid and non-zero
