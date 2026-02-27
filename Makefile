.PHONY: build run test proto

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server/main.go

test:
	go test ./...

proto:
	protoc --go_out=. --go_opt=paths=source_relative proto/vehicle_positions.proto
