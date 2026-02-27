.PHONY: build run test proto models clean

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server/main.go --port 8080

test:
	go test ./...

proto:
	protoc --go_out=. --go_opt=paths=source_relative proto/vehicle_positions.proto

models:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate

clean:
	rm -rf bin/ vehicle_positions.db
