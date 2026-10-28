.PHONY: help generate fmt vet test up down run smoke simulate ridersim css

# Tailwind CSS CLI version pinned for reproducible admin-UI builds. The Go CI
# workflow (.github/workflows/ci.yml) downloads the linux-x64 binary of this
# SAME version to verify web/static/css/admin.css is up to date — bump both
# together.
# Port the local server is expected on; `make run` reads PORT from the
# environment, so the simulator targets default to the same 8080.
PORT ?= 8080

TAILWIND_VERSION := v4.2.0
TAILWIND_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]' | sed 's/darwin/macos/')
TAILWIND_ARCH := $(shell uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/')
TAILWIND_BIN := .tools/tailwindcss-$(TAILWIND_VERSION)-$(TAILWIND_OS)-$(TAILWIND_ARCH)
# Expected SHA-256 per platform, from the release's sha256sums.txt:
# https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/sha256sums.txt
TAILWIND_SHA256_linux_x64   := 8f65e2d21c675f1e8d265219979d17d10634c1f553a2f583265b7edb28726432
TAILWIND_SHA256_linux_arm64 := 376fd4da2c29eb81ae0638cd2f84a4304af92532f2f1576555f41bdb44c185da
TAILWIND_SHA256_macos_x64   := 18cd6bb94d0f26ff8a0fa8a966beb9ea36bea2c7c444397f7619a2b880260e65
TAILWIND_SHA256_macos_arm64 := d9e759fd6612dd442a9caa49d366b24e5097ea9802d35829da3f6db6ee5c2043
TAILWIND_SHA256 := $(TAILWIND_SHA256_$(TAILWIND_OS)_$(TAILWIND_ARCH))

help:
	@echo "Available targets:"
	@echo "  make up        - Start local Postgres + server with docker compose"
	@echo "  make down      - Stop local docker compose stack"
	@echo "  make run       - Run server locally (expects DATABASE_URL env var)"
	@echo "  make smoke     - Check health, authenticate as admin, verify admin status, and fetch GTFS-RT feed"
	@echo "  make simulate  - Run simulator against local server (needs ADMIN_BOOTSTRAP_EMAIL/PASSWORD)"
	@echo "  make ridersim  - Run rider simulator against local server (rider mode; honours PORT)"
	@echo "  make generate  - Regenerate sqlc code"
	@echo "  make fmt       - Format Go code"
	@echo "  make vet       - Run go vet"
	@echo "  make test      - Run test suite"
	@echo "  make css       - Compile web/styles/input.css to web/static/css/admin.css"

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
	@echo "Fetching health check..."
	@curl --silent --show-error --fail http://localhost:$(PORT)/health && echo " OK"
	@echo
	@if [ -z "$(ADMIN_BOOTSTRAP_EMAIL)" ] || [ -z "$(ADMIN_BOOTSTRAP_PASSWORD)" ]; then \
		echo "ERROR: ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD are required for authentication" >&2; \
		echo "Set them in your environment or in a .env file next to docker-compose.yml" >&2; \
		exit 1; \
	fi
	@echo "Authenticating as admin..."
	@LOGIN_RESPONSE=$$(curl --silent --show-error --fail -X POST http://localhost:$(PORT)/api/v1/auth/login -H "Content-Type: application/json" -d "{\"email\":\"$(ADMIN_BOOTSTRAP_EMAIL)\",\"password\":\"$(ADMIN_BOOTSTRAP_PASSWORD)\"}") || { echo "ERROR: login request failed" >&2; exit 1; }; \
	ACCESS_TOKEN=$$(echo "$$LOGIN_RESPONSE" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4); \
	if [ -z "$$ACCESS_TOKEN" ]; then \
		echo "ERROR: login response missing access_token" >&2; \
		exit 1; \
	fi; \
	echo "OK"; \
	echo; \
	echo "Fetching admin status..."; \
	curl --silent --show-error --fail -H "Authorization: Bearer $$ACCESS_TOKEN" http://localhost:$(PORT)/api/v1/admin/status && echo " OK"; \
	echo; \
	echo "Fetching GTFS-RT JSON feed..."; \
	curl --silent --show-error --fail 'http://localhost:$(PORT)/gtfs-rt/vehicle-positions?format=json' && echo " OK"; \
	echo

simulate:
	go run ./cmd/simulator -url http://localhost:$(PORT) -vehicles 5 -interval 6s -duration 30s

ridersim:
	go run ./cmd/ridersim -url http://localhost:$(PORT) -gtfs rider/testdata/fixture.zip -trip T1 -interval 1s -speed 10 -expect-end arrived

css: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i web/styles/input.css -o web/static/css/admin.css --minify

$(TAILWIND_BIN):
	mkdir -p .tools
	@test -n "$(TAILWIND_SHA256)" || { echo "no pinned Tailwind checksum for $(TAILWIND_OS)-$(TAILWIND_ARCH); add TAILWIND_SHA256_$(TAILWIND_OS)_$(TAILWIND_ARCH) to Makefile"; exit 1; }
	curl -fsSL https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(TAILWIND_ARCH) -o $(TAILWIND_BIN).tmp
	echo "$(TAILWIND_SHA256)  $(TAILWIND_BIN).tmp" | shasum -a 256 -c - || { rm -f $(TAILWIND_BIN).tmp; exit 1; }
	mv $(TAILWIND_BIN).tmp $(TAILWIND_BIN)
	chmod +x $(TAILWIND_BIN)
