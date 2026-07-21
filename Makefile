STATICCHECK_VERSION := 2026.1
GOOSE_VERSION := v3.27.1
ENV_FILE ?= .env

.PHONY: build db-down db-migrate db-status db-up fmt obs-config obs-down obs-up run staticcheck test test-integration

build:
	go build -o bin/stacks ./cmd/stacks

db-down:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and set both passwords" >&2; exit 1)
	docker compose --env-file "$(ENV_FILE)" down

db-migrate:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and set both passwords" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; \
		PGPASSWORD="$${STACKS_DB_ADMIN_PASSWORD}" \
		go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) \
		-dir db/migrations postgres \
		"host=127.0.0.1 port=$${STACKS_DB_PORT:-5432} user=stacks_admin dbname=stacks sslmode=disable" up

db-status:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and set both passwords" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; \
		PGPASSWORD="$${STACKS_DB_ADMIN_PASSWORD}" \
		go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) \
		-dir db/migrations postgres \
		"host=127.0.0.1 port=$${STACKS_DB_PORT:-5432} user=stacks_admin dbname=stacks sslmode=disable" status

db-up:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and set both passwords" >&2; exit 1)
	docker compose --env-file "$(ENV_FILE)" up --detach --wait postgres

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

obs-config:
	docker compose -f compose.observability.yaml config --quiet

obs-down:
	docker compose -f compose.observability.yaml down

obs-up:
	docker compose -f compose.observability.yaml up --detach

run:
	go run ./cmd/stacks

test:
	go test ./...

test-integration:
	@test -n "$$STACKS_TEST_DATABASE_URL" || (echo "STACKS_TEST_DATABASE_URL is required" >&2; exit 1)
	STACKS_TEST_DATABASE_URL="$$STACKS_TEST_DATABASE_URL" go test ./internal/storage

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
