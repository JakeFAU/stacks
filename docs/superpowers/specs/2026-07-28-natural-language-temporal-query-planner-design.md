# Plan E Natural-Language Temporal Query Planner Design

**Status:** Approved design; awaiting written-spec review

**Scope:** Add a provider-neutral, read-only planner that converts one private
natural-language temporal question into the existing closed `query.Request`,
then executes that validated request through the unchanged deterministic Plan D
retrieval and rendering path.

**Design base:** `c6852d2` on `main`, after Plan D PR #28 and cleaner PRs #31,
#29, and #30 were merged through protected checks.

## Summary

Plan D established a deterministic, provider-free temporal query boundary:

```text
CLI
  -> internal/query
  -> core/temporal
  -> typed facts, changes, conflicts, gaps, and exact citations

PostgreSQL remains isolated in adapters/postgres behind the root-owned bridge.
```

Plan E adds natural-language planning in front of that boundary. The model may
propose only fields already representable by `query.Request`. Model output is
untrusted structured input. It is strictly decoded, deterministically composed
with caller-owned canonical entity IDs, normalized by the existing query
contract, echoed for audit, and only then executed.

Plan E does not add narration. The returned facts, changes, conflicts, gaps,
causal claims, and citations remain the exact deterministic Plan D result.

The first operator surface is:

```text
stacks query ask --entity <canonical-id> \
  --reference-time <RFC3339> \
  --output text|json
```

The private question is read from standard input. It is not accepted as a flag
or positional argument. The application service behind the command is
transport-neutral so a future web interface can call it without parsing CLI
state or reconstructing planner behavior.

## Current repository evidence

The design relies on the following repository state, verified after Plan D
finalization:

- `internal/query.Request` is the closed caller-supplied request containing
  temporal intent, canonical entity IDs, entity-match policy, predicates,
  valid-time selections, recorded-time knowledge scope, and chronology limit.
- `query.NormalizeRequest` validates cardinality, canonicalizes ordering,
  applies intent-specific constraints, and delegates temporal validation to
  `core/temporal.NewPlan`.
- `query.Service` owns deterministic retrieval, aggregation, chronology,
  conflict preservation, gap construction, causal eligibility, limits, and
  result validation.
- `internal/cli` exposes typed point, trend, trajectory, and causal query
  leaves with text and JSON rendering.
- PostgreSQL remains behind `adapters/postgres` and the root-owned query
  database bridge.
- `extract.Model` and the OpenAI, Anthropic, and Bedrock adapters are currently
  bound to the exact embedded `extract-v2` prompt and schema. They are not a
  generic structured-generation interface.
- Provider selection, data mode, model ID, token limit, attempts, credentials,
  and Bedrock restricted-disclosure policy already live in typed
  `internal/config` and provider-edge packages.
- Core migrations are current at 3/3. The optional directory scope is absent
  and unconfigured. Plan E needs no schema change.

## Goals

Plan E must:

1. Accept one natural-language temporal question through a private input
   boundary.
2. Require the caller to supply every canonical entity ID and an explicit
   reference instant.
3. Produce only a request representable by the existing `query.Request`
   contract.
4. Keep entity matching, identity authority, aggregation, chronology,
   conflict handling, causal eligibility, limits, and citation association out
   of the model.
5. Validate the proposal before constructing a query database boundary.
6. Execute the normalized request automatically through the unchanged Plan D
   query service.
7. Return an auditable envelope containing the exact normalized request,
   reference instant, bounded planner provenance, and unchanged deterministic
   result.
8. Preserve existing typed query commands as a provider-free deterministic
   fallback.
9. Keep model construction lazy and configuration command-specific.
10. Support the approved OpenAI, Anthropic, and Bedrock disclosure policies
    without allowing provider SDK types into planner, query, CLI, or
    application contracts.
11. Remain ready for a future web transport without implementing that
    transport now.

## Non-goals

Plan E does not include:

- entity matching, alias resolution, mention resolution, or identity writes;
- accepting free-form names as canonical entity IDs;
- new temporal intents or retrieval operations;
- changes to deterministic aggregation, comparison, chronology, conflict,
  causal, gap, limit, or citation behavior;
- model-generated narration, summarization, answer prose, or conclusions;
- retrieval-augmented prompting with facts, passages, citations, or query
  results;
- prompt-driven truth, causality, conflict resolution, or confidence policy;
- persistence or caching of questions, proposals, plans, results, or model
  metadata;
- schema changes, migrations, or fingerprint changes;
- an HTTP or browser user interface;
- a public network API;
- automatic provider failover;
- a heuristic or regex natural-language parser;
- a hidden wall clock or implicit timezone;
- a `--question` flag or positional question argument;
- plan-only execution, interactive confirmation, or a multi-turn
  clarification protocol;
- restoration of the retired manager-confidence command, terminology, prompt,
  schema, or vertical;
- live provider invocation, private-corpus testing, or provider-quality claims
  during design or local implementation acceptance.

Narration is a separate future design and approval gate. A combined planner and
narrator is not a later phase of this implementation plan.

## Non-negotiable invariants

### Authority

- Model output is untrusted structured input, never authoritative state.
- An executable planner proposal may contain only fields representable by the
  existing closed query request. A separate closed non-executable status may
  decline to propose a request.
- `query.NormalizeRequest`, `core/temporal`, and `query.Service` remain the
  authorities for request validity, temporal semantics, retrieval, and result
  construction.
- The planner cannot change database state.
- The planner cannot select a fact, winner, citation, conflict outcome,
  causal conclusion, or gap interpretation.

### Identity

- Every canonical entity ID comes from the caller.
- Canonical entity IDs are validated locally before model disclosure.
- Canonical entity IDs are not added to the model request.
- A question containing any supplied canonical entity ID verbatim is rejected
  before disclosure, so the private question cannot accidentally carry those
  local identifiers across the provider boundary.
- Every validated caller-supplied ID is attached unchanged to the proposed
  request.
- The model cannot add, remove, replace, reorder semantically, or resolve
  entity IDs.
- The caller must supply only the canonical IDs relevant to the question.
- Stable canonical identity authority remains separate and reviewable.

### Time

- Every invocation requires an explicit RFC3339 reference instant.
- The reference instant is normalized and included in the audit envelope.
- The model receives that reference instant and must use it for relative time
  phrases.
- No planner code calls the wall clock to interpret the question.
- Valid-time selections and recorded-time knowledge scope remain independent.
- All generated points, window bounds, and recorded-time cutoffs must be
  explicit RFC3339 values before query construction.
- Plan validation establishes structural validity and representability. It
  does not claim that a probabilistic model interpreted the question
  semantically correctly.

### Retrieval and results

- Deterministic retrieval remains the authority for aggregation, chronology,
  conflict handling, causal eligibility, limits, and citation association.
- Limits fail explicitly rather than silently truncating.
- Temporal precedence never establishes causality.
- Causal-chain planning remains constrained to the exact Plan D causal
  predicate.
- The planner never receives retrieved facts, passages, citations, conflicts,
  gaps, or source content.
- The query result is not summarized, flattened, reordered, or rewritten by a
  model.
- Exact fact/change/citation associations and all material conflicts,
  uncertainty, hypotheses, counterevidence, and gaps are preserved.

### Privacy and disclosure

- Questions are read from standard input, not process arguments.
- Questions, prompt bodies, model output, IDs, predicates, timestamps,
  normalized requests, citations, names, SQL, credentials, provider request
  IDs, raw provider errors, and database URLs never enter logs, metrics,
  traces, or error strings.
- Provider requests contain no caller-supplied canonical entity IDs or
  retrieved evidence. Stacks cannot classify arbitrary question text as an
  unknown third-party identifier without performing the deferred entity
  matching work.
- Provider construction occurs only for `query ask`.
- Data mode and provider remain explicit; there is no default provider.
- Restricted data remains Bedrock-only and fail closed behind the approved
  invocation-logging preflight.
- No provider tool, file, browsing, conversation, background, connector,
  cache, or managed-agent capability is enabled.

### Delivery

- Invariants produce tests before implementation.
- Design, implementation planning, implementation, publication, and
  live-provider validation remain separate approval gates.
- No administrator bypass is used for protected branches.
- Only synthetic fixtures are used before separately approved private-corpus
  acceptance.
- Canonical migration bytes and fingerprints remain unchanged.

## Alternatives considered

### 1. Planner-only followed by deterministic execution

This is the selected design.

It solves the leading deferred capability—natural-language question
planning—while adding one model disclosure and validation boundary. It
preserves the existing query request and result contracts and provides a
complete operator workflow because a validated plan executes automatically.

### 2. Narration-only

A narrator could consume a typed cited result and generate bounded prose, but
it would not solve natural-language question planning. Narration also creates a
harder semantic validation problem: generated prose must retain every material
citation association, conflict, gap, uncertainty, hypothesis, and item of
counterevidence. It is deferred to a separate design.

### 3. Combined planner and narrator

A combined surface would couple two prompts, schemas, disclosure decisions,
validation regimes, provider calls, and failure modes. A wrong answer would be
harder to attribute to planning or narration. It is larger than the smallest
complete next increment and is rejected.

### 4. Generalize `extract.Model`

The extraction contract is intentionally exact and limited to `extract-v2`.
Turning it into a generic structured-generation abstraction would broaden a
reviewed disclosure boundary, weaken exact contract verification, and force an
unrelated refactor before Plan E. The planner instead owns a separate
consumer-specific interface.

### 5. Plan-only CLI output

Stopping after proposal validation would maximize manual control but would not
provide a complete natural-question workflow. The selected design executes
automatically after deterministic validation and returns the exact normalized
request in the result envelope for audit.

### 6. Question flags or positional arguments

Passing a private question in process arguments is convenient but routinely
exposes it through shell history and process inspection. Standard input is the
only supported question transport in this phase.

## Architecture

### Component boundary

```text
internal/cli
  query ask transport
  stdin and flag parsing
  text/JSON output selection
          |
          v
internal/queryplan
  transport-neutral Ask service
  private input validation
  exact prompt/schema contract
  provider-neutral model interface
  strict proposal decoding
  deterministic request composition
  audit-envelope construction
          |
          +--------------------+
          |                    |
          v                    v
provider adapter          internal/query
OpenAI/Anthropic/         NormalizeRequest
Bedrock structured        Service.Query
generation                    |
                               v
                        core/temporal
                               |
                               v
                        adapters/postgres
                        through root bridge
```

`internal/queryplan` depends on provider-neutral domain and query types. It
does not import provider packages or SDKs. Provider adapters may import the
consumer-owned planner contract. The composition root imports both and wires
the selected provider lazily.

`internal/query` remains provider-free and does not import `internal/queryplan`.

### Package responsibilities

#### `internal/queryplan`

Owns:

- input, proposal, plan provenance, and execution-envelope types;
- pre-disclosure input validation;
- the exact embedded `query-plan-v1` prompt and JSON Schema;
- prompt/schema contract lookup and isolated byte copies;
- provider-neutral model request and response types;
- strict JSON decoding;
- proposal-to-domain conversion;
- attachment of caller-owned entity IDs;
- `query.NormalizeRequest` invocation;
- the transport-neutral ask orchestration service;
- bounded planner error classification;
- planner-specific observability decisions.

It does not own:

- provider SDK calls;
- CLI parsing or rendering;
- database construction;
- PostgreSQL queries;
- temporal aggregation;
- result narration.

#### `internal/openai`, `internal/anthropic`, and `internal/bedrock`

Each adapter adds a narrow planner-specific structured-generation path that:

- accepts the consumer-owned planner model request;
- verifies the exact supported prompt and schema bytes before network access;
- maps the request to the approved native structured-output API;
- keeps existing stateless and no-tools policies;
- owns exact bounded retries;
- rejects refusals, incomplete output, mismatched models, and invalid response
  envelopes;
- returns untrusted JSON plus bounded metadata;
- redacts provider response bodies and request details from errors.

The existing extraction `Generate` path and `extract-v2` verification remain
unchanged. A provider client may implement both consumer interfaces without
turning either into a generic request type.

#### `internal/cli`

Adds the `query ask` leaf and a dedicated invocation payload. It:

- rejects positional arguments;
- reads the question from an injected input reader;
- applies a configured maximum before allocating the complete question;
- parses canonical entity IDs, reference time, and output format;
- invokes the transport-neutral planner service;
- renders the audited execution envelope.

Existing typed query leaves and their outputs remain unchanged.

#### `internal/config`

Adds a distinct query-planner validation target even though `ask` is nested
under the `query` CLI command. Typed queries continue to validate as
`CommandQuery`; `query ask` validates as `CommandQueryAsk`.

`CommandQueryAsk` requires:

- the canonical application database URL;
- the existing query limits;
- explicit data mode;
- explicit provider;
- explicit model ID;
- output token limit;
- maximum provider attempts;
- provider-specific credentials and Bedrock region/profile when applicable;
- an overall planner timeout covering retries and backoff;
- a maximum question byte count.

Typed `query point`, `trend`, `trajectory`, and `causal` commands do not require
planner or provider settings.

The new planner settings use `STACKS_*` names and are represented in typed
configuration:

- `STACKS_QUERY_PLANNER_TIMEOUT`
- `STACKS_QUERY_PLANNER_MAX_QUESTION_BYTES`

The prompt/schema version is a source-controlled constant in this phase, not a
runtime setting. Adding a second supported version requires a reviewed contract
change.

#### Composition root

The root:

1. validates the command-specific settings;
2. validates the local query-ask input;
3. enforces restricted disclosure policy when required;
4. constructs only the selected planner provider;
5. invokes planning;
6. opens PostgreSQL only after a normalized request exists;
7. constructs the unchanged query service;
8. executes and renders the audited result;
9. closes provider and database resources according to their existing
   ownership contracts.

No Drive, Directory, ingestion, extraction, or narration dependency is
constructed for `query ask`.

## Command and future API boundary

### CLI syntax

```text
stacks query ask \
  --entity <canonical-id> \
  [--entity <canonical-id> ...] \
  --reference-time <RFC3339> \
  [--output text|json]
```

The question is the complete standard-input payload after validation. Empty or
whitespace-only input is rejected. Input larger than the configured byte limit
is rejected before provider construction. Invalid UTF-8 is rejected.

The command uses all supplied entity IDs. The model does not select a subset.
Duplicate, blank, or over-limit IDs fail before disclosure.

`--reference-time` is required even when the question appears to use only
absolute dates. This keeps every invocation formally reproducible and makes
relative-language interpretation auditable.

The existing query limit configuration remains authoritative. The planner
cannot raise limits through its output.

### Transport-neutral service

The service boundary is conceptually:

```go
type Input struct {
    Question      string
    EntityIDs     []identity.EntityID
    ReferenceTime time.Time
}

type Service interface {
    Ask(context.Context, Input) (Execution, error)
}
```

The exact Go ownership and constructor shape belong in the implementation plan,
but the service must not depend on Cobra, `io.Reader`, `io.Writer`, HTTP
frameworks, provider SDK types, or PostgreSQL types.

A future web handler may decode a transport request into this same input and
encode the same execution envelope. Plan E does not create that handler,
authentication policy, session model, browser question history, or network
deployment.

## Planner contracts

### Private planner input

The model receives a canonical private input document containing:

- the question;
- the normalized reference instant;
- the configured query limits;
- a statement that every caller-supplied entity ID will be attached locally;
- the count of caller-supplied entities.

The model does not receive:

- entity IDs;
- canonical names or aliases added by Stacks;
- source documents or passages;
- observations;
- citations;
- query results;
- database state;
- provider credentials;
- SQL;
- prior questions or plans.

The private input is serialized deterministically. Retry attempts use identical
semantic request bytes, provider, model, prompt, schema, token limit, and data
mode.

### Structured proposal

The proposal contains only:

- `status`: `executable` or `cannot-plan`;
- `reason`: `none`, `ambiguous-question`, `unsupported-question`, or
  `insufficient-temporal-detail`;
- `intent`: one of `point-in-time`, `trend-comparison`, `trajectory`, or
  `causal-chain`;
- `entity_match`: `all` or `any`;
- `predicates`: a bounded array of predicate strings;
- `selections`: a bounded array of explicit temporal selection objects;
- `knowledge_scope`: `current` or `as-of` with an explicit cutoff when
  applicable;
- `chronology_limit`: zero for point and trend, or a positive bounded value for
  trajectory and causal chain.

Each selection is closed and explicit:

- point: fixed label `point`, kind `point`, RFC3339 instant, and empty window
  fields;
- trend: two windows with fixed labels `before` and `after`;
- trajectory or causal chain: one window with fixed label `between`;
- window: kind `window`, RFC3339 start and end, and an empty instant field.

Every schema field is required. Unused scalar fields use reviewed empty
sentinels so the schema can remain compatible with all approved providers'
structured-output subsets. Cross-field meaning is enforced deterministically
after schema decoding.

An executable proposal requires `status = executable`, `reason = none`, and a
complete request shape. A non-executable proposal requires
`status = cannot-plan`, one non-`none` reason, and empty sentinels for every
request field. It is a bounded failure outcome, not a request, and never reaches
PostgreSQL.

The closed enums for request fields include an empty sentinel solely for the
`cannot-plan` shape. Deterministic validation rejects that sentinel from every
`executable` proposal.

The schema:

- rejects unknown object fields;
- bounds array cardinality to repository maxima;
- uses enums for every closed value;
- uses integers for limits;
- uses strings for timestamps, which Go then parses strictly as RFC3339;
- does not contain entity IDs or citation fields;
- is embedded and returned as an isolated byte copy.

### Local request composition

After strict decoding, the planner boundary:

1. validates the closed status/reason pairing;
2. returns a bounded error immediately for `cannot-plan`;
3. verifies intent-specific selection count, labels, kinds, and unused fields;
4. parses and normalizes every timestamp;
5. constructs `temporal.At` or `temporal.Between` values;
6. constructs `temporal.CurrentKnowledge` or `temporal.KnownAsOf`;
7. enforces the exact causal predicate rule;
8. converts predicates through the existing observation predicate boundary;
9. attaches all caller-supplied canonical entity IDs unchanged;
10. constructs `query.Request`;
11. calls `query.NormalizeRequest` with configured query limits.

Only the normalized result is executable. The raw proposal is discarded after
the operation and is never logged, rendered, or persisted.

### Audit and execution envelope

The transport-neutral result contains:

- output schema version;
- normalized reference instant;
- exact normalized `query.Request`;
- planner prompt/schema version;
- configured provider and returned model ID after exact-match verification;
- bounded attempt count;
- bounded aggregate token usage;
- bounded aggregate latency;
- deterministic `query.Result`.

It does not contain:

- the question;
- prompt text;
- raw structured proposal;
- provider request or response IDs;
- raw provider output;
- retry errors;
- credentials.

The output schema is versioned independently from `query-plan-v1`. A transport
change must not imply a prompt change, and a prompt revision must not silently
change output serialization.

## Data flow

### Successful request

1. Cobra parses `query ask` flags without reading provider configuration.
2. The CLI reads bounded UTF-8 question input from standard input.
3. Local validation rejects malformed IDs, reference time, question, output
   mode, and query limits.
4. Command-specific configuration validation requires the database and selected
   model boundary.
5. Restricted Bedrock disclosure preflight runs before provider invocation.
6. The root constructs only the selected planner provider.
7. `internal/queryplan` loads `query-plan-v1` and builds the private model
   input without entity IDs.
8. The adapter verifies the exact contract and performs one bounded
   structured-output request, retrying only eligible same-provider failures.
9. `internal/queryplan` strictly decodes and validates the proposal.
10. Caller-owned IDs are attached unchanged.
11. `query.NormalizeRequest` produces the executable request.
12. The root opens one canonical application database.
13. The existing `query.Service` performs deterministic retrieval and result
    validation.
14. The CLI renders one complete audit envelope and result.
15. Resources close through existing lifecycle ownership.

### Failure atomicity

No stdout is written unless planning, normalization, retrieval, result
validation, and complete rendering all succeed. Rendering first builds a
complete bounded byte sequence before writing it. Short writes remain errors.

An invalid or incomplete proposal never opens PostgreSQL. A database or query
failure never emits a partial plan or result. A rendering failure never falls
back to another format.

## Validation and error behavior

### Pre-disclosure failures

The following fail before provider construction:

- missing, empty, oversized, or invalid UTF-8 question;
- missing, blank, duplicate, or over-limit entity IDs;
- a question containing any supplied canonical entity ID verbatim;
- missing or invalid reference instant;
- invalid output mode;
- invalid query limits;
- incomplete planner/provider configuration;
- unsupported provider or data-mode combination;
- unsupported legacy provider environment variables;
- failed restricted-disclosure preflight.

### Provider failures

Adapters map failures to bounded provider-neutral outcomes. They never return
provider response bodies or private request details.

Retryable:

- HTTP 429 or provider-equivalent throttling;
- bounded transient server or unavailable responses;
- retryable transport failures.

Terminal:

- authentication or authorization;
- invalid provider request;
- unsupported prompt/schema contract;
- refusal;
- incomplete or truncated response;
- missing usage required by an adapter contract;
- mismatched returned model;
- malformed provider response envelope;
- context cancellation or deadline;
- deterministic JSON or domain validation failure.

Provider SDK retries are disabled or neutralized so configured attempts are
exact. Backoff respects the caller context. There is no provider failover.

A transport failure after the provider processed a request may cause the exact
request to be retried. This is safe because planning is read-only and only the
final complete response can proceed to query execution.

### Proposal failures

The planner returns bounded errors for:

- malformed JSON;
- multiple JSON values or trailing content;
- unknown fields;
- invalid enums;
- missing required fields;
- invalid status/reason pairing;
- planner-declared `cannot-plan`;
- non-RFC3339 timestamps;
- zero or inverted windows;
- wrong selection count, kind, or labels for the intent;
- invalid recorded-time scope;
- missing or excessive predicates;
- invalid chronology limit;
- any request rejected by `query.NormalizeRequest`.

Errors identify only the failed operation or bounded reason category. They do
not include the question, proposal values, IDs, predicates, timestamps, or raw
output.

### Cancellation precedence

Caller context state is authoritative. If the caller is canceled while a
provider or database returns a conflicting deadline error, the canonical
caller cancellation wins. Otherwise provider-returned cancellation or deadline
identity remains discoverable through `errors.Is`. Private wrapped detail is
never retained.

### Deterministic fallback

There is no automatic fallback inside `query ask`.

The supported fallback is the existing typed CLI:

```text
stacks query point
stacks query trend
stacks query trajectory
stacks query causal
```

If a validated plan and result were returned, the audit envelope shows the
exact request for a later typed rerun. If planning failed before output, the
operator must supply a typed request independently; the application does not
print or persist a partial proposal.

## Rendering and citation preservation

### JSON

JSON output nests the exact normalized request and existing deterministic
result under a versioned outer envelope. It does not flatten citations into
text, duplicate evidence into planner metadata, or convert conflicts and gaps
into conclusions.

### Text

Text output contains:

1. a compact validated-plan section;
2. the existing Plan D deterministic result rendering.

The plan section displays only operator-required audit fields. It omits the
question and raw model response. The result section reuses the existing
renderer rather than implementing a planner-specific summary.

### Preservation rule

The outer envelope may add metadata but may not alter any `query.Result`
content or association. In particular:

- fact citations remain attached to their facts;
- change citations remain attached to their changes;
- conflicting alternatives remain distinct;
- hypotheses remain hypotheses;
- counterevidence remains visible;
- gaps remain explicit;
- valid time and recorded time remain separately represented;
- causal items remain limited to explicit causal observations.

No model runs after retrieval.

## Prompt and schema versioning

The first contract is:

- prompt/schema version: `query-plan-v1`;
- schema name: `temporal_query_plan_v1`;
- embedded system prompt: source-controlled text;
- embedded JSON Schema: source-controlled bytes;
- output-envelope version: `query-ask-v1`.

Tests lock:

- prompt digest;
- schema digest;
- schema name;
- isolated-copy behavior;
- rejection of unknown versions;
- rejection of mutated prompt or schema bytes by every adapter.

The prompt states:

- model authority is limited to proposing the closed request fields;
- ambiguity, unsupported intent, or insufficient temporal detail must return
  the closed `cannot-plan` status and bounded reason;
- caller IDs are attached locally and must not be invented;
- reference time is the sole anchor for relative language;
- valid time and recorded time are distinct;
- causal-chain intent requires explicit causal wording and the closed causal
  predicate;
- unsupported, ambiguous, or unrepresentable questions must not be converted
  into a guessed request.

Because the selected command automatically executes a valid proposal, the
prompt and deterministic validator must prefer explicit failure over invented
temporal boundaries or conclusions.

Adding another prompt/schema version is a reviewed source change. Adapters do
not accept arbitrary caller-supplied prompts or schemas.

## Configuration

Configuration remains in `internal/config` and follows existing Viper
precedence and strict document validation.

### Reused model settings

`query ask` reuses the approved explicit settings:

- data mode;
- model provider;
- model ID;
- maximum output tokens;
- maximum attempts;
- provider credentials;
- Bedrock profile and region.

No provider or model is guessed.

### Planner-specific settings

Planner timeout and maximum question bytes are separate typed settings because
ingestion attempt limits do not describe interactive query planning.

`STACKS_QUERY_PLANNER_TIMEOUT` defaults to 60 seconds and accepts values from
one second through five minutes. It is the overall planning deadline across
provider attempts and backoff.

`STACKS_QUERY_PLANNER_MAX_QUESTION_BYTES` defaults to 16 KiB and accepts values
from 1 byte through 64 KiB. The CLI reads at most the configured limit plus one
byte so oversize input fails without allocating an unbounded payload.

These defaults and bounds are named constants and are covered by configuration
tests; they are not repeated in CLI or provider code.

### Command-specific validation

`Settings.Validate(CommandQuery)` continues to require only database and
deterministic query settings.

`Settings.Validate(CommandQueryAsk)` requires database, deterministic query,
planner, data-mode, and provider settings. It must reject invalid settings
before constructing PostgreSQL, AWS, OpenAI, or Anthropic clients.

`config validate query` retains its current meaning. A planner-specific
validation target is added for operators to validate `query ask` without
invoking a model.

## Provider and disclosure boundaries

### OpenAI

- Uses one stateless Responses request per attempt.
- Uses strict native JSON Schema output.
- Sets `store: false`.
- Disables background operation.
- Supplies no conversation or previous-response ID.
- Enables no tools, files, browsing, connectors, or hosted execution.
- Is allowed only for the approved personal data mode.

### Anthropic

- Uses one stateless Messages request per attempt.
- Uses native structured output.
- Enables no tools, files, batches, prompt-caching option, or managed-agent
  capability.
- Treats refusal, truncation, multiple output blocks, missing required usage,
  invalid JSON, and model mismatch as bounded terminal outcomes.
- Is allowed only for the approved personal data mode.

### Bedrock

- Uses Converse structured output.
- Retains existing region/profile policy.
- Restricted mode performs the approved read-only invocation-logging
  inspection and proceeds only when logging is confirmed disabled.
- Unknown state, enabled logging, access denial, timeout, or inspection failure
  fail closed.
- Enables no provider-managed tools or agents.

### Same-provider retries

Each adapter owns a bounded retry loop. Every attempt uses the same provider,
model, data mode, question, reference instant, prompt, schema, and configured
limits. There is no cross-provider or cross-model fallback.

Attempt count, aggregate token usage, and aggregate latency are returned as
bounded metadata. Individual raw retry failures are not exposed.

## Observability

Planner observability extends the existing bounded model telemetry rather than
creating content-bearing logs.

Allowed span, metric, or log fields:

- operation name;
- provider enum;
- configured/verified model ID under the existing bounded policy;
- prompt/schema version;
- data mode enum;
- outcome enum;
- attempt count;
- aggregate input, output, and total token counts;
- latency;
- question byte count as a histogram measurement, never as an attribute or
  label.

Forbidden fields:

- question;
- private model input;
- prompt body;
- JSON Schema body;
- raw or decoded model output;
- canonical entity IDs;
- predicates;
- temporal values or reference instant;
- normalized request;
- facts, changes, conflicts, gaps, names, citations, or source text;
- provider request/response IDs;
- provider error bodies;
- SQL;
- database URL;
- credentials or authorization headers.

Metric attributes remain low-cardinality. Question length is never a metric
label. Successful manually owned spans use `observability.FinishSpan` and
explicit `OK` status.

The user-facing audit envelope is private output, not telemetry. Plan E adds no
durable telemetry table or audit migration.

## Synthetic fixtures

All design, unit, adapter, CLI, and local PostgreSQL tests use synthetic data.

The fixture set includes:

1. Point-in-time:
   - question with an explicit date;
   - one caller-owned canonical ID;
   - current knowledge;
   - exact point selection.
2. Trend:
   - question comparing two explicit windows;
   - fixed `before` and `after` labels;
   - multiple caller-owned IDs;
   - explicit `all` or `any` entity match.
3. Trajectory:
   - question using relative language anchored to the required reference
     instant;
   - one half-open `between` window;
   - positive chronology limit.
4. Causal chain:
   - explicitly causal wording;
   - exact causal predicate;
   - one bounded window and chronology limit;
   - counterevidence and conflict retained by deterministic retrieval.
5. Recorded-time cutoff:
   - valid-time selection independent of a `known as of` cutoff.
6. Structural failures:
   - malformed JSON;
   - unknown fields;
   - invalid intent;
   - invalid status/reason pairing;
   - explicit ambiguous, unsupported, and insufficient-detail outcomes;
   - wrong labels or selection shapes;
   - invalid timestamp or inverted window;
   - invented extra entity field;
   - excessive predicates or chronology;
   - refusal and incomplete provider responses.
7. Privacy sentinels:
   - synthetic question, name, ID, predicate, timestamp, citation, database
     URL, credential, and provider-body markers that must not appear in
     telemetry or errors.
8. Retry:
   - 429 then success from the same provider;
   - transient transport error then success;
   - authentication, refusal, invalid output, cancellation, and deadline with
     no retry.

Fixtures never contain personal source material or copied private content.

## Test strategy

Implementation proceeds test-first.

### Layer 1: contract tests

- prompt and schema fingerprints;
- exact supported version;
- isolated contract bytes;
- strict proposal decoder;
- unknown-field and trailing-content rejection;
- proposal cross-field validation;
- caller-owned entity attachment;
- reference-time normalization;
- `query.NormalizeRequest` integration;
- causal predicate enforcement;
- configured limit enforcement.

### Layer 2: provider adapter tests

Local fake HTTP servers or SDK transports prove:

- exact prompt/schema verification;
- exact provider request shape;
- stateless/no-tools/no-storage policy;
- same-provider retry eligibility and exact attempt count;
- SDK retry neutralization;
- cancellation and deadline behavior;
- refusal, truncation, invalid output, and model-mismatch handling;
- bounded usage/latency metadata;
- private error redaction.

No provider network call occurs.

### Layer 3: configuration tests

- file, environment, and default precedence;
- planner-only environment names;
- command-specific validation;
- typed query commands requiring no provider settings;
- `query ask` requiring explicit model settings;
- restricted direct-provider rejection;
- Bedrock region/profile rules;
- invalid limits and timeout bounds;
- strict YAML/JSON unknown-key rejection;
- secret/value redaction in validation errors.

### Layer 4: application and CLI tests

- `query ask` accepts no positional question;
- standard-input question handling;
- bounded and invalid UTF-8 input rejection;
- required canonical IDs and reference time;
- lazy provider construction only for `ask`;
- no Drive, Directory, ingestion, or extraction construction;
- invalid proposals open no PostgreSQL connection;
- one PostgreSQL boundary per valid execution;
- resource closure and caller cancellation;
- stdout atomicity;
- text and JSON audit rendering;
- omission of question and raw proposal;
- existing typed query behavior and output remain byte-compatible.

### Layer 5: deterministic parity tests

For each supported intent:

1. a fake planner returns a synthetic structured proposal;
2. local composition produces a normalized request;
3. the same normalized request is executed directly through Plan D;
4. planned and direct deterministic results are exactly equal;
5. citation, conflict, counterevidence, uncertainty, gap, valid-time, and
   recorded-time associations are unchanged.

These tests prove orchestration parity, not live model semantic quality.

### Layer 6: PostgreSQL integration

Using the synthetic canonical corpus:

- planned point, trend, trajectory, and causal requests return the same result
  as direct typed requests;
- invalid planner output performs no read;
- chronology overflow fails explicitly;
- entity authority and recorded-time cutoffs remain enforced;
- exact citation hydration and conflict preservation remain unchanged;
- canonical observation evidence batching from cleaner PR #31 remains covered;
- core migration state remains current;
- the optional directory scope remains correctly absent when unconfigured.

### Layer 7: privacy and observability

Tests inject unique synthetic private markers into every forbidden category and
assert absence from:

- returned errors;
- captured Zap fields;
- spans and events;
- metric attributes;
- provider adapter outcomes;
- stderr.

User-facing successful output is separately asserted to contain the normalized
request and not the question or raw proposal.

### Layer 8: whole-tree acceptance

```text
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
git diff --check
make db-up
make db-migrate
make db-status
make test-integration ENV_FILE=.env
```

Migration directories are diffed byte-for-byte against the approved
implementation base. Any migration change stops the plan and requires a
separately approved forward-migration design.

## Implementation phases

### E1: planner contracts and deterministic composition

Deliver:

- `internal/queryplan` types;
- `query-plan-v1` prompt and schema;
- strict decoding and deterministic conversion;
- caller-owned entity attachment;
- reference-time handling;
- audit-envelope types;
- synthetic contract tests.

No provider adapter, CLI command, or database orchestration is added in E1.

Review gate:

- independent task review;
- phase review for authority, identity, time, schema compatibility, and privacy.

### E2: provider adapter support

Deliver:

- planner-specific OpenAI path;
- planner-specific Anthropic path;
- planner-specific Bedrock path;
- exact contract verification;
- same-provider retries;
- disclosure policy and bounded telemetry;
- fake transport/SDK tests.

No live provider invocation occurs.

Review gate:

- independent review per provider task;
- cross-provider phase review proving semantic contract parity and no
  extraction regression.

### E3: application and CLI integration

Deliver:

- planner-specific configuration and validation target;
- transport-neutral ask service;
- lazy root composition;
- `stacks query ask`;
- stdin question boundary;
- automatic deterministic execution;
- text and JSON audit rendering;
- lifecycle and privacy tests.

Review gate:

- independent task reviews;
- phase review for configuration isolation, resource ownership, output
  atomicity, and future transport neutrality.

### E4: local and PostgreSQL acceptance

Deliver:

- direct-versus-planned parity fixtures;
- PostgreSQL integration coverage;
- migration immutability verification;
- whole-tree acceptance;
- final documentation updates describing implemented behavior only.

Review gate:

- independent whole-branch review;
- explicit confirmation that narration, entity matching, web UX, persistence,
  and migrations did not enter scope.

### Publication gate

After local implementation approval:

1. ask for explicit permission before push or PR creation;
2. reconcile current `origin/main`;
3. rerun all acceptance gates;
4. publish without administrator bypass;
5. wait for protected checks on the exact head;
6. review and reconcile every newly opened daily-cleaner PR sequentially;
7. merge only valid cleaners after fresh checks and close obsolete ones;
8. update `main`;
9. rerun whole-tree acceptance.

### Live-provider validation gate

Live validation is not implied by implementation completion or publication.

After separate explicit approval:

- select one configured provider and model explicitly;
- use a bounded invocation and token budget;
- use only synthetic questions and synthetic canonical data;
- validate refusal, output compatibility, audit metadata, and deterministic
  execution;
- report provider-specific results without generalizing them to other models;
- never print secrets, private questions, raw prompts, or raw model output.

Each additional provider is a separate reported acceptance action. Private
corpus acceptance remains separately authorized after synthetic live-provider
acceptance.

## Subagent and review workflow

The future implementation plan must:

- assign each implementation task to a fresh subagent implementer;
- use invariants-to-tests-to-implementation order;
- assign an independent reviewer who did not implement the task;
- require the implementer to address actionable review findings;
- repeat independent review after material fixes;
- perform an independent review at each phase boundary;
- perform a final whole-branch review after all phases;
- include privacy, citation preservation, provider-boundary, and scope checks
  in review prompts;
- preserve task reports without treating them as substitutes for tests;
- include daily-cleaner reconciliation during finalization.

No subagent may invoke a live provider, inspect secrets, or use private source
content without the corresponding explicit approval gate.

## Dependencies

Plan E depends on:

- merged Plan D typed query contracts and deterministic results;
- stable canonical entity authority supplied by the caller;
- the approved personal-model-provider disclosure design;
- existing provider configuration and policy types;
- existing model telemetry and observability helpers;
- existing text and JSON query renderers;
- the root-owned PostgreSQL bridge;
- current canonical migration bytes.

It does not depend on:

- new tables or migrations;
- Drive or Directory access;
- ingestion or extraction execution;
- entity resolution;
- narration;
- an HTTP server change;
- a live provider during local acceptance.

## Completion criteria

Plan E implementation is complete only when:

1. `query ask` reads a bounded private question from standard input.
2. The caller supplies every canonical entity ID and an explicit reference
   instant.
3. Questions containing a supplied canonical entity ID are rejected before
   disclosure, and Stacks adds no entity IDs or retrieved evidence to provider
   requests.
4. Model output is strictly decoded; `cannot-plan` is an explicit bounded
   failure, and only `executable` output is deterministically converted into an
   existing normalized `query.Request`.
5. Invalid or non-executable output opens no PostgreSQL connection.
6. A valid executable request runs through the unchanged Plan D query service.
7. Text and JSON output include the exact audited plan and unchanged cited
   result while omitting the question and raw proposal.
8. Same-provider 429 and approved transient retries are bounded, exact, and
   tested.
9. Typed queries construct no provider and retain existing behavior.
10. Every privacy sentinel is absent from errors and telemetry.
11. Direct and planned deterministic results are exactly equal for all four
    intents.
12. No migration byte or fingerprint changes.
13. All local, race, static, build, module, and PostgreSQL gates pass.
14. Independent task, phase, and whole-branch reviews have no unresolved
    findings.
15. Documentation states only behavior actually implemented and clearly
    separates unrun live-provider and private-corpus validation.
16. Narration, entity matching, persistence, and web UX remain deferred.

Passing these criteria establishes a local and synthetic natural-language
planning surface. It does not establish live model quality, provider
availability, private-corpus correctness, or narration safety.
