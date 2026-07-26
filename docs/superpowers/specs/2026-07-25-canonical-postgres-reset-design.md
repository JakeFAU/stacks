# Canonical PostgreSQL Reset Design

**Date:** 2026-07-25
**Status:** Implemented
**Scope:** Plan C

**Implementation evidence:** Canonical application cutover commit `04175cb`;
local PostgreSQL reset/acceptance fix commit `c908e81`. Verification included a
guarded reset of only the local Stacks PostgreSQL volume, clean embedded
migration install and repeat no-op, application-role fingerprints and status,
core-only and configured-directory doctor boundaries, full PostgreSQL
integration and race gates, deterministic repository gates, both synthetic
longitudinal scenarios, and an independent whole-branch review. Live Google
Drive, Workspace Directory, Bedrock, Anthropic, OpenAI, and private-corpus
acceptance remain unvalidated.

## Purpose

Plan C gives the temporal evidence engine a canonical PostgreSQL
representation and makes migration ownership match the open-source
architecture.

Stacks is still early-development software. There is no supported installation
base and the local PostgreSQL container is intentionally disposable. Plan C
therefore chooses a clean schema reset over a compatibility migration from the
mixed manager-confidence proof-of-concept schema.

This is a deliberate one-time development boundary:

- migrations `00001` through `00012` are retired rather than adopted;
- existing local database contents are discarded and recreated;
- application code, embedded migrations, tests, operator documentation, and
  doctor status change together;
- after the new baseline is established, its applied migrations become
  immutable and future schema changes are forward-only.

The reset removes compatibility complexity that protects no real installation.
It does not weaken the requirement for deliberate schema changes after Plan C.

This design supersedes the legacy-adoption and manager-confidence migration
sections of:

- `2026-07-25-open-source-temporal-engine-design.md`; and
- `2026-07-25-canonical-observation-legacy-postgres-design.md`.

Plan B remains useful evidence about the frozen PoC representation, but its
legacy codec and digest constraints are transitional rather than permanent
architecture.

## Goals

- Persist the complete public core contract without narrowing it to the PoC
  schema.
- Give canonical core and optional directory state independent ownership.
- Keep manager confidence as a query use case rather than an installed
  database vertical.
- Embed reviewable migrations with the PostgreSQL adapter.
- Make clean installation, repeat migration, status, and drift detection use
  the same migration implementation as the application.
- Preserve immutable source evidence, independent valid and recorded time,
  append-only identity authority, derivation provenance, uncertainty, and
  counterevidence.
- Keep extraction retry, lease, admission, and idempotency behavior generic.
- Keep source, directory, and model providers optional and outside the core.
- Provide an explicit, safe local reset workflow and clearly document its data
  loss.
- Prove schema and code synchronization through synthetic unit and live
  PostgreSQL integration tests.

## Non-goals

- Migrating rows from the retired `stacks.*` schema.
- Preserving the legacy Goose ledger, legacy UUID shape, observation digest v1,
  signal digest, or manager-confidence tables for unused local data.
- Supporting an in-place upgrade from migrations `00001` through `00012`.
- Maintaining two writable schemas, a shadow projection, or a dual-write
  period.
- Treating manager confidence as a plugin, extension, installation scope, or
  permanent database vocabulary.
- Adding the generic natural-language query surface; that is Plan D.
- Installing pgvector before a vector-backed storage and query contract exists.
- Invoking Drive, Workspace Directory, Bedrock, Anthropic, or OpenAI while
  migrating or testing the database.
- Using private source contents in fixtures, logs, errors, or migrations.
- Deploying, enabling cloud logging, or changing external infrastructure.

## Chosen architecture

Plan C uses a fresh canonical schema and optional adapter-owned schemas:

```text
stacks_core          required canonical temporal evidence state
stacks_directory     optional directory snapshot and lookup state
stacks_migrations    admin-owned independent migration ledgers and fingerprints
```

There is no `stacks_managerconfidence` schema or migration ledger.

The ownership direction is:

```text
application and use cases -> PostgreSQL adapter -> core contracts
directory adapter -------> PostgreSQL directory scope + core identity input
Drive adapter -----------> core document input
model adapters ----------> generic extraction contracts

core -X-> SQL drivers, migration tools, provider SDKs, manager confidence,
          environment configuration, or operator policy
```

PostgreSQL is the first durable adapter, not a requirement of the public core.
Its packages translate between relational rows and canonical values without
exporting SQL or driver types into the core.

## Core schema ownership

`stacks_core` owns only reusable temporal-engine state.

### Time precision

Every persisted core timestamp uses one canonical UTC microsecond policy,
including document metadata, source time, evidence recorded time, valid and
recorded observation time, extraction lifecycle, identity authority, and
admission history.

Normalization happens at the domain construction boundary before a stable
digest, idempotency key, or SQL row is produced. The PostgreSQL adapter rejects
values that bypass that policy; it never truncates silently. Document,
evidence, observation, decision, and derivation digests use only the normalized
values. Plan C versions any digest whose prior input admitted nanoseconds.

### Documents and evidence

The core schema stores:

- provider-neutral source documents with a provider and provider document ID;
- immutable document versions with stable content identity and captured source
  metadata;
- a source-owned pointer to the current version;
- ordered, generically named content sections with provider-native role
  metadata where available;
- immutable exact evidence spans with stable IDs, UTF-8-safe boundaries,
  digest, source locator, and recorded time; and
- generic source time separate from ingestion and provider-modified time.

Document versions and evidence payloads are immutable. Repeated ingestion of
the same provider version and content identity is idempotent. A provider
revision is preserved as provenance and must not silently redefine the stable
content digest.

### Identity

The core schema stores:

- canonical entities;
- source-grounded mentions;
- aliases and their assertion provenance;
- resolution proposals;
- ranked candidates with generic candidate provenance;
- append-only reviewer decisions; and
- explicit supersession between decisions.

Candidate sources may include a directory, model, heuristic, or operator, but
the core candidate does not hold a foreign key to an optional adapter schema.
An adapter may retain its own source record and provide an opaque source
reference as candidate provenance.

A unique exact work-email match may be proposed automatically. A name-only
match remains a review candidate. Once a reviewer links an identity, that
effective decision is authoritative until another explicit reviewer decision
supersedes it. Missing directory information never invalidates an existing
decision.

### Processing lifecycle

The core schema stores generic extraction runs with:

- owning document version;
- stable derivation digest and idempotency identity;
- recorded time;
- method, code/schema version, model, and prompt version;
- configured provider and data-disclosure mode as provenance;
- lease owner, lease expiry, attempt state, and bounded failure code; and
- completion state.

Lease acquisition is atomic. Expired work may be reclaimed. A retry with the
same derivation identity either resumes or proves the same completed durable
result. No document version is marked complete before all required evidence,
mentions, identity inputs, observations, and admission decisions commit.

### Canonical observations

Observation IDs and referenced core IDs are opaque, nonblank text values.
PostgreSQL does not impose UUID semantics on the public domain.

An observation row stores:

- the stable observation ID;
- an explicit subject term shape;
- the exact predicate;
- an explicit object term shape;
- explicit temporal kind and bound presence;
- recorded time;
- derivation method, version, run ID, model, and prompt version as independent
  fields;
- epistemic status;
- optional finite confidence value and its declared scale;
- a versioned full-payload canonical digest; and
- immutable creation metadata required for audit and retry comparison.

Subject and object terms use closed tagged shapes:

- absent;
- exact text;
- source-grounded mention;
- canonical entity; or
- canonical entity with its grounding mention.

Kind-specific columns and database checks make every other combination
invalid. Text terms are bounded by core validation rather than coerced into an
entity or predicate.

Temporal storage distinguishes:

- unknown;
- instant;
- bounded interval;
- open interval since a start;
- open interval until an end; and
- uncertainty window.

Intervals and windows are half-open where bounded. The temporal kind is stored
explicitly; it is never reconstructed from nullable bounds.

Valid time says when the proposition applied in the source world. Recorded
time says when Stacks recorded it. They are independent filters and neither is
substituted for the other.

Observation time values use the shared UTC microsecond policy. Temporal
digests and equality operate on the normalized UTC instant.

Observation-to-evidence rows carry an explicit `supporting` or
`contradicting` role. Their primary identity includes observation, evidence,
and role, so the same evidence may legitimately participate in both roles.
New observations are cited. The clean baseline has no `legacy_uncited`
storage exception.

Legacy-only domain accommodations are removed with the retired storage path:

- new observations cannot set `LegacyUncited`;
- derivations cannot set `LegacyUnversioned`; and
- a stored confidence value must declare a current, meaningful scale rather
  than `unspecified_legacy`.

If a future source genuinely lacks evidence, derivation version, or confidence
semantics, it requires a domain decision with its own epistemic meaning rather
than reuse of a PoC compatibility flag.

The canonical digest is versioned and covers the complete semantic payload,
including tagged terms, temporal kind and bound presence, derivation,
epistemic status, confidence scale, and sorted evidence-role pairs. It does not
preserve the obsolete PoC digest merely to match discarded rows.

### Admission history

Admission is append-only authority, not a mutable truth flag on immutable
payloads.

Generic admission decisions identify their target, outcome, reason code,
recorded time, authority, and optional superseded decision. Effective state is
derived from the unsuperseded decision. This permits quarantine, later
admission, or retirement without rewriting the observation, mention, or
extraction payload that was evaluated.

Database constraints and transaction tests enforce at most one effective
decision for a target and preserve decision history.

## Manager confidence is a use case

Manager confidence is one question that can be asked of the temporal graph. It
is not an installation item.

Plan C translates useful PoC behavior into canonical concepts:

- interaction categories become predicates or bounded query policy;
- direction and claimed state become typed observation content;
- rationale remains derived presentation grounded in cited evidence;
- supporting and contradicting passages use generic evidence roles;
- extraction and analysis provenance use generic derivation fields;
- quarantine uses generic admission decisions; and
- pair selection uses generic identity and temporal inputs.

No manager-specific table, trigger, migration, or persistence DTO survives the
cutover solely for compatibility.

Until Plan D is complete, the existing manager-confidence CLI command may
remain as a specialized client of canonical storage. It may choose predicates,
pairing rules, and report presentation, but it receives no private persistence
contract. Plan D introduces the generic cited temporal query boundary. Once
that boundary provides equivalent behavior and its acceptance tests pass, the
specialized command and remaining manager-specific application code are
removed.

The synthetic manager-confidence scenario remains an end-to-end acceptance
case, not a product boundary.

## Optional directory scope

`stacks_directory` is the only optional database scope in Plan C.

It owns provider snapshots, entries, exact-email lookups, and the adapter state
needed for bounded retry and audit. Directory records may propose generic core
identity candidates. Directory tables may reference core IDs, but no core
table or core migration references a directory table.

Directory enrichment is additive:

- disabled directory access is healthy;
- absent results do not block ingestion;
- denial, throttling, stale snapshots, and provider failure do not invalidate
  core state;
- name-only candidates remain review-only;
- only a unique exact work-email result may auto-resolve under configured
  policy; and
- reviewer decisions remain authoritative after directory data changes.

The directory migration scope is installed only when directory enrichment is
configured. Its absence is not drift or failure for a core-only installation.

## Provider boundaries

Drive is an optional document-source adapter. Bedrock, Anthropic, and OpenAI
are interchangeable model-provider adapters. They are runtime composition
choices, not migration scopes.

Provider-specific request types, OAuth types, credentials, region settings,
quota policy, and disclosure policy remain outside core and PostgreSQL
migration code. Generic derivation records retain bounded provider, model, and
prompt provenance without giving those values authority over observations.

Migration, doctor, and database integration tests must not construct or invoke
provider clients.

## Migration manifests and ledgers

Reviewable SQL files are embedded in the PostgreSQL adapter binary. The
directory adapter embeds its own PostgreSQL migration files. A manifest for
each scope contains ordered version, name, and SHA-256 checksum values.

The independent ledgers are:

```text
stacks_migrations.core_version
stacks_migrations.directory_version
```

Each ledger records only successfully applied versions, with migration name,
checksum, and application time. A failed command returns its bounded failure
class to the operator but leaves the version pending. Version `1` in one scope
cannot satisfy version `1` in another. Core is always installed before an
optional scope.

All scoped migration operations acquire one fixed, named PostgreSQL advisory
lock. Migration SQL and its ledger update are transactional. A failed
migration leaves neither partial schema state nor a successful ledger entry.
Repeating an already-current migration performs no writes.

Once the new baseline has shipped, applied SQL and checksums are immutable.
Later schema changes append a new forward migration in the owning scope.

`stacks_migrations` is admin-owned. The application role receives only the
read access needed for readiness and doctor status. Runtime application
credentials cannot apply migrations or alter migration metadata.

### Schema fingerprints

Each scope owns an explicit object manifest. Shared-table ownership is not
inferred from a table name.

A deterministic fingerprint covers the scope-owned:

- schemas and tables;
- columns, types, nullability, and defaults;
- primary, unique, check, and foreign-key constraints;
- indexes;
- functions; and
- triggers.

Canonical serialization is stable under catalog row order and excludes
volatile OIDs, owners, and ACL ordering. PostgreSQL-major-specific differences
are explicit because the local runtime is pinned to PostgreSQL 18.

The expected fingerprint ships with the same adapter version as the embedded
migrations. Migration integration tests build a clean database and prove its
live fingerprint matches that expectation. Doctor recomputes the live
fingerprint and reports bounded drift without attempting repair.

## Commands and operator behavior

Repository commands remain the supported entry point.

- `make db-up` starts the local PostgreSQL container.
- `make db-migrate` applies the embedded core manifest and the explicitly
  configured optional scopes.
- `make db-status` reports independent scope status.
- A new explicit local reset command removes only this Compose project's named
  PostgreSQL data volume, recreates the service, and applies the configured
  scopes.

The reset command:

- requires an explicit non-interactive confirmation value;
- prints which local Compose service and named volume will be reset;
- refuses an unresolved or non-local target;
- never interpolates a broad filesystem path;
- warns that local stored data is unrecoverable; and
- is documented as the only transition from the retired PoC chain.

`stacks doctor` remains read-only. It reports each scope as one of:

- absent;
- pending;
- current;
- checksum mismatch; or
- schema drift.

Core absence or non-current state is a readiness failure. Directory absence is
healthy when directory enrichment is disabled and actionable when it is
configured. Doctor never applies, repairs, resets, or adopts a database.

## Application cutover

Plan C is one coherent storage cutover, not a dual-write rollout:

1. introduce the embedded scoped migrator, manifests, status model, and
   read-only fingerprint inspection;
2. add the fresh core and directory baselines;
3. implement complete canonical codecs and repositories;
4. port ingestion, leases, retry/resume, identity, evidence, observations, and
   admission to those repositories;
5. port the temporary manager-confidence client to canonical storage;
6. update composition, configuration, doctor, Make targets, and documentation;
7. reset the local database and install the selected scopes; and
8. delete the retired migration path, legacy codec, manager-specific storage,
   and obsolete tests in the same completed branch.

Intermediate commits must build and test, but the final branch has one writable
schema path. The cutover is not complete while application code can still write
the retired PoC representation.

## Error handling and observability

Core validation errors describe the failed invariant without including private
text. The PostgreSQL adapter wraps operations while preserving cancellation,
deadline, and conflict identity for callers that need a behavioral decision.

Migration errors identify the scope, version, and bounded failure class. They
must not include SQL connection strings, passwords, raw documents, prompts, or
provider payloads.

Structured logs and spans cover meaningful migration, storage transaction,
lease, ingestion, and optional-provider boundaries. They may include stable
operational IDs, scope, version, duration, retry state, and bounded decision
codes. They must not include private document content, evidence quotes,
prompts, credentials, authorization headers, or embeddings.

No cloud logging is enabled by Plan C.

## Verification

### Unit and contract tests

Tests prove:

- manifests have ordered, unique versions and stable checksums;
- every fingerprinted object has exactly one owner;
- fingerprint serialization is row-order independent;
- each owned semantic mutation changes its fingerprint;
- volatile catalog fields do not affect fingerprints;
- every tagged term shape round-trips;
- every temporal shape round-trips without inference;
- UTC microsecond normalization is explicit and deterministic for every
  persisted timestamp;
- document and evidence values retain exact round-trip equality and stable
  digests after time normalization;
- derivation fields remain independent;
- confidence value and scale round-trip together;
- supporting and contradicting evidence pairs remain distinct;
- canonical digest inputs and sorting are stable;
- identity decisions are append-only and supersedable;
- admission decisions preserve quarantine and later admission history; and
- no core package imports provider, SQL, manager-confidence, configuration,
  logging, or telemetry packages.

### PostgreSQL integration tests

With `STACKS_TEST_DATABASE_URL`, tests prove:

- a clean core-only install contains no manager-confidence, directory, OAuth,
  model-provider, or unused vector objects;
- a composed core-plus-directory install succeeds;
- repeated migration is a no-op;
- pending, checksum-mismatch, and schema-drift status are distinguished;
- failed migration application rolls back schema and ledger state;
- two migration processes serialize under the same advisory lock;
- application-role status inspection works without migration authority;
- document/version/evidence persistence is immutable and idempotent;
- extraction leases support expiry, reclaim, retry, and completed-run resume;
- all identity proposal, candidate, decision, alias, and supersession
  invariants hold;
- every canonical observation and evidence-role shape round-trips;
- quarantine and later admission do not rewrite payloads;
- required completion writes are atomic;
- optional directory absence and failure do not block core processing;
- cancellation and deadlines preserve `errors.Is`; and
- database paths do not construct or invoke model or source providers.

### Repository gates

Before Plan C is complete, run and report:

```text
make fmt
make test
make test-integration
make test-race
make staticcheck
make build
make db-status
```

Also run the explicit clean reset/install, repeat migration, doctor database
checks, and an independent whole-branch review. Exact repository command names
may be added by Plan C, but documentation and CI must use the same entry points.

Passing local tests proves the canonical engine and local PostgreSQL adapter.
It does not prove live Google Drive, Workspace Directory, Bedrock, Anthropic,
OpenAI, or private-corpus acceptance.

## Risks and controls

**Risk:** the reset is mistaken for a production-safe upgrade.

**Control:** require explicit reset confirmation, document data loss, and state
in the PR that migrations `00001` through `00012` have no upgrade path.

**Risk:** manager-confidence vocabulary leaks back into core.

**Control:** give it no schema or migration ownership, keep its policy in a
temporary use-case layer, and retain a second synthetic longitudinal scenario
unrelated to manager confidence.

**Risk:** an optional adapter becomes required through a foreign key.

**Control:** allow extension-to-core references only; core never references an
optional schema.

**Risk:** embedded SQL, expected fingerprints, and runtime code drift.

**Control:** ship manifests and fingerprints with the owning adapter and prove
them against clean live PostgreSQL in integration tests.

**Risk:** the all-at-once storage cutover becomes too large to review.

**Control:** implement in buildable, invariant-focused commits with
task-scoped reviews, then require an independent whole-branch review before
delivery.

**Risk:** data is silently lost at timestamp or digest boundaries.

**Control:** define UTC microsecond precision and canonical digest inputs once,
validate them in core and storage, and test every supported shape.

## Completion criteria

Plan C is complete only when:

- the database can be created from embedded core migrations without the
  retired chain;
- core-only and core-plus-directory installations are independently
  inspectable and current;
- PostgreSQL stores the complete canonical evidence, identity, derivation,
  temporal, confidence, and counterevidence contract;
- ingestion, leases, retry/resume, admission, and identity use only canonical
  storage;
- no manager-confidence database object or migration scope remains;
- the temporary manager-confidence command reads and writes only canonical
  contracts;
- the running application has no writable dependency on the retired schema;
- reset, migration, status, and doctor documentation match real commands;
- all deterministic and live PostgreSQL verification gates pass; and
- remaining live provider and private-corpus acceptance is reported separately.
