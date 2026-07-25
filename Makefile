STATICCHECK_VERSION := 2026.1
GOOSE_VERSION := v3.27.1
ENV_FILE ?= .env

.PHONY: analyze auth-google auth-google-directory build db-down db-migrate db-status db-up doctor entities fmt modules-check obs-config obs-down obs-up review run staticcheck sync test test-integration test-race

analyze:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure the PoC" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks analyze

auth-google:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure Google OAuth paths" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks auth google

auth-google-directory:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure Google directory OAuth paths" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks auth google-directory

build:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		if [ "$$module" = "." ]; then \
			(cd "$$module" && go build -o bin/stacks ./cmd/stacks) || exit; \
		else \
			(cd "$$module" && go build ./...) || exit; \
		fi; \
	done

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

doctor:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure the PoC" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks doctor

entities:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure the PoC" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks entities $(ARGS)

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

modules-check:
	sh scripts/check-modules.sh

obs-config:
	docker compose -f compose.observability.yaml config --quiet

obs-down:
	docker compose -f compose.observability.yaml down

obs-up:
	docker compose -f compose.observability.yaml up --detach

review:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure the PoC" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks review $(ARGS)

run:
	go run ./cmd/stacks

sync:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure the PoC" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks sync

test:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go test ./...) || exit; \
	done

test-race:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go test -race ./...) || exit; \
	done

test-integration:
	@test -n "$$STACKS_TEST_DATABASE_URL" || (echo "STACKS_TEST_DATABASE_URL is required" >&2; exit 1)
	@test -n "$$STACKS_TEST_MIGRATION_DATABASE_URL" || (echo "STACKS_TEST_MIGRATION_DATABASE_URL is required" >&2; exit 1)
	STACKS_TEST_DATABASE_URL="$$STACKS_TEST_DATABASE_URL" \
		STACKS_TEST_MIGRATION_DATABASE_URL="$$STACKS_TEST_MIGRATION_DATABASE_URL" \
		go test ./internal/storage ./internal/doctor

staticcheck:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...) || exit; \
	done
