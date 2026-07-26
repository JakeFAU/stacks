# Repository Guidelines

## Purpose

Stacks builds a provenance-backed temporal knowledge graph from personal
documents and other source material. Its purpose is not merely to find relevant
passages, but to explain how entities, relationships, events, and beliefs
changed over time.

The system should remain understandable, auditable, and easy to operate by one person. Prefer explicit data flow, narrow interfaces, deterministic behavior, and boring infrastructure over clever abstractions.

The likely long-term shape is:

1. discover documents from one or more sources,
2. detect additions and changes,
3. extract and normalize content,
4. extract source-grounded observations with explicit uncertainty,
5. resolve entities without destroying aliases or prior interpretations,
6. build time-bounded relationships and events,
7. answer temporal questions with an evidence trail back to source material.

Document retrieval, embeddings, and language models support that goal; they are
not the product by themselves. A graph edge without provenance, temporal
semantics, or epistemic status is not trusted knowledge.

Do not assume that every step belongs in the same process or abstraction. Introduce boundaries only when the code has earned them.

## Project Structure

```text
cmd/stacks/                        process entrypoint and dependency composition
core/                              provider-neutral evidence, identity, observation, and temporal contracts (separate Go module)
adapters/postgres/                 canonical PostgreSQL repositories and scoped embedded migrations (separate Go module)
internal/app/                      application lifecycle, command dispatch, and review mapping
internal/cli/                      Cobra command tree, operator syntax, output, and consumer-owned ports
internal/config/                   environment loading, defaults, and command-specific validation
internal/ingest/                   resumable canonical ingestion orchestration
internal/analysis/                 read-only temporal analysis use case over canonical observations
internal/directory/                optional directory enrichment orchestration and persistence mapping
internal/source/                   provider-neutral document source boundary and Drive adapter
internal/extract/                  structured extraction contracts, validation, and versioned prompts
internal/doctor/                   read-only dependency and migration readiness checks
internal/httpapi/                  HTTP handlers, routing, middleware, and transport types
internal/localdb/                  guarded local Compose database reset
internal/observability/            Zap and OpenTelemetry lifecycle helpers
internal/...                       focused provider and application packages added as capabilities emerge
db/init/                           first-start PostgreSQL role bootstrap only
bin/                               local build output; never committed
```

`cmd/stacks` must remain thin. It may construct dependencies, handle process signals, start the application, and report fatal startup errors. It must not contain business logic.

Keep tests beside the code they exercise using `*_test.go`.

As the system grows, keep these concerns behind explicit boundaries:

* document sources,
* content extraction,
* persistence,
* observation extraction,
* entity resolution,
* temporal graph construction,
* embedding or model providers,
* graph and evidence retrieval,
* application orchestration,
* HTTP transport.

Domain logic must not depend directly on provider SDKs, SQL drivers, HTTP frameworks, or model-specific request types.

Do not create packages such as `common`, `shared`, `helpers`, `utils`, or `manager`. Name packages after the responsibility they own.

## Engineering Principles

### Prefer the smallest complete design

Implement the narrowest design that satisfies the current requirement.

Do not add speculative extension points, plugin systems, registries, factories, event buses, generic repositories, or configuration options for hypothetical future needs.

A little duplication is cheaper than the wrong abstraction. Extract shared code only after the common contract is visible.

### Make state transitions explicit

Document ingestion and graph construction are stateful. Code should make it clear:

* what was observed,
* what changed,
* what work was attempted,
* what succeeded,
* what failed,
* what may safely be retried.

Prefer idempotent operations and stable identifiers. A repeated ingestion run
should not silently duplicate documents, observations, entities,
relationships, evidence links, or derived artifacts.

Never overwrite history to represent a new state. Preserve the prior assertion
and record when the replacement became valid or known.

### Preserve provenance

Every observation, relationship, event, and answer must remain traceable to its
source evidence and the transformation that produced it.

Where applicable, retain:

* source provider,
* source document identifier,
* source path or URL,
* document version or content hash,
* ingestion timestamp,
* extraction method,
* chunk position,
* relevant source metadata,
* model and prompt version for model-derived claims,
* confidence and epistemic status.

Distinguish when something was true or occurred from when Stacks observed or
recorded it. Do not substitute ingestion time when source time is unknown; keep
the time unknown and preserve that uncertainty.

Graph results should expose enough provenance to support citations, temporal
reasoning, correction, and debugging.

### Keep policy separate from mechanism

Provider clients perform I/O. Domain code decides what the system means.

For example:

* a Drive client may list and fetch files,
* an extractor may convert bytes into normalized text,
* an ingestion service decides whether a document is new or changed,
* an observation extractor may propose source-grounded claims,
* entity resolution decides whether two mentions refer to the same entity,
* a graph service applies temporal and provenance invariants,
* a retriever gathers graph paths and supporting evidence.

Do not let vendor behavior become the domain model by accident.

### Optimize for observability before scale

This is a private-data system. Failures must be diagnosable without logging private document contents.

Use structured logs. Include stable operational identifiers where useful, such as source ID, document ID, ingestion run ID, or content hash. Do not log raw documents, extracted passages, prompts containing private text, embeddings, credentials, or authorization headers.

Logging uses Zap. Traces and metrics use OpenTelemetry. Successful spans must be
explicitly marked `OK`; use `observability.FinishSpan` for manually owned spans.
Create spans only for meaningful request, job, storage, or provider boundaries.
Record decisions as attributes and events on the owning span instead of creating
deep decision subtrees.

Prefer distributional metrics for durations, input/output sizes, candidate
counts, and confidence. Metric attributes must be low-cardinality. Never use
document contents, prompts, user-controlled values, or unbounded identifiers as
metric labels. `observability.DecisionRecorder` is the default decision boundary.

## Build and Development Commands

* `make run` starts the service with `go run ./cmd/stacks`.
* `make build` compiles `bin/stacks`.
* `make test` runs `go test ./...`.
* `make staticcheck` runs the repository-pinned Staticcheck release.
* `make fmt` formats all non-vendored Go files with `gofmt`.
* `make db-up` starts the local PostgreSQL and pgvector container.
* `make db-migrate` applies pending forward migrations as `stacks_admin`.
* `make db-status` reports applied and pending migrations.
* `make db-down` stops PostgreSQL without deleting its named volume.
* `make obs-up` starts the optional Collector, Prometheus, Tempo, Loki, and Grafana stack.
* `make obs-down` stops that stack without deleting its named volumes.
* `make obs-config` validates the observability Compose model.

The default health endpoint is:

```text
http://127.0.0.1:8080/healthz
```

Before considering a change complete, run:

```bash
make fmt
make test
make staticcheck
```

Do not claim verification that was not actually performed.

## Go Conventions

Use idiomatic Go and standard `gofmt` formatting.

* Exported identifiers use `PascalCase`.
* Unexported identifiers use `camelCase`.
* Package names are short lowercase nouns without underscores.
* Constructors should return concrete types unless callers genuinely need an interface.
* Accept interfaces at boundaries; return concrete implementations.
* Define interfaces near the code that consumes them, not near the implementation.
* Keep interfaces small and behavior-oriented.
* Avoid `interface{}` and `any` unless the data is genuinely untyped.
* Prefer ordinary control flow over reflection, code generation, or elaborate generic machinery.
* Use the standard library unless an external dependency provides substantial, concrete value.

Do not introduce a framework to avoid writing a small amount of straightforward Go.

### Configuration

All runtime-tunable values belong in `internal/config`.

Use `STACKS_*` environment variables. Apply defaults deliberately and validate configuration once during startup.

Do not scatter these values through application code:

* ports,
* timeouts,
* model names,
* provider URLs,
* database locations,
* concurrency limits,
* retry policies,
* chunk sizes,
* retrieval limits.

Invalid configuration should fail clearly before the service begins accepting work.

### Errors

Return errors rather than logging them deep in the call stack.

Wrap errors with useful operation context while preserving the original error:

```go
return fmt.Errorf("load document %q: %w", documentID, err)
```

Error messages should describe the failed operation, not merely restate the lower-level error.

Use sentinel or typed errors only when callers need to make a behavioral decision. Do not create custom error taxonomies for decoration.

### Context

Pass `context.Context` through I/O and potentially long-running operations.

Do not store contexts in structs. Do not replace a caller-provided context with `context.Background()`. Respect cancellation and deadlines at provider and storage boundaries.

## HTTP API

Keep HTTP concerns in `internal/httpapi`.

Handlers should:

1. decode and validate transport input,
2. call an application service,
3. translate the result into an HTTP response.

Handlers must not contain persistence queries, provider-specific logic, retrieval algorithms, or document-processing workflows.

Use explicit request and response types. Do not expose internal persistence models or provider SDK types directly through the API.

Health endpoints should report process health without performing expensive dependency checks on every request. Add separate readiness semantics if startup later depends on external systems.

## Persistence and Schema Changes

Keep SQL and database-specific behavior behind a focused storage boundary.

Prefer explicit queries over opaque ORM behavior. Transactions should be visible at the application operation that requires atomicity.

Schema changes must be forward-moving, reviewable, and safe against existing
local data. Do not silently destroy or reinterpret stored documents, graph
history, or provenance.

When adding persistence behavior, define the relevant invariants. Examples include:

* source document IDs are unique within a provider,
* document versions are immutable,
* only one active version exists per logical document,
* observations point to immutable source evidence,
* valid time and recorded time have distinct meanings,
* entity merges preserve aliases and can be audited or reversed,
* relationships may change without erasing their prior state,
* repeated writes are idempotent.

## Ingestion and Temporal Graphs

Treat ingestion as a resumable pipeline, not a single magical function.

Keep source discovery, fetching, extraction, normalization, observation
extraction, entity resolution, graph updates, and state recording separable
enough to test independently.

Do not mark a document version complete until all required durable work has succeeded.

Temporal query code should distinguish:

* entity and time-range resolution,
* graph traversal,
* supporting and conflicting evidence retrieval,
* chronology construction,
* answer generation with citations.

Query planning must classify temporal intent, resolve valid-time selections and
recorded-time knowledge cutoffs, and produce explicit retrieval operations.
Aggregation, conflict handling, diffing, and chronology construction belong in
the deterministic retrieval layer. Narration must consume dated, ordered,
provenance-bearing results rather than undated source snippets.

State aggregation must never select a winner by confidence alone. It should
merge agreeing support, preserve competing values with provenance, and mark
temporally uncertain or hypothesized values explicitly. Recorded-time cutoffs
and valid-time inclusion are independent filters.

Temporal precedence alone does not establish causality. Causal-chain retrieval
must require explicit source-supported causal observations and must preserve
contradicting evidence.

Do not bury temporal semantics, entity identity rules, or graph mutation policy
inside a model prompt or provider client. Embedding similarity may suggest
candidates; it must not silently establish identity or truth.

Any generated answer should retain references to the observations and source
passages used, distinguish evidence from inference, and surface material
conflicts or gaps. A plausible narrative without provenance is a failure mode,
not a feature.

## Testing

Use Go’s standard `testing` package.

Name tests after observable behavior:

```go
func TestLoaderRejectsMissingDatabaseURL(t *testing.T)
func TestRelationshipChangePreservesPriorState(t *testing.T)
```

Use table tests when multiple inputs exercise the same contract. Do not force unrelated scenarios into one table merely to reduce line count.

Behavior changes and bug fixes require a regression test that would previously have failed.

Prefer testing through public behavior. Use fakes at I/O boundaries when needed, but do not mock every internal function. Tests should preserve freedom to refactor implementation details.

Tests must be deterministic. Avoid real network calls, wall-clock dependence, random sleeps, and shared mutable global state.

When time or randomness affects behavior, inject the narrow capability required by the code.

No numeric coverage target is enforced. Coverage is not a substitute for testing invariants and failure paths.

## Dependencies

Before adding a dependency, verify that the standard library or a small local implementation is insufficient.

Cobra is the intentional exception for the root application's CLI transport.
Keep `github.com/spf13/cobra` inside `internal/cli`, construct a fresh command
tree for each execution, and keep Cobra types out of `core`,
`adapters/postgres`, providers, storage, and domain contracts. Do not use the
`cobra-cli` generator or package-global command state.

A new dependency should justify its:

* operational value,
* maintenance cost,
* transitive dependency graph,
* security exposure,
* effect on build and startup behavior.

Do not add dependencies solely to save a few lines of clear code.

Keep provider SDKs at the edge of the system. Do not allow their types to spread across package boundaries.

## Security and Privacy

Never commit:

* API keys,
* credentials,
* tokens,
* `.env` files,
* private document contents,
* production database files,
* exported embeddings,
* retrieved passages from personal documents.

Do not weaken TLS verification, authentication, authorization, or credential handling to simplify local development.

Use least-privilege credentials for source providers and databases.

Private source payloads must not appear in logs, test fixtures, panic output, or error messages. Use synthetic fixtures in tests.

Treat prompts and model-provider requests as data disclosure boundaries. Code should make it possible to understand which private content is sent to which external provider.

## Commits and Pull Requests

Use concise imperative commit subjects:

```text
Add document ingestion contract
Persist temporal relationship observations
Answer graph queries with supporting evidence
```

Each commit should represent one coherent concern. Do not mix broad cleanup, formatting churn, dependency upgrades, and behavior changes unless they are inseparable.

Pull requests should include:

* the behavior changed,
* the reason for the change,
* important design trade-offs,
* configuration or schema changes,
* privacy or security implications,
* exact verification performed,
* relevant issue links.

Include screenshots only for user-visible interface changes.

## Rules for Automated Changes

When modifying this repository:

1. Read the relevant code before proposing architecture.
2. Preserve existing behavior unless the task requires changing it.
3. Keep the patch scoped to the requested work.
4. Do not rename, reorganize, or reformat unrelated code.
5. Do not introduce abstractions merely to make the patch appear extensible.
6. Add or update tests for changed behavior.
7. Update documentation when commands, configuration, schemas, or externally visible behavior change.
8. Run the relevant formatting, tests, and static analysis.
9. Report any checks that could not be run.
10. Call out assumptions rather than silently encoding them.

When requirements are ambiguous, prefer the simplest reversible implementation consistent with the repository’s existing direction.
