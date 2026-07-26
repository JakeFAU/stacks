# Canonical Observation over Legacy PostgreSQL Design

**Date:** 2026-07-25
**Status:** Approved design
**Scope:** Plan B

> **Plan C update:** The repository has no supported installation base. The
> approved
> [Canonical PostgreSQL Reset Design](2026-07-25-canonical-postgres-reset-design.md)
> replaces the legacy-adoption roadmap below with a deliberate local database
> reset and fresh canonical baselines. This document remains the record of Plan
> B's temporary compatibility contract.

## Purpose and roadmap

Plan B makes `core/observation.Observation` the active observation contract at
the PostgreSQL boundary while migrations `00001` through `00012` remain frozen.
It introduces one codec that is lossless relative to the frozen durable
representation, routes active ingestion through that codec, and removes the
parallel durable storage DTO and duplicate domain validation. It makes no
schema change.

This is an intentionally bounded compatibility step:

- **Plan B:** canonicalize the active observation/PostgreSQL boundary over the
  frozen legacy schema. Decode every characterized valid legacy shape and
  reject characterized corrupt pairings with bounded compatibility errors.
  Encode only the explicitly representable canonical subset. Preserve the
  signal tables as a vertical extension.
- **Plan C:** reset the disposable development database into fresh embedded
  core and optional directory baselines, independent migration ledgers,
  migration fingerprints, and complete canonical PostgreSQL storage.
- **Plan D:** expose deterministic temporal reading through a
  production-facing, read-only, cited query boundary.

Plan B must not imply that the legacy schema can represent the complete
canonical model.

## Boundary and ownership

The SQL boundary owns exactly one codec between canonical observations and the
legacy relational shape. The codec is responsible for:

1. decoding legacy rows and related evidence/signal rows into canonical
   observations plus their unchanged signal extension and private relational
   compatibility metadata;
2. checking whether a canonical observation is representable by the legacy
   schema;
3. encoding representable observations without changing legacy bytes or
   semantics; and
4. reproducing the existing observation digest v1 exactly.

`core/observation` remains the sole owner of observation invariants and domain
validation. Storage owns relational integrity, compatibility checks, SQL, and
transaction behavior. Ingestion may retain a key-based pre-persistence draft
because durable mention and evidence UUIDs do not exist before the completion
transaction. That draft is not a second durable observation type, must not
cross the SQL boundary, and must not duplicate canonical validation.

The existing ingestion completion transaction remains the unit of atomicity.
No new repository lifecycle or transaction boundary is introduced.

## Design invariants

- Canonical observation construction succeeds before any observation or signal
  row is written.
- `core/observation` is the only owner of domain validity; storage adds only
  legacy representation and relational checks.
- Every characterized valid legacy shape decodes without information loss when
  the unchanged signal vertical and private relational origin metadata are
  considered with the canonical observation.
- Every accepted Plan B write decodes to the same canonical observation and
  signal extension while reproducing the same relational evidence origin.
- Active observations are cited; only historical decoded rows may use
  `LegacyUncited`.
- No compatibility mapping invents term text, temporal uncertainty, confidence
  scale, evidence role, or derivation metadata.
- Codec losslessness begins after ingestion has constructed the generic durable
  canonical observation. It does not preserve pre-persistence confidence-scale
  semantics that the frozen schema cannot store.
- Digest v1 bytes and their exact `observation_evidence` origin are immutable.
  Canonical evidence-pair deduplication must not erase that private origin.
- Idempotent observation and completed-run retries compare the complete expected
  legacy write-set, not canonical equality alone.
- Observation, evidence, signal, extraction-run, and document-version changes
  either commit together or all roll back.

## Write data flow

The active write path is:

1. When creating an extraction run, `PrepareVersion` captures its observation
   time once, applies
   `UTC().Truncate(legacyPostgresTimestampPrecision)`—where the named precision
   is `time.Microsecond`—and writes that instant to
   `extraction_runs.recorded_at`. Existing runs retain their stored instant.
2. Preparation state returns that exact run `RecordedAt` to ingestion along
   with the durable run identity.
3. Extraction produces a pre-persistence observation draft using local mention
   and evidence keys, the returned run `RecordedAt`, and the validated bounded
   source confidence; completion does not read a new observation clock.
4. `CompleteVersion` begins its existing transaction and persists evidence and
   mentions.
5. Local keys are resolved to durable evidence and mention UUIDs. The active
   observation-evidence origin is reconstructed as the current distinct union
   of statement, supporting, and contradicting citation IDs.
6. The completion path constructs `core/observation.Observation` using those
   durable identifiers, supporting links for every origin ID, signal role
   links, the run `RecordedAt`, and an explicit `unspecified_legacy`
   confidence carrying the unchanged source number. The signal extension
   separately retains the source score's bounded application meaning.
7. The SQL-boundary codec checks legacy representability, encodes the legacy
   row and relationships, and computes digest v1 from the exact private origin
   set.
8. Storage inserts the observation, the origin set in
   `observation_evidence`, and the existing signal vertical extension.
9. The extraction run and document version are marked complete and the
   transaction commits.

Any failure rolls back the entire existing completion transaction. The
completion path and SQL adapter must not call `time.Now` for observations or
normalize their time. `core/observation.NewObservation` already normalizes the
caller's instant to UTC, so the storage boundary treats that canonical UTC
instant as authoritative and rejects sub-microsecond values that PostgreSQL
would truncate. The caller's original location is not retained and therefore
cannot be revalidated by storage.

## Legacy codec

### Terms

The codec maps legacy subject and object columns independently:

| Legacy entity UUID | Legacy mention UUID | Canonical term |
|---|---|---|
| null | null | `Absent` |
| null | set | `Mention(mentionID)` |
| set | null | `Entity(entityID, "")` |
| set | set | `Entity(entityID, groundingMentionID)` |

Decoding must preserve the entity-plus-grounding-mention shape. Encoding
supports exactly these four shapes. `Text` terms are not representable and must
be rejected; they must never be coerced into a predicate, entity, or mention.

### Valid time

Legacy bounds map as follows:

| `valid_start` | `valid_end` | Canonical temporal extent |
|---|---|---|
| null | null | `Unknown` |
| set | null | `Interval(Since)` |
| set | same instant | `Instant` |
| set | later instant | `Interval(During)` |

An end without a start and an end before a start are invalid legacy shapes.
Encoding supports `Unknown`, `Instant`, `Interval(Since)`, and
`Interval(During)`. `Interval(Until)` and uncertainty `Window` are not
representable. A bounded interval must never be reinterpreted as an uncertainty
window.

### Evidence and roles

The decoded carrier retains a private, sorted, distinct copy of the exact
`observation_evidence` ID set. This origin set is not a second domain evidence
model: it exists only to reproduce digest v1, verify legacy rows, and compare
retries. It remains intact even when canonical pair deduplication collapses a
duplicate supporting relationship.

Canonical evidence is decoded from both relationship tables:

- each `observation_evidence` row contributes a `supporting` link;
- each `signal_evidence` row contributes its stored role;
- exact `(evidence ID, role)` pairs are deduplicated and canonically sorted;
- evidence IDs are not deduplicated across different roles.

The characterized legacy cases are:

| Relational shape | Canonical evidence | Private origin |
|---|---|---|
| observation-only ID | one supporting link | contains the ID |
| signal-only ID | the signal's role link | empty |
| supporting ID in both tables | one supporting link | contains the ID |
| contradicting ID also stored as observation evidence | one supporting and one contradicting link | contains the ID |
| same ID in both signal roles | one supporting and one contradicting link | empty unless the ID is also in observation evidence |

The same evidence ID may therefore appear once as supporting and once as
contradicting. A contradiction duplicated through `observation_evidence`
legitimately has both roles. This records faithful legacy relationships, not an
inference that the evidence is substantively both supportive and
contradictory.

For the same-ID-both-signal-roles case, an absent observation relationship
leaves the private origin empty. If the ID also occurs in
`observation_evidence`, canonical evidence remains the same two pairs while the
private origin contains the ID.

Active ingestion preserves the current relational behavior exactly:
`observation_evidence` receives the distinct union of statement, supporting,
and contradicting citation IDs, while `signal_evidence` retains its exact
role pairs. The canonical observation contains supporting links for that exact
origin set plus all signal role links. Plan C may stop this dual storage only
through an explicit migration and behavior decision.

Generic frozen-schema writes without a signal use their canonical supporting
IDs as the observation origin and reject contradicting links. With a signal,
every origin ID must have a canonical supporting link, every signal role must
appear in canonical evidence, a supporting link outside the origin must be
backed by signal support, and every contradicting link must be backed by signal
contradiction. These checks preserve observation-only and signal-only origins
without inventing relationships. Canonical pair deduplication never changes the
supplied origin.

### Uncited legacy rows

A legacy row with no relationships in either evidence table decodes with
`LegacyUncited=true`. Historical uncited rows remain readable and are not
rewritten merely because they are read.

Active writes must reject `LegacyUncited` and must include canonical evidence.
Plan B does not create new uncited observations.

### Derivation and vertical signal metadata

The legacy representation is recovered without normalization:

- `Derivation.Method` is the exact `observations.derivation` string.
- `Derivation.RunID` is `observations.extraction_run_id` when present.
- For a row owned by an extraction run, `Derivation.Version` is the owning
  run's `prompt_version`, `Model` is its `model_id`, and `PromptVersion` is its
  `prompt_version`.
- For a historical row without an extraction run,
  `LegacyUnversioned=true`, `Version` and `RunID` are empty, and model/prompt
  metadata may be recovered from a signal only when both values are present.

The Version-to-prompt-version mapping is a temporary, explicit compatibility
rule. Plan C stores derivation fields independently and removes this
restriction.

If a historical signal's model/prompt differs from its owning extraction run,
the canonical observation derivation still comes from the run and the signal
metadata remains unchanged in the vertical extension. The mismatch is not
discarded or rewritten. It is a compatibility error only when an active write
claims that the signal and observation share one derivation.

For active encoding, the canonical `RunID` must equal the owning extraction
run. Version and prompt version must equal that run's prompt version, and model
must equal that run's model ID. When a signal exists, its model and prompt must
match the extraction run. Model and prompt must always be present as a pair.
Canonical derivations outside these recovery rules are not representable in
Plan B.

Signal category, direction, rationale, model, prompt, confidence, and role
evidence remain in the existing vertical signal tables. Plan B neither moves
nor generalizes them.

### Confidence

Active extraction continues to validate source model confidence as a bounded
`unit_interval` value. The manager-confidence signal vertical retains that
application-level meaning and the exact numeric score.

The frozen generic observation table has no confidence-scale column. Before
constructing the generic durable canonical observation, ingestion therefore
performs an explicit temporary downgrade: it keeps the exact numeric value but
constructs confidence as `unspecified_legacy`. The ingestion boundary owns this
conversion; the SQL codec must not infer or perform it.

Generic decoding is independent of signal presence:

- null `observations.confidence` decodes as absent;
- every finite non-null value decodes as `unspecified_legacy`, regardless of
  numeric range or an associated signal; and
- signal confidence remains separate vertical data.

Signal presence never recovers a generic confidence scale. Null observation
confidence with non-null signal confidence is valid. Unequal observation and
signal confidence values remain losslessly visible in their respective legacy
locations; they are not generic compatibility corruption.

The generic frozen-schema encoder accepts absent or explicitly
`unspecified_legacy` finite confidence. A `unit_interval` canonical observation
presented directly to that encoder returns
`ErrObservationNotRepresentable`. Plan C adds `confidence_scale`, removes the
ingestion downgrade, and permits direct lossless storage of generic confidence
semantics.

### Exact strings and timestamps

Predicate and derivation strings are stored and compared byte-for-byte. The
codec must not trim, case-fold, Unicode-normalize, or otherwise rewrite them.
Canonical constructors may reject structurally invalid values, but accepted
values retain their exact bytes.

Timestamps use the existing PostgreSQL UTC and microsecond semantics.
`PrepareVersion` normalizes and stores the extraction-run instant once, and
returns it through preparation state for canonical observation construction.
`core/observation.NewObservation` supplies canonical UTC instants. The codec
rejects sub-microsecond values that PostgreSQL would silently truncate or
round; it cannot and does not recover the caller's pre-construction location.
No timestamp is normalized inside completion or the codec. Equality compares
UTC instants at stored precision, not Go location or monotonic-clock metadata.

## Digest v1 compatibility

Plan B preserves the existing digest bytes exactly. The digest input is the
NUL-separated sequence, in this order:

1. canonical extraction-run UUID;
2. canonical subject entity UUID;
3. canonical object entity UUID;
4. canonical subject mention UUID;
5. canonical object mention UUID;
6. exact predicate bytes;
7. exact derivation bytes;
8. exact epistemic-status bytes;
9. valid start in UTC `RFC3339Nano`, or empty;
10. valid end in UTC `RFC3339Nano`, or empty;
11. confidence formatted with `%.17g`, or empty; and
12. the exact sorted, deduplicated UUIDs originating in
    `observation_evidence`, one field per UUID.

Empty optional UUIDs are empty fields. Fields are joined with `\x00` and hashed
with SHA-256. Signal-only evidence and signal roles are intentionally absent.
The stable observation ID and `RecordedAt` are intentionally absent. The codec
must never substitute the canonical evidence union for the private observation
origin. The decoded carrier retains the stored digest alongside that origin for
verification and retry comparison. There is no new prefix, version marker,
normalization rule, or ordering change.

Golden tests must pin representative pre-Plan-B digest bytes. The existing
function may be moved behind the codec, but its output is immutable.
Every decode recomputes v1 from the stored row and private origin; a difference
from the stored digest is a compatibility error.

## Idempotency and conflict behavior

Digest v1 remains the semantic uniqueness key for compatibility, but it is not
a complete retry-equality check because it excludes `RecordedAt`, signal-only
evidence, and roles.

On a stable-ID conflict, storage must load the complete row, both evidence
relationship sets, and associated signal metadata; decode the stored canonical
observation and compatibility metadata; and compare:

- stable ID;
- subject, predicate, and object, including grounding mention;
- temporal kind and bounds;
- exact `RecordedAt`;
- sorted evidence `(ID, role)` pairs;
- the exact private `observation_evidence` origin set;
- the stored digest, its recomputation from stored row/origin, and the expected
  retry digest v1 bytes;
- complete derivation;
- epistemic status;
- confidence presence, numeric value, and scale; and
- `LegacyUncited`; plus
- the complete optional signal extension.

Equality accepts an idempotent observation retry; any difference returns a
conflict. In particular, changing only `RecordedAt`, a signal role, a
signal-only evidence ID, generic confidence presence/value, vertical signal
confidence, or relational origin conflicts even when digest v1 is unchanged.

The existing unique digest constraint continues to reject the same digest
under a different stable ID. Signal idempotency and signal-digest comparison
remain independently enforced.

A completed extraction-run retry is a larger write-set comparison. When
`CompleteVersion` locks a run already marked complete by the same owner, it
must not return early. It reconstructs the complete expected durable write-set
from the supplied completion using read-only identity resolution, then compares:

- evidence-span identities and immutable payload digests;
- mention identities, evidence bindings, original completion-owned resolution
  rows, and immutable fields;
- canonical observations, private origin/digest metadata, and signal
  extensions;
- version/run association and data mode; and
- completion status, completed owner, admissibility, and the applicable current
  document-version pointer.

Reconstruction uses the persisted extraction-run `RecordedAt`, never a new
clock value. Only complete equality accepts the retry. The completed-run path
performs no insert, update, current-pointer repair, or other fresh write. A
different owner or any write-set difference returns a conflict.

Identity data remains additive after ingestion. The comparison requires every
proposal, candidate, automatic decision, and alias assertion created by the
original completion to remain present with its original immutable payload and
digest. It permits lifecycle fields to reflect later append-only authority,
including proposal status and decision supersession links. It does not reject
later directory candidates, reviewer decisions, aliases, directory assertions,
or other append-only enrichment that was not part of the original completion.
Such later evidence neither rewrites nor excuses a mismatch in
completion-owned payload.

## Interfaces and removal

The implementation should expose a narrow, package-local SQL codec contract
equivalent to:

```go
decodeLegacyObservation(row, evidenceRows, signalRows) (decodedLegacyObservation, error)
encodeLegacyObservation(observation.Observation, compatibility, owningRun, signal) (legacyWrite, error)
computeObservationDigestV1(legacyWrite) ([32]byte, error)
```

These names and concrete carrier structs are implementation details. The
decoded result contains the canonical observation plus the unchanged optional
vertical signal and private exact observation-evidence origin/stored-digest
metadata; it is not another domain observation DTO. The metadata cannot escape
the storage compatibility path or be reconstructed from canonical evidence
pairs. The important boundary is that public repository completion accepts a
canonical observation, the exact observation-evidence origin entering the
storage compatibility path, and its optional existing signal extension so
stable-ID retry equality can cover the complete write-set atomically. The
origin is not returned as domain evidence or reconstructed from canonical
pairs. Unexported row/write carriers contain only SQL mechanics.

After all callers use the codec:

- remove storage's durable `ObservationInput`;
- remove duplicate storage validation of status, confidence, and temporal
  invariants;
- remove obsolete digest adapters after their callers move to the codec; and
- retain relational compatibility checks that canonical validation cannot
  express, such as representability, owning-run consistency, and stored-row
  corruption.

## Error semantics and privacy

The boundary distinguishes three behaviors:

- `ErrObservationNotRepresentable`: a valid canonical value cannot be encoded
  by the frozen legacy schema;
- `ErrObservationCompatibility`: stored legacy data or vertically paired active
  input violates the characterized compatibility contract; and
- `ErrObservationConflict`: a retry or uniqueness collision differs from
  stored state.

Fixed privacy-safe reason codes include
`observation_origin_mismatch`, `observation_digest_mismatch`,
`confidence_scale_not_representable`, and
`completion_owner_mismatch`, and `completion_write_set_mismatch`. Origin,
digest, and write-set reasons map to compatibility or conflict according to
whether stored data or a retry exposed the mismatch.
`completion_owner_mismatch` maps to `ErrObservationConflict`.
`confidence_scale_not_representable` maps to
`ErrObservationNotRepresentable`.

Errors must preserve `errors.Is`/`errors.As` behavior through operation-context
wrapping. Context cancellation and deadlines pass through unchanged. Database
errors retain their cause.

Error text is bounded and privacy-safe. It may include the failed operation, an
observation or extraction-run UUID, and a fixed reason code. It must not include
predicate or text-term contents, evidence text, prompt contents, model output,
rationale, document contents, or raw SQL values.

## Observability

Plan B adds no spans, metrics, or deep storage logging. Codec and SQL errors
return to the existing ingestion-completion boundary, which owns request/job
observability. Stable operational identifiers may be attached there under the
repository's current policy.

No observation content, private source payload, prompt, embedding, or evidence
text may be added to logs, trace events, metric attributes, or errors.

## Test-driven implementation matrix

Implementation proceeds test-first across these claims:

| Level | Required proof |
|---|---|
| Codec unit | all four term shapes in each position; entity plus grounding mention; every valid-time mapping; all statuses; derivation/run recovery; exact strings; uncited decode; private origin retention; observation-only, signal-only, duplicated support, contradiction duplicated through observation support, and same-ID-both-roles evidence shapes |
| Confidence unit | source manager scores remain validated `[0,1]`; ingestion preserves the number while constructing durable `unspecified_legacy`; every finite generic stored value decodes as `unspecified_legacy` with or without a signal; null generic plus non-null signal is valid; unequal generic/signal values remain separate; direct generic `unit_interval` encoding is not representable |
| Representation unit | reject text terms, `Until`, windows, unsupported timestamp precision, unpaired model/prompt, run/model/prompt mismatches, direct generic `unit_interval`, active evidence-origin mismatch, unsupported role ownership, and active uncited writes |
| Digest golden | exact v1 bytes for representative null/entity/mention/time/confidence/origin combinations; signal-only evidence, role changes, canonical union changes, and `RecordedAt` do not change v1 when the observation origin is unchanged; changing origin changes v1 |
| Repository integration | decode every characterized valid historical shape and return bounded compatibility errors for characterized corruption; preserve stored digest and origin independently of canonical pair dedup; canonical-plus-compatibility write/read round trip; exact direct-caller `RecordedAt`; stable-ID retry success; conflicts for origin, stored digest, `RecordedAt`, role, generic or vertical confidence value, signal-only evidence, or another write field; different ID with same digest conflicts |
| Ingestion integration | preparation returns the once-stored extraction-run `RecordedAt`; canonical construction occurs after durable ID resolution; current observation origin remains the statement/support/contradiction union; manager score validation and vertical meaning remain unit interval while durable generic confidence is explicitly downgraded to `unspecified_legacy` with the same number; partial failures roll back |
| Completed-run retry | same-owner exact write-set succeeds without SQL mutation; evidence, mention, observation, origin/digest, signal, data-mode, owner, admissibility, or current-pointer mismatch conflicts; different owner conflicts |
| Compatibility | vertical signal fields, evidence dual storage, notes-only trigger behavior, and frozen migration checksums remain unchanged |

PostgreSQL integration must run against the existing `00001`-through-`00012`
schema. Final verification is:

```text
make fmt
make test
make staticcheck
```

plus the repository's gated PostgreSQL integration suite with its documented
test database configuration. No live provider or private-corpus acceptance is
required for Plan B.

## Rollout and removal sequence

1. Add codec characterization, confidence, evidence-origin, representation,
   completed-run retry, and digest-golden tests.
2. Implement read decoding for every characterized valid legacy shape and
   bounded rejection for characterized corruption, retaining private origin and
   stored-digest metadata.
3. Return the once-persisted extraction-run `RecordedAt` through preparation
   state and route direct graph-storage tests through canonical observations.
4. Resolve durable IDs and construct canonical observations plus exact active
   origin metadata inside completion.
5. Route active writes and observation retries through the codec while
   preserving the explicit confidence downgrade, evidence dual storage, and the
   signal vertical extension.
6. Replace completed-run same-owner early success with read-only expected
   write-set reconstruction and complete comparison.
7. Remove the duplicate durable storage DTO, duplicate domain validation, and
   obsolete digest adapters.
8. Run unit, integration, formatting, and static analysis checks against the
   frozen schema.

There is no runtime dual-writer, feature flag, or compatibility switch. Each
change must remain buildable, and the completed Plan B state has one active
observation/PostgreSQL path.

## Risks and controls

- **Accidental lossy encoding:** an explicit representation check rejects
  unsupported canonical kinds before SQL is attempted.
- **Digest drift:** golden byte tests pin v1 and keep its input independent of
  new canonical fields; private origin prevents canonical pair deduplication
  from changing digest input.
- **False idempotency:** observation retries compare canonical, signal, and
  compatibility state; completed-run retries compare the full expected durable
  write-set without writing.
- **Evidence double-counting or origin loss:** pair-level deduplication
  preserves canonical roles, while private metadata independently preserves the
  exact legacy observation origin and current dual storage.
- **Hidden confidence downgrade:** ingestion performs and tests the downgrade
  explicitly before generic canonical construction; the vertical signal keeps
  the bounded-score meaning and numeric value. The codec never guesses scale,
  and Plan C removes the downgrade with `confidence_scale`.
- **Invented temporal certainty:** bounded valid time never becomes an
  uncertainty window.
- **Retry-time drift:** observations use the owning run's once-persisted
  `RecordedAt`, so completion and retries never read a fresh observation clock.
- **Derivation ambiguity:** active writes require exact owning-run,
  model/prompt, and temporary version mappings; unsupported forms wait for
  Plan C.
- **Privacy leakage:** errors use fixed reason codes and exclude private
  payloads.
- **Transaction regression:** canonical construction and all writes stay within
  the existing completion transaction.

## Acceptance criteria

Plan B is complete only when:

- migrations `00001` through `00012` and their checksums are unchanged;
- one SQL-boundary codec decodes every characterized valid legacy observation
  shape and returns bounded compatibility errors for characterized corruption;
- active ingestion constructs canonical observations after durable IDs exist;
- only the explicitly documented canonical subset can be encoded;
- predicate/derivation bytes, exact observation-evidence origin, digest v1, and
  vertical signal behavior round-trip exactly;
- canonical `RecordedAt` is the owning extraction run's once-persisted
  `recorded_at`, while direct graph callers provide a canonical instant whose
  precision is already representable;
- source and vertical manager-confidence scores retain their validated
  `unit_interval` meaning and exact number, while generic durable observations
  explicitly use `unspecified_legacy`;
- observation retry equality covers canonical, signal, origin, and stored
  digest state;
- completed-run same-owner retry compares the complete expected durable
  write-set and performs no fresh writes;
- historical uncited rows remain readable while new uncited writes are
  rejected;
- the storage observation DTO and duplicate domain validation are removed;
- failures remain atomic, bounded, and privacy-safe; and
- all required unit, PostgreSQL integration, formatting, and static analysis
  checks pass.

## Explicit nonclaims

Plan B does not add or modify a migration, migration ledger, embedded baseline,
adoption transaction, fingerprint, CLI command, provider, module boundary,
release artifact, or production query API. It does not move the signal
vertical. It does not claim complete canonical PostgreSQL storage.

In particular, Plan B does not persist text terms, uncertainty windows,
`Until` intervals, independent derivation versions, observation-evidence roles
outside the signal vertical, or generic explicit confidence scales. It
intentionally does not preserve pre-persistence `unit_interval` semantics in
the generic durable observation: ingestion downgrades that scale to
`unspecified_legacy` while preserving the number and the signal vertical.
Plan C stores `confidence_scale` and removes this limitation.

Plan B also does not expose deterministic temporal reading to users. The
production-facing, read-only, cited query boundary is Plan D.
