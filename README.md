# Stacks

Stacks is a personal document ingestion and retrieval service. The project will
watch document sources, preserve their provenance, index their contents, and
provide grounded retrieval over a continuously growing collection.

The initial scaffold is deliberately small and uses only the Go standard
library. Storage, ingestion, chunking, embeddings, and retrieval contracts will
be added as their boundaries are defined.

## Requirements

- Go 1.26 or newer

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

## Intended architecture

- `cmd/stacks`: process entrypoint
- `internal/app`: application lifecycle and dependency wiring
- `internal/config`: validated runtime configuration
- `internal/httpapi`: HTTP transport boundary

The next slice should define the source-document and ingestion-job contracts,
then add PostgreSQL and pgvector behind a storage interface.
