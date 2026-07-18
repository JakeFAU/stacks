# Stacks

Stacks builds a temporal knowledge graph from personal documents and other
source material. It is designed to answer questions about change: how a
relationship evolved, how a product reached its current form, what was believed
at a particular point, and which evidence supports that history.

Documents remain immutable evidence. Stacks extracts observations, resolves
entities, and records time-bounded relationships without erasing prior states.
Answers must distinguish source evidence from inference and cite their path back
to the original material.

The initial scaffold is deliberately small. PostgreSQL and pgvector provide the
storage foundation, but vector search is supporting machinery rather than the
product: similarity can find candidate evidence; it cannot establish identity,
chronology, or truth.

## Product principles

- Preserve both when something happened and when Stacks learned about it.
- Never replace history with the latest state.
- Treat model-produced observations and entity matches as untrusted proposals.
- Preserve aliases, uncertainty, conflicting evidence, and source provenance.
- Answer temporal questions with inspectable graph paths and citations.

## Requirements

- Go 1.26 or newer
- Docker with Compose

## Run

```sh
make run
```

The service listens on `127.0.0.1:8080` by default. Verify it with:

```sh
curl http://127.0.0.1:8080/healthz
```

## Configuration

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `STACKS_HTTP_HOST` | `127.0.0.1` | HTTP bind host |
| `STACKS_HTTP_PORT` | `8080` | HTTP bind port |
| `STACKS_READ_HEADER_TIMEOUT_SECONDS` | `5` | Maximum time to read request headers |

## Development

```sh
make fmt
make test
make staticcheck
make build
```

## Local database

Create local credentials before starting PostgreSQL:

```sh
cp .env.example .env
openssl rand -hex 24
```

Put independently generated values in `STACKS_DB_ADMIN_PASSWORD` and
`STACKS_DB_APP_PASSWORD`, then run:

```sh
make db-up
make db-migrate
make db-status
```

`stacks_admin` owns the local database and is used only for migrations.
Application code must connect as the least-privileged `stacks_app` role. The
database listens only on `127.0.0.1` and uses `STACKS_DB_PORT` (default `5432`).

`make db-down` stops the database while preserving its named volume. To remove
local database contents deliberately, run `docker compose down --volumes`.
Bootstrap credentials and roles are created only when PostgreSQL initializes a
new volume; changing `.env` later does not update roles in an existing volume.

## Intended architecture

- `cmd/stacks`: process entrypoint
- `internal/app`: application lifecycle and dependency wiring
- `internal/config`: validated runtime configuration
- `internal/httpapi`: HTTP transport boundary
- `db/init`: first-start cluster and role bootstrap
- `db/migrations`: ordered, forward-only schema migrations

The next slice should define immutable source evidence and temporal observation
contracts, including valid time, recorded time, provenance, and epistemic
status, before introducing graph persistence or model extraction.
