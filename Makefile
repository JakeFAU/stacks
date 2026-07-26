STATICCHECK_VERSION := 2026.1
ENV_FILE ?= .env

.PHONY: analyze auth-google auth-google-directory build db-down db-migrate db-reset db-status db-up doctor entities fmt modules-check obs-config obs-down obs-up review run staticcheck sync test test-integration test-race

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
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks db-migrate

db-reset:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and set both passwords" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks db-reset "$(CONFIRM)"

db-status:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and set both passwords" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks db-status

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
	@test -n "$(ENV_FILE)" || (echo "ENV_FILE is required" >&2; exit 1)
	@test -f "$(ENV_FILE)" || (echo "ENV_FILE does not exist" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; \
		test -n "$$STACKS_TEST_DATABASE_URL" || (echo "STACKS_TEST_DATABASE_URL is required" >&2; exit 1); \
		test -n "$$STACKS_TEST_MIGRATION_DATABASE_URL" || (echo "STACKS_TEST_MIGRATION_DATABASE_URL is required" >&2; exit 1); \
		(cd adapters/postgres && GOWORK=off go test ./... -count=1) && \
		go test ./internal/ingest ./internal/directory ./internal/analysis ./internal/doctor -count=1

staticcheck:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...) || exit; \
	done
