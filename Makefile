.PHONY: help generate fmt vet test check up down run smoke simulate ridersim css demo demo-down

# Tailwind CSS CLI version pinned for reproducible admin-UI builds. The Go CI
# workflow (.github/workflows/ci.yml) downloads the linux-x64 binary of this
# SAME version to verify web/static/css/admin.css is up to date — bump both
# together.
# Port the local server is expected on; `make run` reads PORT from the
# environment, so the simulator targets default to the same 8080.
PORT ?= 8080

# The demo stack (docs/demo.md) publishes the server on this port, on loopback.
# Not 8080, so it can run beside the development stack or a OneBusAway server.
DEMO_PORT ?= 18080
export DEMO_PORT
DEMO_COMPOSE := docker compose -f docker-compose.demo.yml
DEMO_ENV := .env.demo.local

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

# sqlc version the committed code in db/ is generated with. The Go CI workflow
# installs this SAME version to check db/ is current — bump both together.
SQLC_VERSION := v1.31.1

help:
	@echo "Available targets:"
	@echo "  make up        - Start local Postgres + server with docker compose"
	@echo "  make down      - Stop local docker compose stack"
	@echo "  make run       - Run server locally (expects DATABASE_URL env var)"
	@echo "  make smoke     - Post sample location and fetch feed/status (needs ADMIN_BOOTSTRAP_EMAIL/PASSWORD)"
	@echo "  make simulate  - Run simulator against local server (needs ADMIN_BOOTSTRAP_EMAIL/PASSWORD)"
	@echo "  make ridersim  - Run rider simulator against local server (rider mode; honours PORT)"
	@echo "  make demo      - Start the demo: server, sample GTFS and simulated vehicles (docs/demo.md)"
	@echo "  make demo-down - Stop the demo and remove its containers, volume, images and secrets"
	@echo "  make generate  - Regenerate sqlc code"
	@echo "  make check     - Run the formatting and generated-code checks CI runs"
	@echo "  make fmt       - Format Go code"
	@echo "  make vet       - Run go vet"
	@echo "  make test      - Run test suite"
	@echo "  make css       - Compile web/styles/input.css to web/static/css/admin.css"

generate:
	cd db && sqlc generate

# The two checks CI runs before vet and test: every Go file is gofmt'd, and
# db/ matches what sqlc $(SQLC_VERSION) generates from db/query.sql.
check:
	@unformatted=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$unformatted" ]; then echo "Not gofmt'd (run 'make fmt'):"; echo "$$unformatted"; exit 1; fi
	@case "$$(sqlc version 2>/dev/null)" in \
	  $(SQLC_VERSION)) ;; \
	  *) echo "sqlc $(SQLC_VERSION) is required: go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)"; exit 1 ;; \
	esac
	@cd db && sqlc diff || { echo "db/ is stale; run 'make generate'"; exit 1; }
	@echo "OK"

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
	@test -n "$$ADMIN_BOOTSTRAP_EMAIL" -a -n "$$ADMIN_BOOTSTRAP_PASSWORD" || { echo "set ADMIN_BOOTSTRAP_EMAIL and ADMIN_BOOTSTRAP_PASSWORD to the server's admin account"; exit 1; }
	@echo "Posting a sample location as a provisioned driver on an active trip..."
	go run ./cmd/simulator -url http://localhost:$(PORT) -vehicles 1 -interval 5s -duration 6s
	@echo "Fetching admin status..."
	@TOKEN=$$(curl --silent --show-error --fail -X POST http://localhost:$(PORT)/api/v1/auth/login \
		-H 'Content-Type: application/json' \
		-d '{"email":"'"$$ADMIN_BOOTSTRAP_EMAIL"'","password":"'"$$ADMIN_BOOTSTRAP_PASSWORD"'"}' \
		| sed -E 's/.*"token":"([^"]+)".*/\1/') && \
	curl --silent --show-error --fail -H "Authorization: Bearer $$TOKEN" http://localhost:$(PORT)/api/v1/admin/status | cat
	@echo
	@echo "Fetching GTFS-RT JSON feed..."
	curl --silent --show-error --fail 'http://localhost:$(PORT)/gtfs-rt/vehicle-positions?format=json' | cat
	@echo

simulate:
	go run ./cmd/simulator -url http://localhost:$(PORT) -vehicles 5 -interval 6s -duration 30s

ridersim:
	go run ./cmd/ridersim -url http://localhost:$(PORT) -gtfs rider/testdata/fixture.zip -trip T1 -interval 1s -speed 10 -expect-end arrived

# The demo's JWT secret and admin password are generated on first use and kept
# in a git-ignored file: nothing secret is checked in, and a second `make demo`
# still has the password of the admin account already in the demo database.
$(DEMO_ENV):
	@secret=$$(openssl rand -hex 32) && password=$$(openssl rand -hex 12) && umask 077 && \
		printf 'JWT_SECRET=%s\nDEMO_ADMIN_PASSWORD=%s\n' "$$secret" "$$password" > $@

demo: $(DEMO_ENV)
	$(DEMO_COMPOSE) --env-file $(DEMO_ENV) up --build --detach --wait
	@. ./$(DEMO_ENV) && echo && \
		echo "The demo is running. Sign in as admin@demo.example with password $$DEMO_ADMIN_PASSWORD" && \
		echo "  Admin map:    http://localhost:$(DEMO_PORT)/admin/map" && \
		echo "  GTFS-RT feed: http://localhost:$(DEMO_PORT)/gtfs-rt/vehicle-positions?format=json" && \
		echo "Stop it and remove everything it made with: make demo-down"

# `down` still reads the compose file, which refuses to parse without the two
# secrets, so it gets placeholders: the generated file may already be gone.
demo-down:
	JWT_SECRET=unused DEMO_ADMIN_PASSWORD=unused $(DEMO_COMPOSE) down --volumes --rmi local
	rm -f $(DEMO_ENV)

css: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i web/styles/input.css -o web/static/css/admin.css --minify

$(TAILWIND_BIN):
	mkdir -p .tools
	@test -n "$(TAILWIND_SHA256)" || { echo "no pinned Tailwind checksum for $(TAILWIND_OS)-$(TAILWIND_ARCH); add TAILWIND_SHA256_$(TAILWIND_OS)_$(TAILWIND_ARCH) to Makefile"; exit 1; }
	curl -fsSL https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(TAILWIND_ARCH) -o $(TAILWIND_BIN).tmp
	echo "$(TAILWIND_SHA256)  $(TAILWIND_BIN).tmp" | shasum -a 256 -c - || { rm -f $(TAILWIND_BIN).tmp; exit 1; }
	mv $(TAILWIND_BIN).tmp $(TAILWIND_BIN)
	chmod +x $(TAILWIND_BIN)
