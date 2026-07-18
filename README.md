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
| `STACKS_LOG_LEVEL` | `info` | Zap log level: `debug`, `info`, `warn`, or `error` |
| `STACKS_OTEL_ENABLED` | `false` | Export logs, metrics, and traces through OTLP |
| `STACKS_OTEL_ENDPOINT` | `127.0.0.1:4317` | OTLP gRPC collector endpoint |
| `STACKS_OTEL_INSECURE` | `true` | Use plaintext OTLP transport for local development |
| `STACKS_OTEL_METRIC_EXPORT_INTERVAL` | `10s` | Metric export interval as a Go duration |
| `STACKS_OTEL_SERVICE_NAME` | `stacks` | OpenTelemetry service name |
| `STACKS_OTEL_TRACE_SAMPLE_RATIO` | `1` | Parent-based trace sampling ratio from `0` to `1` |

## Development

```sh
make fmt
make test
make staticcheck
make build
```

## Local observability

The optional observability Compose project runs an OpenTelemetry Collector,
Prometheus, Tempo, Loki, and Grafana without changing the database stack:

```sh
make obs-up
STACKS_OTEL_ENABLED=true make run
```

Grafana is available at `http://127.0.0.1:3000`; Prometheus, Tempo, and Loki are
also bound to loopback on ports `9090`, `3200`, and `3100`. Grafana starts with
its standard local `admin` login and provisions all three data sources. Run
`make obs-down` to stop the observability project while preserving its volumes.

Application code logs through Zap. OpenTelemetry exports all three signals over
OTLP when enabled. Successful spans are explicitly marked `OK`. Decisions should
be recorded as events on an existing meaningful span and as duration, input/output
size, and confidence histograms; they should not create a child span merely to
make a decision visible.

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
- `internal/observability`: Zap, OpenTelemetry lifecycle, and decision telemetry
- `internal/knowledge`: immutable evidence and temporal observation contracts
- `internal/query`: temporal query plans and deterministic retrieval operators
- `db/init`: first-start cluster and role bootstrap
- `db/migrations`: ordered, forward-only schema migrations

The current domain layer defines immutable source evidence and temporal
observations, including valid time, recorded time, provenance, derivation, and
epistemic status. Graph persistence and model extraction remain intentionally
unimplemented until these contracts have been exercised. Query plans represent
classified point-in-time, comparison, trajectory, and causal-chain intent;
resolve valid and recorded time independently; and require aggregation and
diffing before a narrator receives results. In-memory state aggregation applies
those temporal filters, merges agreeing observations, and preserves conflicting,
hypothesized, or temporally uncertain values rather than manufacturing state.
