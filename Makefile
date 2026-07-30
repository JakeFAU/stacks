STATICCHECK_VERSION := 2026.1
ENV_FILE ?= .env
export STACKS_MAKE_ENV_FILE := $(value ENV_FILE)

define resolve_env_file_path
case "$$STACKS_MAKE_ENV_FILE" in \
	/*) env_file_path=$$STACKS_MAKE_ENV_FILE ;; \
	*) env_file_path=$$PWD/$$STACKS_MAKE_ENV_FILE ;; \
esac;
endef

.PHONY: auth-google auth-google-directory build db-down db-migrate db-reset db-status db-up doctor entities fmt modules-check obs-config obs-down obs-up query-trend review run staticcheck sync test test-env-file test-integration test-integration-contract test-race test-retired-analysis-terminology

auth-google:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and configure Google OAuth paths\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks auth google

auth-google-directory:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and configure Google directory OAuth paths\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks auth google-directory

build:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		if [ "$$module" = "." ]; then \
			(cd "$$module" && go build -o bin/stacks ./cmd/stacks) || exit; \
		else \
			(cd "$$module" && go build ./...) || exit; \
		fi; \
	done

db-down:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and set both passwords\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	docker compose --env-file "$$env_file_path" down

db-migrate:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and set both passwords\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks db-migrate

db-reset:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and set both passwords\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks db-reset "$(CONFIRM)"

db-status:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and set both passwords\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks db-status

db-up:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and set both passwords\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	docker compose --env-file "$$env_file_path" up --detach --wait postgres

doctor:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and configure the application\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks doctor

entities:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and configure the application\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks entities $(ARGS)

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

query-trend:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and configure the query database\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks query trend $(ARGS)

review:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and configure the application\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks review $(ARGS)

run:
	go run ./cmd/stacks

sync:
	@$(resolve_env_file_path) \
	test -f "$$env_file_path" || { printf 'copy .env.example to %s and configure the application\n' "$$STACKS_MAKE_ENV_FILE" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; go run ./cmd/stacks sync

test: test-env-file test-integration-contract test-retired-analysis-terminology
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go test ./...) || exit; \
	done

test-env-file:
	sh scripts/check-env-file-loading.sh

test-integration-contract:
	sh scripts/check-test-integration-packages.sh

test-retired-analysis-terminology:
	sh scripts/check-retired-analysis-terminology.sh

test-race:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go test -race ./...) || exit; \
	done

test-integration:
	@$(resolve_env_file_path) \
	test -n "$$STACKS_MAKE_ENV_FILE" || { echo "ENV_FILE is required" >&2; exit 1; }; \
	test -f "$$env_file_path" || { echo "ENV_FILE does not exist" >&2; exit 1; }; \
	set -a; . "$$env_file_path"; set +a; \
		test -n "$$STACKS_TEST_DATABASE_URL" || (echo "STACKS_TEST_DATABASE_URL is required" >&2; exit 1); \
		test -n "$$STACKS_TEST_MIGRATION_DATABASE_URL" || (echo "STACKS_TEST_MIGRATION_DATABASE_URL is required" >&2; exit 1); \
		(cd adapters/postgres && GOWORK=off go run ./cmd/validate-test-database) && \
		(cd adapters/postgres && GOWORK=off go test ./... -count=1) && \
		go test ./internal/ingest ./internal/directory ./internal/app ./internal/doctor ./internal/query ./internal/queryplan -count=1

staticcheck:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...) || exit; \
	done
