# Task D5.1 report — generic temporal evidence parity

## Scope and baseline

- Baseline: `4daa8af1f323ee36bdd458c50ca835d8a1999527` on
  `codex/plan-d-temporal-query-design`.
- Scope: test-only parity evidence and the mandatory integration-gate wiring
  needed to execute it. No runtime behavior, schema, migration, configuration,
  provider, push, or pull-request change.
- PostgreSQL test placement is intentionally split:
  `adapters/postgres/temporal_query_integration_test.go` owns canonical
  adapter write/projection acceptance, while
  `internal/query/postgres_integration_test.go` owns public
  `query.Service` plus `query.PostgresRepository` equality. Importing
  `internal/query` from the in-package adapter test would create the cycle
  `postgres -> query -> postgres`.

## Generic fixture contract

The synthetic fixture contains only the stable person IDs
`entity:atlas-owner` and `entity:atlas-reviewer`, generic
`atlas.responsibility/*` predicates, and synthetic Atlas text.

It preserves:

- reviewed owner and reviewer mentions with reviewer-authority resolution;
- one exact valid-time window and an independent exact recorded-time cutoff;
- a dated initial state, transfer, bounded competing state, reviewed pair,
  unknown-time observation, and admission-scoped observation;
- immutable exact support and contradicting spans with role separation;
- complete observation contributions, derivation, grounding mention IDs, and
  citations;
- deterministic trajectory ordering, a competing-value conflict, unknown-time
  temporal uncertainty, unresolved-mention coverage, and authority coverage;
- a later admission successor that hides one observation currently while the
  earlier admitted observation remains visible as of the cutoff, with exact
  authority-excluded coverage for both requested `EntityMatchAny` endpoints;
  and
- `EntityMatchAll` acceptance across the two reviewed generic entities.

The fixture contains no vertical roles, scoring vocabulary, derived
classification, source-type specialization, or private content.

## Typed service parity

`TestGenericTemporalQueryPreservesVerticalEvidenceParity` executes the public
typed `query.Service` for current and as-of knowledge. It asserts exact
chronology, unchanged valid-time selection across recorded-time scopes, full
contributions, grounding, support, counterevidence, conflict candidates,
unknown-time uncertainty, citations, current/as-of authority, and explicit
gaps.

Focused GREEN:

```text
go test ./internal/query -run TestGenericTemporalQueryPreservesVerticalEvidenceParity -count=1
ok stacks/internal/query
```

## CLI JSON parity

`TestQueryCommandJSONPreservesGenericTemporalEvidenceParity` passes the generic
trajectory through public `cli.QueryCommand`. The test decodes the emitted
payload into the exact v1 test DTO using `DisallowUnknownFields`, requires EOF,
and asserts the request, chronology, contribution grounding, valid time,
support/counter spans, conflict, uncertainty, and gaps. It does not call
renderer internals or inspect an untyped property bag.

Focused GREEN:

```text
go test ./internal/cli -run 'Test.*Parity' -count=1
ok stacks/internal/cli
```

## PostgreSQL parity

The adapter-package test persists generic canonical documents, exact evidence
spans, entities, observations, admission decisions, and the admission
successor through public canonical writes. It then checks the public temporal
snapshot projection for current/as-of admission behavior, exact role-separated
evidence, unknown valid time, and `EntityMatchAll`.

The root integration test persists the reviewed-mention variant through public
canonical writes, executes public `query.Service` through
`query.PostgresRepository`, and compares both current and as-of results by
structural equality with independently assembled in-memory snapshots after
canonical normalization and ordering.

The first adapter integration run rejected the test fixture because its direct
adapter predicate list was not in canonical order. The test input was corrected
to the adapter's public normalized contract.

Follow-up review then found that the root PostgreSQL parity test had not been
included in `make test-integration`, and direct execution exposed a missing
reviewer-scoped authority coverage row in the independently assembled current
oracle. Canonical SQL correctly emits authority-excluded coverage for both
requested `EntityMatchAny` endpoints of the retired observation. The oracle now
includes both endpoints and the test asserts those exact entity IDs.

`test-integration` now retains `internal/analysis` and also executes
`internal/query`. An executable Make dry-run contract test keeps both packages
mandatory. The corrected full integration run passed:

```text
make test-integration ENV_FILE=.env
ok github.com/JakeFAU/stacks/adapters/postgres
ok stacks/internal/ingest
ok stacks/internal/directory
ok stacks/internal/analysis
ok stacks/internal/app
ok stacks/internal/doctor
ok stacks/internal/query
```

This is synthetic local PostgreSQL acceptance only. It does not establish
acceptance for document sources, directory providers, model providers, cloud
services, or private corpora.

## Mutation RED and restored GREEN

Mutation: temporarily changed `internal/query/projectFact` from
`ContradictingCitations: contradicting` to
`ContradictingCitations: contradicting[:0]`.

RED:

```text
go test ./internal/query -run TestGenericTemporalQueryPreservesVerticalEvidenceParity -count=1
--- FAIL: TestGenericTemporalQueryPreservesVerticalEvidenceParity
initial citations ... counter [] ... want exact role-separated spans
```

The production line was restored exactly. Restored GREEN:

```text
go test ./internal/query -run \
  'TestGenericTemporalQueryPreservesVerticalEvidenceParity|TestPostgresRepositoryPreservesGenericTemporalEvidenceParity' \
  -count=1
ok stacks/internal/query

go test ./internal/cli -run 'Test.*Parity' -count=1
ok stacks/internal/cli
```

## Deterministic gates and migration immutability

All completed successfully:

- `make fmt`
- `sh scripts/check-test-integration-packages.sh`
- focused typed, CLI, root PostgreSQL, and adapter PostgreSQL parity tests
- `make test-integration ENV_FILE=.env`
- `make test`
- `make test-race`
- `make staticcheck`
- `make build`
- `make modules-check`
- `git diff --check`
- migration and manifest `git diff --exit-code`
- `make db-status ENV_FILE=.env`: core current `3/3`; optional directory scope
  absent and unconfigured

Migration bytes were unchanged. Recorded SHA-256 values:

```text
0b264c7fb57e31f97335e4373c988b8e7425e43afbeaaf437ddec92f3db6f4a4  core 00001_documents_evidence.sql
5a0f88834edebfa587f69d24c58a3e5f62fe8205ab8e9246aeebc88dde30c5ee  core 00002_identity_admission.sql
0a9047e408fa0621c776837546460a936c0b382e00ef32299c2a613788c90c17  core 00003_extraction_observations.sql
a06de5237eb4d36af0c8c261a31131c9514b5afe26b032e18219b0ea83f5fde5  directory 00001_directory.sql
```

## Delivery boundary

The initial change is committed locally as
`Prove generic temporal evidence parity`. The deletion-gate review fix is a
separate local follow-up commit. Neither commit is pushed and no pull request
is created. Specialized runtime removal remains a later gated D5 task.
