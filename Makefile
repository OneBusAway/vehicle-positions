.PHONY: help generate fmt vet test up down run smoke simulate

help:
	@echo "Available targets:"
	@echo "  make up        - Start local Postgres + server with docker compose"
	@echo "  make down      - Stop local docker compose stack"
	@echo "  make run       - Run server locally (expects DATABASE_URL env var)"
	@echo "  make smoke     - Post sample location and fetch feed/status"
	@echo "  make simulate  - Run simulator against local server"
	@echo "  make generate  - Regenerate sqlc code"
	@echo "  make fmt       - Format Go code"
	@echo "  make vet       - Run go vet"
	@echo "  make test      - Run test suite"

generate:
	cd db && sqlc generate

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

up:
	docker compose up --build -d

down:
	docker compose down

run:
	go run .

smoke:
	@echo "Posting sample location..."
	curl --silent --show-error --fail \
		-X POST http://localhost:8080/api/v1/locations \
		-H 'Content-Type: application/json' \
		-d '{"vehicle_id":"demo-vehicle-1","trip_id":"demo-trip-1","latitude":-1.2864,"longitude":36.8172,"bearing":120,"speed":8.5,"accuracy":5.0,"timestamp":'"$$(date +%s)"'}' >/dev/null
	@echo "OK"
	@echo "Fetching admin status..."
	curl --silent --show-error --fail http://localhost:8080/api/v1/admin/status | cat
	@echo
	@echo "Fetching GTFS-RT JSON feed..."
	curl --silent --show-error --fail 'http://localhost:8080/gtfs-rt/vehicle-positions?format=json' | cat
	@echo

simulate:
	go run ./cmd/simulator -url http://localhost:8080 -vehicles 5 -interval 3s -duration 30s
