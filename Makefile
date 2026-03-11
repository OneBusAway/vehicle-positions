.PHONY: generate fmt vet test

generate:
	cd db && sqlc generate

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...
