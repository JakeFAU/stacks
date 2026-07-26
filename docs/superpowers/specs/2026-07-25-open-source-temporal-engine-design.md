# Open-Source Temporal Evidence Engine

**Date:** 2026-07-25
**Status:** Approved architecture, amended by Plan C
**Scope:** Establish an enforceable open-source core for versioned evidence,
identity authority, temporal observations, and deterministic longitudinal
interpretation while retaining the existing Stacks application and
manager-confidence workflow as downstream consumers.

> **Plan C update:** The repository has no supported installation base and its
> local database is intentionally disposable. The approved
> [Canonical PostgreSQL Reset Design](2026-07-25-canonical-postgres-reset-design.md)
> supersedes this document's legacy-adoption, manager-confidence migration,
> and permanent-example assumptions. Plan C uses fresh core and optional
> directory baselines; manager confidence is a temporary query use case, not an
> installation scope.

## Product thesis

Stacks is an auditable temporal interpretation engine for document
collections.

Its reusable product contract is:

```text
versioned source material
  -> immutable evidence
  -> proposed observations and identities
  -> deterministic validation
  -> append-only identity authority
  -> canonical temporal observations
  -> deterministic aggregation and comparison
  -> bounded conclusions with citations, uncertainty, and counterevidence
```

The core question is not merely which passages resemble a query. It is:

> What was asserted, by whom, from which evidence, at what valid time, when was
> it recorded, how has the supported state changed, and what contradicts it?

Manager-confidence analysis is one useful demonstration of this machinery. It
does not define the open-source core. Google Drive, Google Workspace Directory,
PostgreSQL, Bedrock, Anthropic, and OpenAI are adapters or deployment choices.

## Context

The current repository already contains the essential primitives:

- immutable document versions and exact evidence spans;
- stable content digests and preserved provider provenance;
- proposed identities, aliases, append-only decisions, and supersession;
- valid time distinct from recorded time;
- source-grounded observations with epistemic status and derivation;
- deterministic temporal planning, aggregation, conflict preservation, and
  comparison;
- provider-neutral model generation requests and responses;
- resumable PostgreSQL-backed ingestion.

Those primitives are presently hidden under `internal`, share one Go module
with every provider SDK, and are not connected through one canonical
observation path. The running application remains centered on a
manager-confidence use case. Generic temporal query code is tested but not used
by the production ingestion path, while ingestion and storage use parallel,
narrower observation data-transfer types.

The objective is therefore boundary extraction and integration, not a rewrite.

## Goals

- Make the temporal evidence engine consumable as a public Go module.
- Enforce a dependency boundary that prevents provider SDKs, deployment policy,
  and vertical analysis from entering the core.
- Preserve the current working application throughout the transition.
- Use one canonical observation contract from validated extraction through
  persistence and deterministic query.
- Preserve exact evidence, derivation, valid time, recorded time, temporal
  uncertainty, epistemic status, and counterevidence end to end.
- Keep identity proposals separate from authoritative reviewer decisions.
- Keep source, model, directory, storage, and operator behavior behind explicit
  consumer-owned contracts.
- Establish fresh canonical database history through the approved destructive
  early-development reset, then use forward-only, compatibility-tested
  migrations.
- Retain manager-confidence temporarily as a query acceptance case until the
  generic query boundary replaces it.
- Prepare the repository for a legally and operationally responsible
  open-source release.

## Non-goals

- Rewriting the working manager-confidence implementation from scratch.
- Splitting the project into separate Git repositories during this work.
- Building a dynamic plugin loader, provider registry, workflow DSL, generic
  graph database, or ORM.
- Making model output, directory data, provider ranking, embeddings, or
  confidence scores authoritative.
- Inventing a universal ontology or unrestricted property-value system.
- Adding a web interface.
- Adding natural-language temporal query planning or uncited narration.
- Providing an in-place upgrade from the retired PoC migration chain.
- Invoking live model providers, reading private source contents, deploying,
  enabling cloud logging, or changing external repository settings as part of
  the boundary extraction.
- Claiming live Google Drive, Workspace Directory, Bedrock, or private-corpus
  acceptance based on deterministic or local PostgreSQL tests.

## Chosen repository shape

Use one Git repository with multiple Go modules.

This creates a hard dependency line without prematurely introducing a second
repository, duplicate implementation, cross-repository release coordination,
or divergent histories.

The target shape is:

```text
stacks/
  core/
    go.mod
    evidence/
    observation/
    identity/
    temporal/
  adapters/
    model/
      go.mod
    googleauth/
      go.mod
    postgres/
      go.mod
      migrations/core/
    drive/
      go.mod
    directory/
      go.mod
      migrations/postgres/
    anthropic/
      go.mod
    bedrock/
      go.mod
    openai/
      go.mod
  app/
    go.mod
    cmd/stacks/
    internal/
  go.work
```

The exact filesystem move will be incremental. Each commit must leave the
repository buildable and tested. The final dependency direction is:

```text
app -> adapters -> core
app ------------> core

core -> Go standard library and reviewed foundational dependencies
model adapters -> adapters/model
Drive and directory adapters -> adapters/googleauth
core -X-> provider SDKs
core -X-> manager-confidence
core -X-> operator configuration
core -X-> deployment disclosure policy
```

The core module path will be
`github.com/JakeFAU/stacks/core`. Each adapter family and the application own
separate modules under the same repository namespace. This keeps a PostgreSQL
consumer from inheriting AWS or Google dependencies and lets adapters evolve
independently. Manager confidence remains temporary application-level query
policy, not a module or installation boundary.

The core is standard-library-first, not artificially standard-library-only.
The existing `golang.org/x/text/unicode/norm` dependency remains permitted
because Unicode NFKC normalization is part of the accepted-alias compatibility
contract. Every core dependency requires an explicit architectural reason and
must remain free of providers, persistence, logging, telemetry, and deployment
policy.

The repository root uses `go.work` for local development. Nested modules use
path-prefixed semantic-version tags such as `core/v0.1.0` and
`adapters/postgres/v0.1.0`. Before the first coordinated release, local
workspace development may temporarily require relative replacements in
downstream modules. A public release is not complete until:

- the core has been tagged with explicit user approval;
- downstream `go.mod` files refer to published module versions;
- no relative `replace` directives remain;
- every module is tidy and builds from a clean module cache outside `go.work`.

This sequencing avoids pretending an unpublished nested module can already be
resolved independently.

## Core package responsibilities

Public packages are organized around domain responsibility rather than the
current application lifecycle.

### `evidence`

Owns immutable source material and exact support:

- provider-neutral source and document identifiers;
- immutable document versions;
- stable, ordered content identity;
- source locator and captured source metadata;
- provider version and optional revision provenance;
- recorded time;
- generic source time, without meeting-specific naming;
- ordered content sections;
- exact UTF-8-safe evidence spans;
- evidence digests and compatibility identities.

The current tab-aware implementation remains supported. A tab becomes one kind
of ordered content section rather than a Google-specific or meeting-specific
core concept. Source adapters may preserve native section structure and roles
as metadata. A manager-confidence query use case may interpret a section as a
transcript; the core does not.

`SourceMeetingTime` becomes a generic explicit source-time observation. Drive
modified time remains provider metadata and must not silently become valid
time.

### `observation`

Owns immutable, evidence-backed propositions:

- stable observation identity for retry idempotency;
- typed subject and object terms that can contain bounded text, an unresolved
  mention, or a resolved entity together with its optional grounding mention;
- a typed predicate;
- valid time, including unknown, instant, interval, and uncertainty window;
- recorded time;
- one or more immutable evidence links with supporting or contradicting roles;
- derivation method and version;
- optional model and prompt provenance;
- epistemic status;
- optional finite confidence score and its declared scale, preserved as model
  metadata rather than truth.

A term is a closed tagged value, not `any` or arbitrary JSON. It represents
only the forms already earned by the implementation: absent for unary
observations, exact text, a source-grounded mention reference, or a canonical
entity reference that may retain the grounding mention reference. Keeping both
entity and mention preserves the evidence grounding after resolution. Numeric,
boolean, time-valued, composite, or provider-specific objects may be added only
after a real use case establishes their invariants.

The public epistemic vocabulary includes every status already accepted by the
database: observed, inferred, hypothesized, structurally validated,
empirically validated, and rejected. Confidence never promotes one status to
another. New normalized confidence uses an explicit unit-interval scale.
Every stored confidence value declares a current meaningful scale; generic
logic must not compare values from incompatible scales or calibrate them as
probabilities.

The core constructor validates all required invariants and returns an immutable
value. Persistence adapters consume and return this canonical type rather than
parallel observation DTOs.

### Observation persistence contract

The clean canonical baseline stores the public observation contract directly.
It has no compatibility carrier, manager-specific evidence table,
`LegacyUncited`, `LegacyUnversioned`, or `unspecified_legacy` confidence state.

PostgreSQL round-trip tests cover every tagged term, epistemic status, temporal
kind and open-bound shape, evidence role, derivation field, and supported
confidence scale. The versioned canonical digest covers the complete semantic
payload. The exact relational contract is defined by the
[Canonical PostgreSQL Reset Design](2026-07-25-canonical-postgres-reset-design.md).

### `identity`

Owns revisable identity and authority:

- entities and kinds;
- source-grounded mentions;
- normalized alias claims;
- resolution candidates;
- proposed links and their evidence;
- append-only decisions;
- authority and provenance;
- supersession and current effective state;
- ambiguity rather than guessing when multiple accepted aliases match.

Provider directory records are evidence used to construct candidates. They are
not core entities and do not become authoritative without the deterministic
policy or reviewer action configured by the consuming application.

The initial resolver may remain optimized for person names and emails where
those are the only earned rules. Generality comes from the authority contract,
not from pretending all entity kinds share identical normalization.

### `temporal`

Owns deterministic reading over time:

- query plans using independent valid-time and recorded-time filters;
- deterministic observation selection;
- aggregation of agreeing support;
- preservation of conflicting values and provenance;
- uncertainty-aware chronology;
- before-and-after comparison;
- recorded-time knowledge cutoffs;
- explicit causal traversal only when supported by causal observations.

Temporal selection never chooses a winner by confidence alone. Temporal
precedence never implies causality. Results expose the observations and
evidence required for citation.

### Internal orchestration

The first extraction keeps orchestration internal to the core module while it
connects the public primitives:

```text
immutable document version
  -> exact evidence set
  -> untrusted proposals
  -> deterministic validation
  -> canonical observations and identity candidates
  -> atomic durable write
```

Consumer-owned interfaces remain narrow:

- an extractor proposes structured, evidence-referencing output;
- a validator converts untrusted output into domain proposals;
- a store atomically preserves the document version, evidence, proposals, and
  canonical observations;
- an identity resolver consults effective authority without hiding new writes;
- a clock is injected where recorded time is assigned.

Source discovery and provider fetching remain outside core orchestration. They
produce immutable `evidence.DocumentVersion` values. Human review remains an
application operation over append-only identity decisions. Model transport
remains an adapter concern.

The orchestration package does not own retries for provider I/O. Adapters own
bounded transport retries; application jobs own operation retries; stable
identifiers and transactions make the durable core safe to retry.

It becomes a public `engine` package only after the unrelated synthetic
commitment example uses the same orchestration without manager-confidence
knowledge and demonstrates the minimal stable interface. Until then, only the
immutable domain and deterministic temporal packages are public. This avoids
freezing the current coupled ingestion lifecycle as an open-source API.

## Model boundary

The existing provider-neutral structured-generation interface moves into the
small shared `github.com/JakeFAU/stacks/adapters/model` module and is separated
from manager-confidence prompt contracts. Provider adapters consume this
module; it does not belong in core because model transport is an optional edge
capability.

The generic adapter contract contains:

- reviewed system instructions supplied by the caller;
- schema name and immutable JSON Schema bytes;
- private input;
- bounded usage and latency metadata;
- returned model identity;
- raw untrusted structured output;
- provider-neutral authentication, authorization, throttling, cancellation,
  and availability errors.

Adapters must not contain a registry of application prompt versions. They
validate only transport-level requirements and provider capabilities. Prompts,
schemas, deterministic output validation, and conversion to observations live
with the application use case that owns their meaning.

Private input, prompts containing private content, model output, credentials,
and raw provider errors must not enter ordinary logs, metrics, traces, or error
strings.

## Source and directory adapters

The existing installed-application OAuth mechanism moves into the focused
`github.com/JakeFAU/stacks/adapters/googleauth` module. Drive and Directory
consume it without importing one another or duplicating security-sensitive
token handling.

Google Drive implements a provider-neutral versioned source boundary. Its
tab-aware extraction behavior remains an adapter capability, while transcript
selection and meeting chronology remain temporary manager-confidence query
policy until the generic query boundary replaces them.

Google Workspace Directory implements an optional identity-evidence boundary.
Its data may add candidates. Disabled, denied, throttled, or unavailable
directory access must not prevent document preservation or generic temporal
processing.

The core has no Google OAuth types, scopes, token paths, folder IDs, directory
resource names, or IT deployment policy.

## PostgreSQL adapter

PostgreSQL is the first durable implementation, not a core requirement.

The adapter must persist the canonical domain contract without narrowing it:

- temporal kind is stored explicitly;
- instant, open interval, bounded interval, and uncertainty window remain
  distinguishable;
- valid-time bounds retain their presence semantics;
- caller-supplied recorded time is preserved;
- epistemic status and optional finite confidence value plus scale are
  preserved;
- derivation method, code version, extraction-run identity, model, and prompt
  version are preserved;
- observation-to-evidence links are immutable and complete;
- repeated writes with stable identities are idempotent;
- document, evidence, identity, and observation writes that form one durable
  operation are transactionally explicit.

Storage packages must not import manager-confidence analysis types, Google
directory SDK types, or operator disclosure policy. Adapter-specific records
are translated at their owning boundary.

## Migration transition

Plan C deliberately resets the disposable development database. It does not
adopt or upgrade migrations `00001` through `00012`.

The PostgreSQL adapter embeds a fresh canonical core baseline and owns
`stacks_migrations.core_version`. The optional directory adapter embeds its
own baseline and owns `stacks_migrations.directory_version`. There is no
manager-confidence migration scope.

The full migration, fingerprint, reset, doctor, safety, and verification
contract is defined by the
[Canonical PostgreSQL Reset Design](2026-07-25-canonical-postgres-reset-design.md).
After that baseline is established, applied migrations are immutable and later
schema changes are forward-only.

## Manager-confidence transitional use case

Manager confidence is one question that can be asked of the temporal graph,
not a permanent example module, database vertical, or installation choice.

During Plan C, its existing command may remain as specialized application query
policy over canonical storage. It has no private tables, migrations, or storage
DTOs. Plan D introduces the generic cited temporal query interface. Once that
interface provides equivalent behavior, the specialized command and remaining
manager-specific application code are removed.

The synthetic manager-confidence workflow continues to test chronology,
identity, supporting and contradicting evidence, admission, and bounded
conclusions until the generic interface replaces it.

## Second synthetic proof

Before declaring the public core stable, add one small, fully synthetic
longitudinal example unrelated to manager-confidence. The preferred proof is a
project-commitment timeline:

- a commitment first appears in one dated document;
- responsibility changes in a later document;
- a later document contradicts an earlier assumption;
- identity is linked by an explicit review decision;
- an as-of query shows what was known before and after the contradiction;
- output cites exact evidence and preserves both support and counterevidence.

This example uses no live provider and no private source. Its purpose is to
expose accidental coupling, not to create another product vertical.

## Application and configuration

The operator application remains responsible for:

- environment configuration;
- OAuth and credential locations;
- provider selection;
- disclosure policy;
- CLI presentation;
- process lifecycle;
- structured logging and telemetry wiring;
- local database and migration commands;
- provider readiness checks.

The application may provide privacy-safe defaults and an executable disclosure
policy. Those defaults are valuable reference behavior, but they do not alter
the core domain contract.

Core packages accept explicit values and interfaces. They do not read
environment variables, construct provider clients, initialize telemetry, or
choose models.

## Error handling

Core errors describe invariant or operation failures without private payloads.
Typed or sentinel errors exist only when a caller needs a behavioral decision,
such as conflict, stale authority, or idempotent duplicate.

Provider adapters translate vendor errors into a small bounded set while
preserving cancellation. Raw private request or response bodies are not wrapped
into returned errors.

Applications decide whether an optional adapter failure is fatal. Generic
durable processing fails if a required atomic write fails; optional directory
enrichment remains additive.

## Observability

Core operations expose stable, privacy-safe attributes and decision outcomes
through narrow observer interfaces only where a real consumer needs them. The
core does not depend on Zap or OpenTelemetry.

The application and adapters retain the established observability rules:

- no raw documents, evidence text, prompts, model output, identities, email
  addresses, credentials, or provider response payloads;
- stable operational identifiers only when safe;
- low-cardinality metrics;
- explicit operation outcome, duration, retry, and bounded error class;
- successful manually owned spans marked `OK`.

## Public release hygiene

The repository is currently source-visible but has no license, so it is not yet
a complete open-source release.

Before announcing or tagging a release:

- choose and add an OSI-approved license; Apache-2.0 is the recommended default
  because it includes an explicit patent grant;
- add `SECURITY.md`, `CONTRIBUTING.md`, and a concise code of conduct;
- rewrite the README around the temporal evidence engine;
- describe manager confidence only as a temporary query acceptance case;
- document the disclosure boundary for every provider adapter;
- add synthetic quick-start data;
- run a history-wide secret scan with a pinned tool;
- audit dependencies and generated artifacts;
- make each module testable outside `go.work`;
- add PostgreSQL integration coverage for scoped migrations;
- document which live provider and private-corpus acceptance remains
  intentionally unvalidated;
- review GitHub description, topics, release settings, and security features
  separately before changing them.

Adding a license and changing external repository metadata require explicit
confirmation during implementation. No external settings change is implied by
this design approval.

## Incremental implementation sequence

The detailed execution plan will split this design into independently reviewed
tasks. The intended dependency order is:

1. Lock characterization tests around current public-worthy invariants and
   dependency direction.
2. Establish the workspace and core module with provider-neutral evidence,
   observation, identity, and temporal packages.
3. Introduce one canonical observation path and make PostgreSQL preserve the
   full temporal contract.
4. Wire deterministic temporal reading into a production-facing, read-only
   boundary.
5. Separate generic model transport from application prompt contracts.
6. Split PostgreSQL, Drive, directory, Anthropic, Bedrock, and OpenAI adapter
   families into independently owned modules.
7. Keep manager-confidence policy temporarily in the application as a client
   of canonical contracts.
8. Add the fresh embedded core and optional directory migrations, explicit
   local reset, independent ledgers, and schema fingerprints defined by Plan C.
9. Add the second synthetic longitudinal proof.
10. Promote core orchestration publicly only if that proof establishes the
    interface.
11. Update documentation and release hygiene, with the license choice handled
    as an explicit user decision.
12. With explicit push and tag approval, publish the core first, replace local
    development references with published versions, then prove every
    downstream module outside `go.work`.
13. Run independent task reviews and an independent whole-branch review.

Each task must be test-first, preserve a buildable state, and avoid broad
mechanical moves that obscure semantic changes.

## Verification strategy

Every implementation task runs the smallest relevant tests first, followed by
the repository gates appropriate to its scope.

The final branch must pass:

```bash
make fmt
make test
make test-race
make staticcheck
make build
make db-up
make db-migrate
make db-status
make test-integration
```

It must additionally prove:

- the core module has no imports from adapters, applications, Zap,
  OpenTelemetry, SQL drivers, or provider SDKs;
- the core module builds and tests outside the workspace;
- the repository Make targets explicitly cover every module rather than
  relying on root `./...` patterns that skip nested modules;
- core-only and core-plus-directory clean installs both pass;
- retry and resume remain idempotent;
- extraction leases and admission quarantine remain intact;
- versioned canonical digest behavior remains stable;
- identity authority and alias lifecycle remain append-only and deterministic;
- temporal extent kind and recorded time round-trip through PostgreSQL;
- the temporary manager-confidence query uses only canonical persistence and
  retains its synthetic acceptance behavior;
- the synthetic commitment example produces cited, time-aware results with
  preserved counterevidence.

After the explicitly approved coordinated module tags, the release gate also
proves every downstream module from a clean module cache outside `go.work` with
no relative replacement directives.

Live Google Drive, live Workspace Directory, Bedrock quota, provider disclosure
acceptance, and private-corpus acceptance are reported separately. Passing
local and PostgreSQL integration never implies those live boundaries passed.

## Risks and controls

### Accidental rewrite

**Risk:** moving packages and changing contracts simultaneously obscures
regressions.

**Control:** characterize first, move in small steps, and separate mechanical
moves from semantic changes where practical.

### Public API frozen too early

**Risk:** exporting every current type creates a large unstable contract.

**Control:** export only immutable domain values and narrow earned interfaces;
keep application orchestration details private until a second use demonstrates
them.

### Migration divergence

**Risk:** embedded migrations, expected fingerprints, and runtime codecs drift.

**Control:** ship one embedded, checksummed SQL history per scope and compare
its clean live schema with the expected fingerprint in integration tests.

### Core contaminated by adapters

**Risk:** convenience imports reintroduce provider or deployment coupling.

**Control:** separate modules, import-boundary tests, and independent module
builds.

### Query-use-case behavior silently changes

**Risk:** translating manager confidence onto canonical storage weakens its
admission, counterevidence, privacy, or retry guarantees.

**Control:** retain existing behavioral and PostgreSQL tests, then run an
independent use-case regression review.

### Open-source release exposes sensitive history

**Risk:** source visibility is mistaken for a completed privacy review.

**Control:** synthetic fixtures only, pinned history-wide secret scanning,
dependency review, disclosure documentation, and no private-corpus use.

## Acceptance criteria

The boundary extraction is complete when:

- a consumer can depend on `github.com/JakeFAU/stacks/core` without provider,
  database, logging, telemetry, or manager-confidence dependencies;
- immutable evidence, canonical observations, identity authority, and temporal
  comparison are public, documented, and deterministic;
- PostgreSQL round-trips the full canonical temporal contract;
- the working Stacks application uses the public core rather than a duplicate
  internal model;
- the temporary manager-confidence command uses canonical contracts and its
  synthetic acceptance behavior still passes;
- one unrelated synthetic example demonstrates longitudinal cited reasoning;
- the documented destructive local reset creates the fresh canonical baseline;
- clean core databases do not contain vertical or provider schemas;
- repository verification, race tests, Staticcheck, build, migration tests,
  PostgreSQL integration, and independent reviews pass;
- public documentation distinguishes deterministic/local verification from
  still-unvalidated live provider and private-corpus acceptance;
- the user has explicitly selected the release license and approved any
  external repository metadata changes.
