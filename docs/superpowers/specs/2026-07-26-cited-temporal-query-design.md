# Cited Temporal Query Design

**Date:** 2026-07-26
**Status:** Approved design
**Scope:** Plan D

## Purpose

Plan D exposes the existing canonical temporal model as a production-facing,
read-only query boundary. It turns admitted, versioned evidence into bounded,
deterministic, cited temporal results without treating document retrieval,
models, or a manager-specific interpretation as the public product.

The first delivered operation is trend comparison. The public contract is
defined now for all four supported temporal intents so later point-in-time,
trajectory, and explicit causal-chain work extend a stable shape rather than
reopen its semantics.

```text
resolved identifiers + explicit temporal request
  -> one historical knowledge snapshot
  -> admitted canonical observation projection
  -> deterministic temporal core operators
  -> typed facts, changes, conflicts, gaps, and exact citations
  -> CLI text or JSON
```

This design follows Plan C's canonical PostgreSQL reset. It does not restore
the retired manager-confidence schema, migration ledger, or installation
concept. Manager confidence remains a temporary application use case and a
generic evidence-parity acceptance case only.

## Goals

- Provide one typed, provider-neutral, read-only temporal query contract.
- Preserve the four existing temporal intents: point-in-time, trend comparison,
  trajectory, and explicit causal chain.
- Implement trend comparison first against canonical PostgreSQL observations.
- Return exact supporting and contradicting evidence provenance, not merely
  source document references or uncited prose.
- Keep valid time independent from recorded time and make historical
  `known-as-of` answers reconstruct the whole then-effective knowledge state.
- Require stable resolved entity IDs at the query boundary; identity matching is
  a separate, reviewable operation.
- Keep aggregation, comparison, chronology, conflict handling, and causal
  eligibility deterministic and model-free.
- Provide a useful CLI text rendering and a stable JSON rendering of the same
  typed result.
- Make limits explicit and fail rather than silently truncate chronology.
- Prove behavior with synthetic Project Atlas evidence in unit and PostgreSQL
  integration tests.
- Prove generic evidence parity before removing the temporary
  manager-confidence command and application policy.

## Non-goals

- Natural-language question parsing, intent classification, entity matching, or
  model-generated narration.
- Invoking Google Drive, Workspace Directory, Bedrock, Anthropic, OpenAI, or
  any other provider while executing a query.
- Web, HTTP, cache, pagination, background refresh, subscriptions, or query
  history.
- Vector retrieval, embeddings, similarity ranking, graph-database traversal,
  or an unrestricted query language.
- Writing observations, identity decisions, admission decisions, reviews, or
  source data through the query command.
- Treating confidence, directory information, temporal precedence, or
  extraction success as proof of truth or causality.
- New schema claims or migrations in this design. The Plan C canonical schema
  is the source of current durable facts; any schema change must be separately
  designed and migrated forward.
- Claiming live source/provider/private-corpus acceptance from synthetic or
  local PostgreSQL tests.

## Vocabulary and invariants

### Observation and evidence

An **observation** is an immutable canonical proposition with subject,
predicate, object, valid time, recorded time, epistemic status, derivation, and
one or more evidence-role links. It is not a mutable current fact.

An **evidence citation** identifies one immutable exact evidence span and its
document-version and section provenance. A citation's role is `supporting` or
`contradicting`; the same span may appear in both role-separated collections
when the durable observation data records both roles. A citation does not mean
the text has been independently verified as true.

**Admitted** means the observation's effective admission decision allows it
into the projection. Admission is independent of an observation's epistemic
status. Rejected observations are not candidates for state aggregation.

### Identity and authority

An **entity ID** is a stable, opaque, nonblank canonical identifier. Requests
accept IDs only. A display name, email, alias, mention, directory profile, or
free-form name never silently becomes an entity ID in this boundary.

Identity authority is append-only. A reviewer decision is effective until an
explicit later decision supersedes it. Exact work-email and directory matches
may inform upstream proposals; they are not query-time identity inference.

### Two independent time axes

**Valid time** is when an assertion applied in the source world. A valid-time
point selects one instant; a window is half-open `[start, end)`. Unknown valid
time and uncertainty windows remain uncertainty; they are never filled from
document, ingestion, or recorded time.

**Recorded time** is when Stacks recorded an observation or authority decision.
`known-as-of` selects what Stacks knew by a normalized UTC timestamp. It is a
whole-knowledge cutoff, not an observation-row filter alone.

All public instants use RFC 3339 timestamps and normalize to the repository's
canonical UTC microsecond policy before comparison. A zero or malformed time
is invalid.

### Result categories

A **fact** is a deterministic aggregate with all its contributing observation
IDs and role-separated citations. It is not a claim that conflicting or
uncertain material disappeared.

An **unresolved item** preserves a key, a reason, candidate values, and their
citations when the core cannot safely promote one value to state. The supported
reasons are the existing temporal vocabulary: conflicting values, multiple
states in a window, temporal uncertainty, hypothesized, and counterevidence
only.

A **gap** is a structured absence or exclusion statement, never an empty
success that looks like a negative factual conclusion. It says whether no
eligible evidence exists, candidate mention/identity/admission authority was
absent at the selected knowledge cutoff, or evidence was excluded by valid
time. Bounds are request errors, never gaps. Gaps contain no private source
payload.

## Architecture and dependency direction

Plan D adds no provider dependency to the core. The direction is:

```text
cmd/stacks
  -> internal/app
     -> internal/cli           (Cobra transport and text/JSON rendering)
          -> internal/query    (typed request/result and orchestration)
               -> core/temporal (pure plan, aggregation, comparison, ordering)
               -> core/{admission,evidence,identity,observation,timepoint}
     -> internal/query.PostgresRepository (root-module bridge)
          -> adapters/postgres (adapter-owned PostgreSQL projection)
               -> core/{admission,evidence,identity,observation,timepoint}
```

`core/temporal` owns only deterministic temporal contracts and operators. It
does not know PostgreSQL, Cobra, configuration, logs, telemetry, source
providers, or manager confidence. Existing core operations remain the basis:

| Intent | Required deterministic operations |
| --- | --- |
| point-in-time | reconstruct state |
| trend comparison | aggregate before and after windows; diff them |
| trajectory | partition timeline; aggregate windows; diff; order transitions |
| causal chain | retrieve explicit causal claims; order claims |

`internal/query` owns the application contract, request validation that is not
intrinsic to a core value, execution ordering, result projection, bounded
limits, and mapping between a repository read projection and core candidates.
It owns the small `Reader` and `ReadSnapshot` port; it does not export
PostgreSQL types through the application boundary.

`adapters/postgres` owns SQL, repeatable-read transaction mechanics, historical
identity/admission interpretation, and its adapter-owned projection methods.
It depends only on `core`, the standard library, and pgx. It returns canonical
observations plus exact adapter citation records; it does not import the root
module or any `internal` package, select a winning state, manufacture causal
claims, or render output.

Because the standalone PostgreSQL adapter cannot import root-module
`internal/query`, a root-owned bridge implements the consumer port:
`internal/query.PostgresRepository`, analogous to
`internal/analysis.PostgresRepository`, calls adapter-owned projection methods
and translates adapter records into query `ReadSnapshot` records. This one-way
bridge prevents a Go-module cycle and an illegal `internal` import. Snapshot
instrumentation belongs in the adapter projection method; the application
query span belongs in the query service.

`internal/cli` owns Cobra syntax, RFC 3339 and flag parsing, and output
rendering. It does not query SQL or implement temporal rules. `internal/app`
creates the query service lazily after command-specific configuration is
validated and only for query commands.

## Typed request contract

The application request is explicit and fully resolved before repository I/O:

```go
type Request struct {
    Intent          temporal.Intent
    EntityIDs       []identity.EntityID
    EntityMatch     EntityMatch
    Predicates      []observation.Predicate // exact optional filters
    Selections      []temporal.TemporalSelection
    KnowledgeScope  temporal.KnowledgeScope
    Limit           int
}

type EntityMatch string

const (
    EntityMatchAll EntityMatch = "all"
    EntityMatchAny EntityMatch = "any"
)
```

The final implementation may use an equivalent closed Go representation, but
must preserve these meanings and must not introduce untyped JSON properties.

Rules:

- `Intent` is one of exactly `point-in-time`, `trend-comparison`,
  `trajectory`, or `causal-chain`.
- `EntityIDs` contains one or more trimmed, unique stable IDs. Their request
  order is not semantic; the service normalizes ordering for stable results.
  They are required for every intent.
- `EntityMatch` is exactly `all` or `any`. `all` means every requested ID must
  occur among a selected observation's historically resolved subject/object
  endpoints. `any` means at least one requested ID must occur. Both statement
  positions count; v1 has no subject-only or object-only selector. A direct
  entity term counts directly. A mention term counts only after its historical
  resolution at the query cutoff. Text and absent terms never satisfy entity
  filtering.
- `Predicates` is optional. Each supplied predicate is an exact canonical
  predicate value, unique after trimming. It is a filter, not a fuzzy match,
  prefix, regular expression, or model interpretation. A causal-chain request
  permits only the sole v1 causal predicate described below.
- `Selections` use the existing core constructors and intent cardinality:
  point-in-time needs exactly one point; trend comparison exactly two ordered
  windows; trajectory and causal chain exactly one window. Trend's second
  window must start after the first window's start.
- `KnowledgeScope` is either current knowledge or a single `known-as-of`
  recorded-time cutoff. Current knowledge is explicit in the result; absence
  of a cutoff never means an implicit wall-clock value.
- `Limit` is exactly zero for point-in-time and trend comparison. It is a
  required positive bound for chronology-capable intents (trajectory and
  causal chain), constrained by the configured maximum. The service rejects a
  result that would exceed the supplied bound rather than returning a partial
  prefix.

The request cannot name an alias or an unresolved mention. Operators first use
the existing entities/review workflow to obtain authoritative IDs. An entity
absent from the entity table, or first recorded after the selected cutoff, is a
bounded typed `ErrEntityNotFound` / unknown-entity error; text rendering does
not echo the supplied ID. A known entity with no eligible observations produces
a no-evidence gap. Neither case performs a best-effort match.

### Semantic candidate mapping

The reader returns pre-valid-time candidates and coverage reasons; valid-time
selection remains in the deterministic temporal layer. A state key is the
typed pair `(canonical resolved subject term, exact predicate)`. Its state
value is the canonical resolved object term. Terms retain a closed kind tag:
entity, exact text, unresolved mention, or absent. Entity semantic identity is
the entity ID and ignores an optional grounding mention; each contribution
still preserves that grounding mention. Exact text bytes remain exact. An
unresolved mention is never coerced to text or an entity and is excluded from
state candidates with a structured coverage reason.

The implementation must add or narrow typed core candidate helpers as needed;
it must not manufacture string delimiters or concatenate terms into synthetic
keys. Coverage records distinguish a genuine no-evidence result from records
that existed but were excluded by valid time, missing historical mention
resolution, entity matching, predicate filtering, or authority.

## Selection and knowledge-cutoff semantics

### Valid-time eligibility

The repository supplies canonical observations eligible under the historical
authority snapshot; core operations apply temporal eligibility:

- a point reconstructs state at the requested valid instant;
- a window includes instants inside `[start, end)` and intervals overlapping
  it according to the canonical half-open rules;
- unknown valid time and overlapping uncertainty windows do not become facts;
  they are retained as temporal-uncertainty unresolved material when relevant;
- no ingestion, document version, provider-modified, or recorded timestamp is
  substituted for missing valid time.

### Whole-knowledge `known-as-of`

For `known-as-of = T`, the adapter evaluates all authority and evidence
eligibility as of `T` in one snapshot:

1. an entity must exist and have been recorded at or before `T`; a direct
   entity term then resolves directly and requires no resolution decision;
2. a mention term resolves only when its entity, mention, resolution proposal,
   and accepted resolution decision were all recorded at or before `T`;
3. that mention and the effective accepted identity decision must each have an
   effective admitted admission decision at `T`;
4. an observation must have been recorded at or before `T` and must have an
   effective admitted `TargetObservation` admission decision at `T`;
5. an evidence span and its document version must have been recorded at or
   before `T`; their immutable provenance is preserved;
6. for every relevant `TargetMention`, `TargetIdentityDecision`, and
   `TargetObservation` admission decision, only decisions recorded at or
   before `T` participate. A decision superseded by a successor recorded at or
   before `T` is ineffective; a successor recorded after `T` is invisible;
7. `TargetExtractionRun` does not separately gate an already admitted
   observation. Extraction derivation remains provenance, not a second query
   admission gate; and
8. current-knowledge queries use the latest effective identity and admission
   authority instead.

Thus a review correction or admission change made after `T` cannot rewrite a
historical answer. Conversely, a current query intentionally sees the current
effective decision. The cutoff does not change valid-time selection.

An absent entity, or one recorded after `T`, is a bounded unknown-entity error.
A known entity without eligible observations produces a no-evidence gap. A
historically unresolved mention contributes structured coverage rather than
becoming a substitute identity. The service does not guess a replacement ID.

## Repository read port, transactions, and cancellation

The consumer-owned port is conceptually:

```go
type Reader interface {
    Read(ctx context.Context, selection ReadSelection) (ReadSnapshot, error)
}
```

`ReadSelection` contains only normalized entity IDs, `EntityMatch`, exact
predicate filters, valid-time selections, and the knowledge scope.
`ReadSnapshot` contains:

- each requested entity's authority/gap status at the selected knowledge
  scope;
- admitted canonical observations matching the resolved entities and optional
  predicates before valid-time aggregation;
- resolved subject/object entity IDs as of the same scope;
- typed subject/object semantic terms, including grounding mentions and
  structured coverage/exclusion reasons for unresolved terms or filtered
  records;
- immutable exact evidence records with role, source document/version,
  section identity/order/path/title/role, and span identity/bounds; and
- no raw SQL, pgx, Cobra, configuration, or provider types.

One adapter projection call opens one PostgreSQL transaction with `REPEATABLE
READ`, `READ ONLY`.
Identity authority, admission authority, observations, and citations are read
within that transaction, then it commits before deterministic in-memory core
operations. This gives one coherent recorded-time state even if review or
ingestion writes continue concurrently.

The adapter passes the caller's context to transaction start, every query, row
iteration, rollback, and commit. Cancellation or deadline expiry is returned
with operation context and remains discoverable via `errors.Is`; it is neither
converted to an empty result nor retried by this boundary. The query service
does not open nested transactions or retain a transaction after repository
return.

The existing current `LoadRelationshipSnapshot` projection is useful evidence
for the required read discipline, but is pair-specific and current-authority
only. Plan D introduces the narrower general read projection rather than
stretching that API into an unbounded repository.

## Result and citation contract

Every successful result contains the normalized request metadata, the
effective knowledge scope, intent-specific material, and ordered gaps. It
contains no generated narrative or conclusion label. Gap kinds are closed:

```go
type GapKind string

const (
    GapNoEvidence        GapKind = "no-evidence"
    GapValidTimeExcluded GapKind = "valid-time-excluded"
    GapUnresolvedMention GapKind = "unresolved-mention"
    GapAuthorityExcluded GapKind = "authority-excluded"
    GapNoCausalEvidence  GapKind = "no-causal-evidence"
)
```

`GapKind` is never used for a request, configuration, or bound error. Limit
overflow is an error with no partial result.

```go
type Result struct {
    Intent         temporal.Intent
    EntityIDs      []identity.EntityID
    EntityMatch    EntityMatch
    Predicates     []observation.Predicate
    Selections     []temporal.TemporalSelection
    KnowledgeScope temporal.KnowledgeScope
    Payload        IntentPayload
    Gaps           []Gap
}
```

`IntentPayload` is a closed tagged union. Its tag equals `Intent`, and exactly
one corresponding payload is present. The JSON result has exactly one of the
keys `point`, `trend`, `trajectory`, or `causal`, never four nullable
alternatives. Constructors and renderers reject a malformed union rather than
choosing a shape by accident.

### Shared cited material

A cited fact or candidate includes:

- its exact key and value as supplied by the canonical projection;
- ordered contributing observation IDs;
- ordered supporting citations and ordered contradicting citations;
- the contributing observations' epistemic statuses and valid-time extents;
- no confidence-derived winner or confidence-based sort.

Every fact and unresolved candidate additionally contains ordered contribution
records. Each contribution ties one observation ID to its epistemic status,
valid temporal extent, recorded time, complete derivation provenance (method,
version, run ID, model, and prompt version where present), and the grounding
mention for either term where relevant. This preserves the relationship between
one aggregate value and its source observations instead of only preserving
unrelated ID lists.

Each citation includes a stable evidence ID, role, source document ID,
document version ID, section ID, section title/path/order/role, exact
UTF-8-safe span bounds, and a source locator when the immutable evidence
contract carries one. Text rendering may display the span text only when it is
already present in the in-process cited record for the requesting operator; it
must not log it. JSON follows the same access rule. The v1 contract does not
fetch source content or perform a provider call to enrich a citation.

Facts and citations preserve supporting and contradicting lists separately.
No renderer may collapse them into one unqualified `evidence` list or omit a
material counterevidence list to make output appear decisive.

JSON uses exact top-level `schema_version` value
`"stacks.temporal-query.v1"`. All timestamps are normalized to UTC
microseconds and encode as RFC3339Nano with `Z`. Every collection is encoded
as `[]`, never `null`; absent optional scalar/object fields are omitted, never
`null`. Citation `text` is included only when the already-authorized
in-process record contains it, otherwise that field is omitted. It is never
fetched as a rendering side effect. These shape rules apply equally to nested
facts, candidates, contributions, citations, and gaps.

### Normative JSON wire contract

This section is exhaustive for v1 JSON output. It fixes field names and
nesting; illustrative field order is not semantic. JSON uses typed DTO structs
and slices, never maps, except for the fixed one-key `result` union below.
Renderers reject any value that violates this contract before writing output.

The envelope is exactly:

```json
{
  "schema_version": "stacks.temporal-query.v1",
  "intent": "point-in-time|trend-comparison|trajectory|causal-chain",
  "request": {
    "entity_ids": ["..."],
    "entity_match": "all|any",
    "predicates": ["..."],
    "selections": [],
    "knowledge_scope": {},
    "limit": 0
  },
  "result": {"point|trend|trajectory|causal": {}},
  "gaps": []
}
```

`schema_version`, `intent`, `request`, `result`, and `gaps` are required.
`result` contains exactly one member: `point` for `point-in-time`, `trend` for
`trend-comparison`, `trajectory` for `trajectory`, or `causal` for
`causal-chain`. No other result member is present. `gaps` exists only at the
envelope level; no result payload, fact, transition, or causal link owns a
`gaps` member.

The normalized request has required `entity_ids`, `entity_match`,
`predicates`, `selections`, `knowledge_scope`, and `limit` fields. Arrays are
already deduplicated and canonically ordered. `entity_ids` and `predicates`
are arrays of exact nonblank canonical strings. `entity_match` is `all` or
`any`. `limit` serializes as `0` for point and trend, and as the validated
positive requested bound for trajectory and causal.

A selection is exactly one of:

```json
{"kind":"point","label":"...","at":"2026-07-01T09:00:00Z"}
{"kind":"window","label":"...","start":"2026-07-01T00:00:00Z","end":"2026-07-02T00:00:00Z"}
```

`knowledge_scope` is exactly one of:

```json
{"kind":"current"}
{"kind":"as-of","at":"2026-07-01T09:00:00Z"}
```

Every timestamp in this contract is canonical UTC microsecond precision,
encoded with RFC3339Nano and `Z`. A term is exactly one of:

```json
{"kind":"absent"}
{"kind":"text","text":"exact source bytes"}
{"kind":"mention","mention_id":"..."}
{"kind":"entity","entity_id":"..."}
```

Grounding mentions never alter the entity term and never appear in a state key
or value; they are carried only by contributions. A valid-time extent is
exactly one of:

```json
{"kind":"unknown"}
{"kind":"instant","at":"2026-07-01T09:00:00Z"}
{"kind":"interval","start":"2026-07-01T00:00:00Z"}
{"kind":"interval","end":"2026-07-02T00:00:00Z"}
{"kind":"interval","start":"2026-07-01T00:00:00Z","end":"2026-07-02T00:00:00Z"}
{"kind":"window","start":"2026-07-01T00:00:00Z","end":"2026-07-02T00:00:00Z"}
```

An `interval` has at least one of `start` or `end`; a `window` has both.
`StateKey` is always `{"subject": <term>, "predicate": "..."}`, and a state
value is always an object term, not a string encoding of either component.

A contribution is exactly:

```json
{
  "observation_id": "...",
  "status": "observed|inferred|hypothesized|validated_structurally|validated_empirically|rejected",
  "valid_time": {},
  "recorded_at": "2026-07-01T09:00:00Z",
  "derivation": {"method":"...","version":"...","run_id":"...","model":"...","prompt_version":"..."},
  "subject_grounding_mention_id": "...",
  "object_grounding_mention_id": "..."
}
```

`observation_id`, `status`, `valid_time`, `recorded_at`, and `derivation` are
required. `derivation.method` and `derivation.version` are required;
`run_id`, `model`, and `prompt_version` are omitted when absent (model and
prompt version are present together when present). Grounding mention fields are
omitted when absent.

A citation is exactly:

```json
{
  "evidence_id":"...",
  "role":"supporting|contradicting",
  "source_document_id":"...",
  "document_version_id":"...",
  "section_id":"...",
  "section_title":"...",
  "section_path":[],
  "section_order":0,
  "section_role":"...",
  "start_offset":0,
  "end_offset":0,
  "locator":"...",
  "text":"..."
}
```

All fields through `end_offset` are required. `locator` and `text` are omitted
when absent; `text` is never fetched and is included only from an authorized
in-process record. `section_path` is always an array. `start_offset` and
`end_offset` are UTF-8 byte offsets in the immutable cited section, with
`start_offset < end_offset`. Required strings are present; they are empty only
where the underlying domain explicitly permits an empty value.

A fact is exactly:

```json
{
  "key":{"subject":{},"predicate":"..."},
  "value":{},
  "contributions":[],
  "supporting_citations":[],
  "contradicting_citations":[]
}
```

`key` is a `StateKey` and `value` is an exact typed object term. An unresolved
candidate is a fact. An unresolved item is exactly
`{"key": <StateKey>, "reason": "conflicting-values|multiple-states-in-window|temporal-uncertainty|hypothesized|counterevidence-only", "candidates": [<fact>]}`.
Facts and unresolved candidates always retain role-separated citation arrays.

A change is exactly one of:

```json
{"kind":"added","key":<StateKey>,"after":<fact>}
{"kind":"removed","key":<StateKey>,"before":<fact>}
{"kind":"changed","key":<StateKey>,"before":<fact>,"after":<fact>}
```

`before` is omitted for `added`, `after` is omitted for `removed`, and both
are required for `changed`. A trajectory transition uses the same state-change
shape plus exact transition time and unresolved material:

```json
{"kind":"added|removed|changed","key":<StateKey>,"valid_time":<valid-time>,"before":<fact>,"after":<fact>,"unresolved":[]}
```

The same before/after omission rules apply. `unresolved` is always an array.
This is the minimal v1 transition representation; it does not imply an
unstated causal or state winner.

A causal link is exactly:

```json
{
  "cause":<term>,
  "effect":<term>,
  "contributions":[],
  "supporting_citations":[],
  "contradicting_citations":[]
}
```

Every causal-link contribution is from an admitted
`stacks.causal.v1/causes` observation and therefore carries that observation's
status, valid time, recorded time, and full derivation provenance. The link
arrays are never null and preserve counterevidence even when a chain is
formed.

The four result payloads are exactly:

```json
{"point":{"selection":<point-selection>,"facts":[],"unresolved":[]}}
{"trend":{"before":{"selection":<window-selection>,"facts":[],"unresolved":[]},"after":{"selection":<window-selection>,"facts":[],"unresolved":[]},"changes":[],"unresolved_keys":[]}}
{"trajectory":{"selection":<window-selection>,"transitions":[]}}
{"causal":{"selection":<window-selection>,"links":[]}}
```

`unresolved_keys` is an array of `StateKey`. Every collection shown is always
encoded as `[]`, never `null`. A gap is exactly
`{"kind":"no-evidence|valid-time-excluded|unresolved-mention|authority-excluded|no-causal-evidence","entity_id":"...","predicate":"...","selection_label":"..."}`.
Only `kind` is required; `entity_id`, `predicate`, and `selection_label` are
optional authorized context and are omitted when absent. A gap has no
free-form message or private payload.

### Point-in-time result

`PointInTimeResult` contains one selected point, resolved facts, and unresolved
items. It uses the core reconstruct-state semantics and does not pretend an
absent fact is a negative assertion. Gaps are top-level envelope material.

### Trend result

`TrendResult` contains the before and after window summaries, resolved changes
(`added`, `removed`, `changed`), unresolved keys, before/after unresolved
items. A key unresolved in either window is excluded from resolved changes and
remains visible with citations. Gaps are top-level envelope material. This is
the first implemented intent.

### Trajectory result

`TrajectoryResult` contains one selected window and a chronologically ordered,
bounded list of transitions. Each transition has a valid-time boundary or
interval, before/after cited state where available, and unresolved material.
Gaps are top-level envelope material. If the number of transitions exceeds
`Limit`, the service returns a limit error without a partial result.

### Causal-chain result

`CausalChainResult` contains a chronologically ordered, bounded list of
explicit causal observations only. The sole v1 provider-neutral causal
predicate is `stacks.causal.v1/causes`: an observation statement's subject is
the cause term and its object is the effect term. Only admitted observations
with this exact predicate qualify. A chain exists only when one canonical
effect term equals the next canonical cause term under the typed term semantics
above; temporal ordering alone, a shared entity, or a confidence score never
creates a link. Each link retains valid/recorded time, epistemic status,
supporting and contradicting citations, and full contributions. If no eligible
explicit causal observation exists, the top-level result contains a
no-causal-evidence gap, not an inferred chain. No additional causal predicates
are added in v1.

## Deterministic ordering

The service normalizes and returns collections in a documented order so equal
snapshots render identically in text and JSON:

1. entity IDs and predicate filters: lexical ascending canonical string;
2. selections: request/intent order (before then after for trend);
3. facts and unresolved keys: lexical key, then lexical value;
4. observation IDs and evidence IDs: lexical canonical ID;
5. citations within a role: source document ID, document version ID, section
   order, section ID, span start, span end, evidence ID;
6. transitions and causal links: earliest normalized valid-time bound, then
   recorded time, observation ID;
7. gaps: entity ID, gap kind, selection label, predicate.

No ordering uses Go map iteration, database physical row order, confidence,
or provider ranking. Ties are broken by the listed stable identifiers. Empty
optional fields compare as empty strings after all present values where their
kind permits them.

## CLI transport and output

The command tree adds a `query` group under the existing fresh Cobra root. It
uses leaf commands and local flags; no package-global Cobra command state is
introduced.

The smallest v1 syntax is:

```text
stacks query point --entity <entity-id> [--entity <entity-id> ...] \
  --at <rfc3339> [--entity-match all|any] [--predicate <exact-predicate> ...] \
  [--known-as-of <rfc3339>] [--output text|json]

stacks query trend --entity <entity-id> [--entity <entity-id> ...] \
  --before <rfc3339>/<rfc3339> --after <rfc3339>/<rfc3339> \
  [--entity-match all|any] [--predicate <exact-predicate> ...] [--known-as-of <rfc3339>] \
  [--output text|json]

stacks query trajectory --entity <entity-id> [--entity <entity-id> ...] \
  --between <rfc3339>/<rfc3339> --limit <positive-integer> \
  [--entity-match all|any] [--predicate <exact-predicate> ...] [--known-as-of <rfc3339>] \
  [--output text|json]

stacks query causal --entity <entity-id> [--entity <entity-id> ...] \
  --between <rfc3339>/<rfc3339> --limit <positive-integer> \
  [--entity-match all|any] [--known-as-of <rfc3339>] \
  [--output text|json]
```

`--entity` is repeatable and required. `--entity-match` accepts only `all` or
`any` and defaults to `all`. `--predicate` is repeatable and optional.
`--output` defaults to `text`; only exact `text` and `json` values are
accepted. Window flags split on exactly one `/`, parse both RFC 3339
instants, normalize them, and require end after start. Flags are parsed before
the query service receives a typed request. Unsupported combinations fail
before database access.

The causal leaf does not expose `--predicate`: it supplies the exact sole v1
predicate `stacks.causal.v1/causes`. The typed service rejects a causal request
whose predicate set is nonempty and not exactly that predicate.

Text output is concise but must show intent, selected valid-time range(s),
knowledge scope, resolved changes/facts, unresolved items, gaps, and
role-separated citations. JSON is a deterministic serialization of the typed
result with an explicit top-level `schema_version` value. JSON is a transport
representation, not a replacement domain contract; an eventual HTTP adapter
calls the typed service rather than shelling out to this CLI.

## Configuration safety limits

The query boundary uses existing typed configuration and the explicit Viper
file/environment merge rules. Plan D earns only these command-specific,
non-secret settings:

| File key | Environment variable | Default | Inclusive range |
| --- | --- | --- | --- |
| `query.max_entities` | `STACKS_QUERY_MAX_ENTITIES` | 16 | 1..64 |
| `query.max_predicates` | `STACKS_QUERY_MAX_PREDICATES` | 32 | 1..256 |
| `query.max_chronology` | `STACKS_QUERY_MAX_CHRONOLOGY` | 1000 | 1..10000 |

These values belong in `internal/config`, are non-secret and file-eligible,
and must be added to the configuration documentation/examples during
implementation. Request entity/predicate counts and chronology limits are
validated against them before database access. There is no default chronology
flag: trajectory and causal commands always require `--limit`.

`CommandQuery` requires the database URL and these query bounds, but no Google,
directory, model, or provider configuration. `stacks config validate query`
validates its settings offline and never opens a database connection. Database
credentials remain secrets in ignored environment configuration; they never
appear in files, result output, logs, or telemetry. There is no configuration
for provider calls, query-time model choice, cache, pagination, remote config,
watchers, or automatic environment discovery.

## Errors, privacy, and observability

### Error policy

- Malformed flags, unknown output mode, duplicate IDs/predicates, invalid
  timestamps, invalid intent selection, invalid window ordering, zero/negative
  bounds, and over-maximum bounds fail before database access.
- Repository unavailability, transaction failure, and cancellation return
  contextual errors while preserving the original cause.
- An entity absent from the entity table, or recorded after the cutoff, returns
  bounded typed `ErrEntityNotFound` / unknown-entity error without echoing the
  ID in text. A known entity without eligible evidence returns a successful
  no-evidence gap; neither case performs fuzzy resolution.
- A chronology or causal result exceeding its limit fails with guidance to
  narrow the window, entity set, predicate filter, or limit. It never returns
  a partial chronology.
- Internal invariant violations (for example multiple intent result shapes)
  fail loudly and are test failures; renderers do not mask them.

Errors never include raw source passages, document titles/paths, SQL text,
database URLs, credentials, prompt/model payloads, or unbounded identifiers.

### Privacy and observability

The query service creates meaningful spans only at the application query and
PostgreSQL snapshot boundaries. Successful spans are marked `OK` through the
repository observability helper. Logs and low-cardinality metrics may record
intent, output mode, whether a cutoff exists, bounded count buckets, duration,
and outcome class. They must not record entity IDs, predicates, observation or
evidence IDs, source metadata, citation text, SQL, full errors containing
private context, credentials, or provider payloads.

The result is a deliberate disclosure boundary. Citation text is shown only to
the requesting local operator through the selected renderer and is never sent
to telemetry, a model provider, or a background service.

## Phased implementation

### Phase D1: contract and pure trend execution

- Add `internal/query` typed request, result, gap, citation, renderer-neutral
  projection, and consumer-owned read port.
- Reuse core `temporal.NewPlan`, `AggregateWindow`, and
  `CompareWindowSummaries`; add only narrowly earned pure helpers required to
  project exact canonical candidates.
- Implement strict request/result validation and deterministic ordering.
- Add synthetic unit tests that prove exact citations, counterevidence,
  uncertainty, conflict preservation, gaps, cutoffs, and no confidence winner.

### Phase D2: historical PostgreSQL trend reader

- Implement the general read-only repeatable-read PostgreSQL projection with
  effective identity and admission authority as of current knowledge or an
  explicit cutoff.
- Keep SQL behind `adapters/postgres`; prove no adapter/driver type crosses
  into `internal/query` or `core`.
- Add PostgreSQL integration acceptance over synthetic Project Atlas data.

### Phase D3: CLI trend transport

- Add `stacks query trend` with the exact v1 syntax, strict flag parsing,
  human text output, and deterministic JSON output.
- Compose it lazily in `internal/app`; no query command constructs a source,
  directory, model, or web dependency.
- Test text/JSON semantics without a real listener or provider.

### Phase D4: remaining intents

- Add point-in-time, trajectory, and causal leaves using the already-approved
  request/result contract.
- Implement each only after its deterministic core operation and synthetic
  acceptance rules are proven. Causal chains remain restricted to explicit
  causal observations.

### Phase D5: generic parity and manager-confidence removal

- Run the generic query boundary over a synthetic scenario equivalent in
  temporal evidence shape to the manager-confidence acceptance case.
- Remove the specialized command and remaining manager-confidence application
  policy only after the parity gate below passes in CI and local PostgreSQL
  acceptance.

## Acceptance and test plan

The synthetic Project Atlas corpus is expanded without private content. It
contains immutable note versions and exact spans showing:

- an initial hypothetical delivery commitment;
- a later observed changed commitment;
- supporting and contradicting evidence for one claimed state;
- an explicit responsibility transfer between two reviewed, stable person IDs;
- a reviewed identity linkage whose later recorded-time visibility changes an
  `as-of` projection but not an earlier one;
- at least one admitted `stacks.causal.v1/causes` observation with a linked
  next cause/effect term and role-separated counterevidence;
- unknown/uncertain valid time that remains unresolved;
- current and historical admission decisions, including an effective decision
  changed after an earlier cutoff; and
- no-evidence and authority-gap requests.

Unit tests must cover:

- every request validation and CLI parsing rule;
- all four intent plan shapes, even while only trend executes end-to-end;
- half-open valid-time boundaries and independent recorded-time cutoffs;
- deterministic output across reordered repository input;
- exact supporting versus contradicting citation ordering;
- conflicts, hypotheses, counterevidence-only material, temporal uncertainty,
  no-evidence gaps, authority gaps, and limit failures;
- no confidence-based state selection;
- causal rejection when input has chronology but no explicit causal observation;
- positive causal-chain linkage, counterevidence, historical cutoff behavior,
  and chronology-only rejection for the sole v1 causal predicate;
- renderer parity: text and JSON expose the same facts, uncertainty, gaps, and
  citations without logging source content; and
- cancellation/error preservation through the reader.

PostgreSQL integration tests must cover:

- one `REPEATABLE READ`, read-only snapshot produces one coherent result under
  synthetic concurrent authority changes;
- effective identity and admission decisions are evaluated as of the cutoff,
  including later supersession invisibility;
- admitted canonical observations and immutable citation records round-trip
  into trend candidates without SQL/provider type leakage;
- exact valid-time and recorded-time behavior for Project Atlas;
- `EntityMatchAll` across the two reviewed generic-parity entities and
  `EntityMatchAny` across the Project Atlas reviewed people;
- existing migration status and all schema fingerprints remain clean; and
- no query operation writes rows, acquires extraction leases, invokes a
  provider, or requires private fixtures.

The normal deterministic gates remain `make fmt`, `make test`,
`make test-race`, `make staticcheck`, `make build`, `make modules-check`, and
`git diff --check`. PostgreSQL acceptance uses the documented local database
commands and ignored local environment values. Passing those tests does not
validate Google Drive, Workspace Directory, Bedrock, Anthropic, OpenAI, or
private-corpus behavior.

## Generic parity and removal gate

The parity case is deliberately generic. It proves that the Plan D contract
can return the same **chronology**, exact **supporting evidence**, exact
**counterevidence**, unresolved **conflicts/uncertainty**, and explicit
**gaps** as the temporary manager-confidence acceptance scenario when supplied
equivalent canonical observations and reviewed identities.

Parity does **not** require generic results to emit manager-specific labels,
scores, classifications, conclusions, or prose. It does not encode a manager
role, confidence concept, transcript type, or vertical schema vocabulary in
the generic API. The generic result is considered equivalent when its cited,
time-aware evidence structure preserves everything an application-level
manager-confidence interpretation needs to derive its own bounded view.

Removal is permitted only when all of the following are true:

1. the generic synthetic parity test passes through the public typed service
   and CLI/JSON transport;
2. its PostgreSQL integration variant passes against the canonical local
   schema;
3. point/trend/trajectory/causal contract tests remain deterministic and
   provider-free;
4. the specialized `analyze` CLI leaf/path, `STACKS_EMPLOYEE_ENTITY_ID` and
   `STACKS_MANAGER_ENTITY_ID` configuration/action inputs, manager-only
   application/service/repository/signal/policy code and prompt/schema,
   composition/telemetry/documentation/Make targets specific to analysis have
   been deleted;
5. no runtime/current documentation/package retains manager-specific labels or
   conclusions. Historical design documents remain as historical records, and
   the generic synthetic parity fixture remains with generic names;
6. a whole-branch review confirms no generic package imports or names the
   manager-confidence vertical; and
7. the requested removal change is separately reviewed and merged.

## Completion criteria

Each phase is independently shippable. Plan D itself is complete only when the
typed read-only query boundary, PostgreSQL historical projection, all four CLI
leaves and executable intents, deterministic text and JSON rendering, exact
citation contract, expanded Project Atlas acceptance, generic
manager-confidence parity gate, and gated specialized-path removal are all
implemented and passing. Completing an earlier phase does not imply that later
intent behavior or removal is complete.

No completion claim includes live document-source, directory, model-provider,
cloud, web, cache, or private-corpus acceptance unless those boundaries are
separately exercised and reported.
