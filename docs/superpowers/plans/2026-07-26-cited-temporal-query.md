# Cited Temporal Query Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a provider-free, read-only, deterministic temporal query boundary over canonical PostgreSQL observations, expose all four approved cited intents through the CLI, prove generic evidence parity, and only then remove the temporary manager-confidence vertical.

**Architecture:** The application accepts already-resolved entity IDs and explicit temporal selections, reads one coherent historical authority snapshot through an adapter-owned `REPEATABLE READ, READ ONLY` transaction, and performs valid-time aggregation and comparison in `core/temporal`. `internal/query` owns the consumer port, request/result contract, bounded orchestration, exact citation projection, and stable ordering; `internal/cli` owns parsing and text/JSON rendering; `internal/app` and `cmd/stacks` compose the boundary lazily for query commands only.

**Tech Stack:** Go 1.26.0, PostgreSQL 18, pgx v5.10.0, Cobra v1.10.2, Viper v1.21.0, canonical `core` and `adapters/postgres` modules, Zap/OpenTelemetry at root application boundaries, Go's standard `testing` package, and repository-pinned Staticcheck 2026.1.

## Global Constraints

- Preserve phases D1 through D5 exactly. Each phase is implemented in a fresh chat on a branch created from the then-current merged `main`, produces one independently reviewed PR, and stops at its phase completion gate before the next phase starts.
- D1 is the only prerequisite for D2; D2 is the prerequisite for D3; D3 is the prerequisite for D4; D4 plus the generic parity gates are prerequisites for D5 removal.
- Use a fresh implementation subagent for every numbered task. After each task, use a fresh specification reviewer and then a fresh code-quality reviewer. Keep the implementer available to fix findings test-first. After every phase's complete verification, use a separate fresh reviewer for the whole phase branch.
- Every task follows invariants → failing tests → minimal implementation → focused green tests → independent reviews → coherent commit.
- Never push, open a PR, merge, deploy, alter external repository settings, or delete a branch without explicit user approval. A phase implementation chat stops after the reviewed local branch and verification report are ready.
- Do not invoke Google Drive, Workspace Directory, Bedrock, Anthropic, OpenAI, or another provider. Do not read private source contents or inspect/print credentials. Use only synthetic fixtures.
- The query boundary is read-only. It must not write observations, identity decisions, admission decisions, reviews, source data, query history, caches, leases, or derived reports.
- Do not add a schema migration in Plan D. The three immutable canonical core migrations and their fingerprints remain unchanged.
- Preserve valid time and recorded time as independent axes. Normalize every public instant to UTC microsecond precision before comparison or output.
- Require exact, stable, opaque entity IDs. Never resolve aliases, emails, names, free-form text, or directory profiles at query time.
- Admission is independent of epistemic status. Confidence never selects a state, breaks a tie, establishes identity, or creates causality.
- Historical `known-as-of` applies to entity existence, mention resolution authority, admission authority, observations, evidence, and document versions in one coherent snapshot.
- Preserve exact supporting and contradicting evidence as separate ordered collections. Never fetch citation text as a rendering side effect.
- All collections in JSON are `[]`, never `null`; absent optional fields are omitted; the result union has exactly one intent member; the schema version is exactly `stacks.temporal-query.v1`.
- Chronology-capable results fail without partial output when they exceed the caller's validated limit.
- Successful manually owned application and PostgreSQL snapshot spans are explicitly marked `OK`. Telemetry contains only low-cardinality intent, cutoff-presence, bounded count buckets, duration, and outcome class.
- Deterministic/local PostgreSQL acceptance never implies live Drive, Directory, model-provider, cloud, or private-corpus acceptance.

## Branch and Review Protocol for Every Phase

- [ ] Verify the phase starts from merged `main`, not from the Plan D design branch or a previous unmerged worktree:

```bash
git fetch origin
git status --short --branch
git rev-parse HEAD
git rev-parse origin/main
git merge-base HEAD origin/main
```

Expected: the worktree is clean and the selected phase branch descends from the current `origin/main`. Do not merge, rebase, or rewrite history implicitly.

- [ ] Read `AGENTS.md`, `README.md`, the approved design, this plan, and the files listed for the phase before editing:

```bash
sed -n '1,240p' AGENTS.md
sed -n '1,220p' README.md
sed -n '1,1020p' docs/superpowers/specs/2026-07-26-cited-temporal-query-design.md
sed -n '1,1900p' docs/superpowers/plans/2026-07-26-cited-temporal-query.md
```

- [ ] Record the exact pre-change deterministic baseline:

```bash
make test
make staticcheck
git diff --check
```

Expected: all commands pass. If the baseline is not green, stop and distinguish the pre-existing failure from phase work.

- [ ] For each numbered task, dispatch one fresh implementer with only the approved design, this plan, current task, and current branch evidence. After the task's focused tests pass, dispatch a fresh specification reviewer; after specification findings are fixed, dispatch a different fresh code-quality reviewer.

- [ ] At the phase gate, give a fresh whole-branch reviewer the approved design, this plan, `git diff origin/main...HEAD`, task review reports, and exact verification output. Fix every actionable finding test-first and rerun the complete phase gate.

## Dependency and Phase Map

```text
D1 typed state + query contract + pure trend
  -> D2 historical PostgreSQL snapshot + root bridge
      -> D3 trend CLI + query configuration + lazy composition
          -> D4 point + trajectory + explicit causal intents
              -> D5 generic parity gate
                  -> manager-confidence removal
```

No phase may silently claim a subsequent phase:

- D1 has no PostgreSQL or CLI acceptance.
- D2 has no public CLI.
- D3 exposes trend only.
- D4 retains the manager-confidence path.
- D5 removes the specialized path only after both generic typed/CLI parity and PostgreSQL parity pass.

## File Map

### Phase D1: contract and pure trend execution

- Create `core/temporal/state.go`: typed `StateKey`, canonical term ordering, and typed state-candidate validation.
- Modify `core/temporal/aggregation.go`: replace string-composed keys/values with typed state keys and exact observation terms.
- Modify `core/temporal/comparison.go`: compare typed keys and values without delimiter encodings.
- Modify `core/temporal/aggregation_test.go` and `core/temporal/comparison_test.go`: lock typed-term, uncertainty, conflict, role separation, and confidence invariants.
- Create `internal/query/contract.go`: request, limits, entity-match, read selection/snapshot, authority status, coverage, gaps, and typed errors.
- Create `internal/query/result.go`: closed intent payload, cited facts, contributions, citations, changes, and strict result validation.
- Create `internal/query/service.go`: request normalization, reader orchestration, pure trend execution, cancellation/error preservation, and the application query span.
- Create `internal/query/projection.go`: canonical observation-to-state candidate mapping and exact provenance attachment.
- Create `internal/query/order.go`: all approved stable sort rules.
- Create `internal/query/contract_test.go`, `service_test.go`, `projection_test.go`, and `project_atlas_test.go`: synthetic request/result, trend, gap, citation, cutoff, ordering, and privacy tests.

### Phase D2: historical PostgreSQL trend reader

- Create `adapters/postgres/temporal_query.go`: adapter-owned selection/snapshot records and one historical read-only projection.
- Create `adapters/postgres/temporal_query_test.go`: SQL-boundary, cancellation, authority, and type-isolation unit tests.
- Create `adapters/postgres/temporal_query_integration_test.go`: synthetic Project Atlas, historical supersession, entity-match, read-only, and coherent snapshot acceptance.
- Modify `adapters/postgres/dependency_test.go`: prevent root/internal/provider imports.
- Create `internal/query/postgres.go`: root-owned bridge from adapter records to `internal/query.ReadSnapshot`.
- Create `internal/query/postgres_observability.go`: root-owned OpenTelemetry implementation of the adapter's narrow snapshot observer.
- Create `internal/query/postgres_test.go` and `postgres_integration_test.go`: translation, error preservation, and real PostgreSQL cancellation/round-trip tests.
- Reuse but do not stretch or delete `adapters/postgres/query.go` and `internal/analysis/postgres.go`; they remain temporary D5 inputs.

### Phase D3: CLI trend transport

- Modify `internal/config/application.go`, `config.go`, `loader.go`, and `document.go`: add `CommandQuery` and typed query limits.
- Modify `internal/config/*_test.go`: exact defaults, ranges, precedence, strict schema, environment binding, offline validation, and no-provider requirements.
- Modify `.env.example`, `config.example.yaml`, and `config.example.json`: document non-secret query settings.
- Modify `internal/cli/runner.go`: add the `query trend` leaf and typed `QueryInput` on `Invocation`.
- Create `internal/cli/query_input.go` and `query_input_test.go`: exact RFC 3339/window/entity/predicate/output parsing.
- Create `internal/cli/query.go`, `query_json.go`, `query_text.go` and corresponding tests: typed trend execution and deterministic renderers.
- Modify `internal/cli/config.go`, `config_test.go`, `runner_test.go`: add `config validate query` and transport isolation.
- Modify `internal/app/bootstrap.go`, `bootstrap_test.go`, `execute.go`, and `execute_test.go`: validate and route the query target lazily.
- Modify `cmd/stacks/main.go`, `main_test.go`, and `canonical_composition_test.go`: open one canonical database only for query execution and construct no providers.
- Modify `Makefile` and `README.md`: add the operator-facing trend command and accurate acceptance boundary.

### Phase D4: remaining intents

- Create `core/temporal/point.go`, `trajectory.go`, and `causal.go` with matching tests.
- Modify `internal/query/result.go`, `service.go`, `projection.go`, and `order.go`: execute and validate point, trajectory, and causal payloads.
- Extend `internal/query/*_test.go` and `project_atlas_test.go`: all remaining intent and limit behavior.
- Extend `adapters/postgres/temporal_query_integration_test.go` and `internal/query/postgres_integration_test.go`: all-intent historical acceptance.
- Modify `internal/cli/runner.go`, `query_input.go`, `query.go`, `query_json.go`, and `query_text.go`, plus tests: point, trajectory, and causal leaves.
- Modify `README.md`: document all four exact syntaxes and result semantics.

### Phase D5: generic parity and manager-confidence removal

- Create `internal/query/parity_test.go`: public typed-service generic evidence-parity test.
- Extend `internal/cli/query_json_test.go` and `adapters/postgres/temporal_query_integration_test.go`: CLI/JSON and PostgreSQL parity gates.
- Delete `internal/analysis/observation.go`, `observation_test.go`, `postgres.go`, `postgres_test.go`, `postgres_integration_test.go`, `service.go`, `service_test.go`, `signal.go`, and `signal_test.go`.
- Delete `internal/cli/analyze.go`, `analyze_test.go`, and `internal/extract/prompts/analyze-v1.txt`.
- Create `internal/extract/observation.go` and `observation_test.go`: own the still-required versioned interaction-predicate construction.
- Modify `internal/ingest/service.go`, `service_test.go`, and `canonical_test.go`: consume extraction-owned observation mapping, then remove imports of `internal/analysis`.
- Modify `internal/extract/schema.go`, `schema_test.go`, `validate.go`, and `validate_test.go`: remove the pair-analysis prompt/schema contract while preserving the existing `extract-v2` canonical extraction contract.
- Modify `internal/bedrock/client_test.go`: remove the pair-analysis transport-contract case while retaining provider-neutral structured-generation coverage.
- Modify `internal/source/drive/chronology_test.go`: replace manager-policy assertions with provider-neutral source-time/canonical-query assertions or delete the test if covered by generic query acceptance.
- Modify `internal/config/application.go`, `config.go`, `loader.go`, `document.go`, and tests: remove `CommandAnalyze`, `ManagerConfidenceSettings`, analysis prompt settings, and employee/manager action inputs.
- Modify `internal/cli/runner.go`, `config.go`, and tests; `internal/app/bootstrap.go`, `execute.go`, and tests; `cmd/stacks/main.go`, `main_test.go`, and `canonical_composition_test.go`: remove specialized command routing/composition.
- Modify `.env.example`, `config.example.yaml`, `config.example.json`, `Makefile`, `README.md`, and current operational documentation: remove runtime manager-specific configuration, commands, labels, conclusions, and acceptance instructions.
- Modify `adapters/postgres/coremigrations/manifest_test.go`: remove obsolete manager-confidence wording without changing migration bytes, versions, ledgers, or fingerprints.

---

# Phase D1 — Contract and Pure Trend Execution

## D1 Invariants

1. State identity is `(observation.Term subject, observation.Predicate)` and state value is the exact typed object `observation.Term`; no string delimiter or synthetic key is permitted.
2. `internal/query` owns a small consumer port and contains no pgx, Cobra, Viper, provider, or manager-confidence type.
3. The service normalizes request IDs/predicates, validates limits and result unions before reader access/output, and never guesses identities.
4. The reader returns authority-qualified pre-valid-time candidates plus coverage; `core/temporal` owns valid-time eligibility and deterministic aggregation.
5. Every cited fact retains ordered per-observation contributions and role-separated exact citations.
6. Reordered snapshot input yields structurally equal results.
7. Cutoff filtering in D1 fakes proves service semantics only; whole-knowledge PostgreSQL enforcement is a D2 gate.

### Task D1.1: Replace string state encodings with typed canonical state

**Files:**
- Create: `core/temporal/state.go`
- Modify: `core/temporal/aggregation.go`
- Modify: `core/temporal/comparison.go`
- Test: `core/temporal/aggregation_test.go`
- Test: `core/temporal/comparison_test.go`

**Interfaces:**
- Produces:

```go
type StateKey struct {
    Subject   observation.Term
    Predicate observation.Predicate
}

type StateCandidate struct {
    Key                       StateKey
    Value                     observation.Term
    Observation               observation.Observation
    SubjectGroundingMentionID string
    ObjectGroundingMentionID  string
}

type Fact struct {
    Key                      StateKey
    Value                    observation.Term
    ObservationIDs           []observation.ObservationID
    SupportingEvidenceIDs    []evidence.EvidenceID
    ContradictingEvidenceIDs []evidence.EvidenceID
}
```

- Preserves: `AggregateWindow(TemporalSelection, KnowledgeScope, []StateCandidate) (WindowSummary, error)` and `CompareWindowSummaries(WindowSummary, WindowSummary) (Comparison, error)`.
- Consumed by: Tasks D1.2-D1.4 and all later intents.

- [ ] **Step 1: Write typed-state regression tests**

Add these observable tests:

```go
func TestAggregateWindowKeepsTypedEntityAndTextTermsDistinct(t *testing.T)
func TestAggregateWindowIgnoresGroundingMentionForEntitySemanticIdentity(t *testing.T)
func TestAggregateWindowRejectsCandidateThatDoesNotMatchObservationStatement(t *testing.T)
func TestCompareWindowSummariesOrdersTypedKeysAndValuesDeterministically(t *testing.T)
func TestStateKeyNeverDependsOnStringDelimiterEncoding(t *testing.T)
```

Use synthetic terms containing `:`, `/`, Unicode, and whitespace-preserving exact text. Assert that an entity term and a text term with the same visible bytes remain different and that a grounded entity compares by entity ID while its contribution can retain the mention.

- [ ] **Step 2: Run the focused tests and prove red**

```bash
(cd core && go test ./temporal -run 'Test(AggregateWindowKeepsTyped|AggregateWindowIgnoresGrounding|AggregateWindowRejectsCandidate|CompareWindowSummariesOrdersTyped|StateKeyNever)' -count=1)
```

Expected: compile/test failure because `StateKey` and typed `StateCandidate` do not exist and current facts use strings.

- [ ] **Step 3: Implement the smallest typed state contract**

In `state.go`, implement validation/equality/order helpers over the closed `observation.Term` kinds. Compare term kind first, then exact text, mention ID, or entity ID. Entity comparison ignores grounding mention; absent has no payload. Validate:

```go
func NewStateKey(subject observation.Term, predicate observation.Predicate) (StateKey, error)
func CompareStateKeys(left, right StateKey) int
func CompareTerms(left, right observation.Term) int
```

Update aggregation to require the exact predicate and a valid semantic mapping:

- direct entity terms map to the same entity ID;
- text and absent terms map exactly;
- mention terms may map only to an entity when the corresponding candidate grounding mention equals the source mention ID; and
- state keys/values contain the ungrounded canonical entity term while grounding remains on the candidate/contribution.

Replace every `map[string]...` identity with typed structs or canonical comparable internal keys; do not serialize terms to manufacture map keys.

- [ ] **Step 4: Run all temporal tests**

```bash
(cd core && go test ./temporal -count=1)
```

Expected: all existing temporal behavior plus the new typed-state tests pass.

- [ ] **Step 5: Run the task reviews and commit**

Specification review questions: Does core preserve closed term kinds, exact text, entity semantic identity, valid/recorded separation, uncertainty, conflicts, role separation, and no-confidence selection? Code-quality review questions: Is ordering total and deterministic, are inputs defensively copied, and is there any delimiter/string encoding?

```bash
git add core/temporal/state.go core/temporal/aggregation.go core/temporal/comparison.go core/temporal/aggregation_test.go core/temporal/comparison_test.go
git commit -m "Type temporal state candidates"
```

### Task D1.2: Define and validate the renderer-neutral query contract

**Files:**
- Create: `internal/query/contract.go`
- Create: `internal/query/result.go`
- Create: `internal/query/order.go`
- Create: `internal/query/contract_test.go`

**Interfaces:**
- Consumes: `temporal.Intent`, `temporal.TemporalSelection`, `temporal.KnowledgeScope`, `temporal.StateKey`, canonical observation/evidence/identity types.
- Produces:

```go
type EntityMatch string

const (
    EntityMatchAll EntityMatch = "all"
    EntityMatchAny EntityMatch = "any"
)

type Limits struct {
    MaxEntities   int
    MaxPredicates int
    MaxChronology int
}

type Request struct {
    Intent         temporal.Intent
    EntityIDs      []identity.EntityID
    EntityMatch    EntityMatch
    Predicates     []observation.Predicate
    Selections     []temporal.TemporalSelection
    KnowledgeScope temporal.KnowledgeScope
    Limit          int
}

type ReadSelection struct {
    EntityIDs      []identity.EntityID
    EntityMatch    EntityMatch
    Predicates     []observation.Predicate
    Selections     []temporal.TemporalSelection
    KnowledgeScope temporal.KnowledgeScope
}

type Reader interface {
    Read(context.Context, ReadSelection) (ReadSnapshot, error)
}

type EntityAuthority struct {
    EntityID identity.EntityID
    Known    bool
}

type ReadObservation struct {
    Observation               observation.Observation
    Subject                   observation.Term
    Object                    observation.Term
    SubjectGroundingMentionID string
    ObjectGroundingMentionID  string
    Evidence                  []Citation
}

type CoverageReason string

const (
    CoverageUnresolvedMention CoverageReason = "unresolved-mention"
    CoverageAuthorityExcluded CoverageReason = "authority-excluded"
    CoverageEntityFiltered    CoverageReason = "entity-filtered"
    CoveragePredicateFiltered CoverageReason = "predicate-filtered"
)

type Coverage struct {
    Reason         CoverageReason
    EntityID       identity.EntityID
    Predicate      observation.Predicate
    ObservationID observation.ObservationID
}

type ReadSnapshot struct {
    Entities     []EntityAuthority
    Observations []ReadObservation
    Coverage     []Coverage
}

type GapKind string

const (
    GapNoEvidence        GapKind = "no-evidence"
    GapValidTimeExcluded GapKind = "valid-time-excluded"
    GapUnresolvedMention GapKind = "unresolved-mention"
    GapAuthorityExcluded GapKind = "authority-excluded"
    GapNoCausalEvidence  GapKind = "no-causal-evidence"
)

type Gap struct {
    Kind           GapKind
    EntityID       identity.EntityID
    Predicate      observation.Predicate
    SelectionLabel string
}

type Result struct {
    Intent         temporal.Intent
    EntityIDs      []identity.EntityID
    EntityMatch    EntityMatch
    Predicates     []observation.Predicate
    Selections     []temporal.TemporalSelection
    KnowledgeScope temporal.KnowledgeScope
    Limit          int
    Payload        IntentPayload
    Gaps           []Gap
}
```

`ReadSelection` is the normalized repository request. `ReadSnapshot` contains requested-entity authority status, qualified canonical observations, resolved semantic terms with optional grounding mentions, exact evidence records, and closed coverage reasons. Define `ErrEntityNotFound` and `ErrLimitExceeded` so callers can use `errors.Is` without embedding supplied IDs.

- [ ] **Step 1: Write exhaustive request and result validation tests**

Add table tests for:

```go
func TestNormalizeRequestAcceptsEveryApprovedIntentShape(t *testing.T)
func TestNormalizeRequestRejectsInvalidIntentSelectionsBeforeReaderAccess(t *testing.T)
func TestNormalizeRequestRejectsBlankDuplicateOrOverLimitEntitiesAndPredicates(t *testing.T)
func TestNormalizeRequestEnforcesIntentSpecificLimitRules(t *testing.T)
func TestNormalizeRequestRestrictsCausalPredicate(t *testing.T)
func TestValidateResultRequiresExactlyOneMatchingPayload(t *testing.T)
func TestResultCollectionsAreNonNilAndCanonicallyOrdered(t *testing.T)
func TestTypedErrorsDoNotContainSuppliedEntityIDsOrPrivatePayloads(t *testing.T)
```

Assert exact normalization: trimmed unique lexical IDs/predicates, selection order unchanged, point/trend limit `0`, positive bounded trajectory/causal limit, and causal predicate exactly `stacks.causal.v1/causes`.

- [ ] **Step 2: Run the contract tests and prove red**

```bash
go test ./internal/query -run 'Test(NormalizeRequest|ValidateResult|ResultCollections|TypedErrors)' -count=1
```

Expected: package or identifiers do not exist.

- [ ] **Step 3: Implement closed request, snapshot, result, and error types**

Use explicit structs rather than `map[string]any`. Define one tagged `IntentPayload` whose constructor and validator enforce exactly one of `Point`, `Trend`, `Trajectory`, or `Causal`. Define:

```go
type Contribution struct {
    ObservationID            observation.ObservationID
    Status                   observation.EpistemicStatus
    ValidTime                observation.TemporalExtent
    RecordedAt               time.Time
    Derivation               observation.Derivation
    SubjectGroundingMentionID string
    ObjectGroundingMentionID  string
}

type Citation struct {
    EvidenceID       evidence.EvidenceID
    Role             observation.EvidenceRole
    SourceDocumentID string
    DocumentVersionID string
    SectionID        string
    SectionTitle     string
    SectionPath      []string
    SectionOrder     int
    SectionRole      string
    StartOffset      int
    EndOffset        int
    Locator          string
    Text             string
}

type Fact struct {
    Key                    temporal.StateKey
    Value                  observation.Term
    Contributions          []Contribution
    SupportingCitations    []Citation
    ContradictingCitations []Citation
}

type UnresolvedItem struct {
    Key        temporal.StateKey
    Reason     temporal.UnresolvedReason
    Candidates []Fact
}

type WindowResult struct {
    Selection  temporal.TemporalSelection
    Facts      []Fact
    Unresolved []UnresolvedItem
}

type Change struct {
    Kind   temporal.ChangeKind
    Key    temporal.StateKey
    Before *Fact
    After  *Fact
}

type Transition struct {
    Kind       temporal.ChangeKind
    Key        temporal.StateKey
    ValidTime  observation.TemporalExtent
    Before     *Fact
    After      *Fact
    Unresolved []UnresolvedItem
}

type CausalLink struct {
    Cause                  observation.Term
    Effect                 observation.Term
    Contributions          []Contribution
    SupportingCitations    []Citation
    ContradictingCitations []Citation
}

type PointInTimeResult struct {
    Selection  temporal.TemporalSelection
    Facts      []Fact
    Unresolved []UnresolvedItem
}

type TrendResult struct {
    Before         WindowResult
    After          WindowResult
    Changes        []Change
    UnresolvedKeys []temporal.StateKey
}

type TrajectoryResult struct {
    Selection   temporal.TemporalSelection
    Transitions []Transition
}

type CausalChainResult struct {
    Selection temporal.TemporalSelection
    Links     []CausalLink
}

type IntentPayload struct {
    intent     temporal.Intent
    point      *PointInTimeResult
    trend      *TrendResult
    trajectory *TrajectoryResult
    causal     *CausalChainResult
}
```

Add `NewPointPayload`, `NewTrendPayload`, `NewTrajectoryPayload`, and `NewCausalPayload` constructors plus read-only accessors. Constructors clone slices, normalize times, initialize empty collections, and reject invalid citation bounds/roles or malformed unions.

- [ ] **Step 4: Implement the stable sort functions**

Implement named ordering functions for entity IDs, predicates, state keys/values, contributions, citations, changes, unresolved items, transitions, causal links, and gaps. Never depend on map or SQL row order and never sort by confidence.

- [ ] **Step 5: Run package and dependency checks**

```bash
go test ./internal/query -count=1
! rg -n 'pgx|cobra|viper|internal/analysis|internal/model|internal/source|google|bedrock|anthropic|openai' internal/query
```

Expected: tests pass and the dependency scan has no matches.

- [ ] **Step 6: Review and commit**

Review the contract line-by-line against the normative JSON section even though JSON rendering is D3. Confirm every required field can be represented without a map or nullable alternative.

```bash
git add internal/query/contract.go internal/query/result.go internal/query/order.go internal/query/contract_test.go
git commit -m "Define cited temporal query contract"
```

### Task D1.3: Execute a pure cited trend over a synthetic reader

**Files:**
- Create: `internal/query/service.go`
- Create: `internal/query/projection.go`
- Create: `internal/query/service_test.go`
- Create: `internal/query/projection_test.go`

**Interfaces:**
- Consumes: Task D1.1 typed temporal candidates and Task D1.2 `Reader`, `Request`, and result contract.
- Produces:

```go
type Service struct {
    Reader Reader
    Limits Limits
    Tracer trace.Tracer
}

func (service Service) Query(ctx context.Context, request Request) (Result, error)
```

- [ ] **Step 1: Write service-boundary failing tests**

Add:

```go
func TestTrendQueryNormalizesBeforeReaderAndExecutesOneRead(t *testing.T)
func TestTrendQueryProjectsExactContributionsAndRoleSeparatedCitations(t *testing.T)
func TestTrendQueryPreservesConflictHypothesisCounterevidenceAndTemporalUncertainty(t *testing.T)
func TestTrendQueryCreatesNoEvidenceValidTimeUnresolvedMentionAndAuthorityGaps(t *testing.T)
func TestTrendQueryReturnsUnknownEntityErrorWithoutEchoingID(t *testing.T)
func TestTrendQueryPreservesReaderCancellationAndDoesNotRetry(t *testing.T)
func TestTrendQueryIsIdenticalAcrossReorderedSnapshotInput(t *testing.T)
func TestTrendQueryNeverUsesConfidenceToSelectState(t *testing.T)
func TestTrendQuerySpanContainsOnlyBoundedLowCardinalityAttributes(t *testing.T)
```

The fake reader records call count and selection, returns synthetic canonical observations/evidence, and can return wrapped `context.Canceled`.

- [ ] **Step 2: Run focused tests and prove red**

```bash
go test ./internal/query -run 'TestTrendQuery' -count=1
```

Expected: failure because the service and projection do not exist.

- [ ] **Step 3: Implement request-to-reader orchestration**

`Query` must:

1. reject a nil context, missing reader, invalid limits, or invalid request by constructing the approved `temporal.NewPlan`;
2. start one application query span with only approved bounded attributes;
3. call `Reader.Read` exactly once with normalized entity IDs, match mode, predicates, selections, and knowledge scope;
4. return unknown entity authority as `ErrEntityNotFound`;
5. map qualified observations into typed `temporal.StateCandidate` values;
6. aggregate the before and after windows with `temporal.AggregateWindow`;
7. compare with `temporal.CompareWindowSummaries`;
8. attach per-observation contributions and exact evidence records by ID/role;
9. convert coverage into closed ordered gaps;
10. validate and order the complete result before returning it; and
11. finish the span with explicit `OK` on success while preserving cancellation with `errors.Is`.

- [ ] **Step 4: Implement projection integrity checks**

Reject a snapshot when:

- an observation is missing from its contribution index;
- an observation link names absent evidence;
- evidence metadata conflicts for one stable evidence ID;
- a role in the observation link differs from the snapshot citation role;
- the resolved semantic subject/object does not correspond to a direct canonical term or to the recorded grounding mention for a mention term;
- one stable observation ID carries different canonical payloads; or
- a coverage record has an unknown reason.

Errors must name the failed bounded operation, not source text, titles, locators, IDs, SQL, or credentials.

- [ ] **Step 5: Run all D1 package tests**

```bash
(cd core && go test ./temporal -count=1)
go test ./internal/query -count=1
```

Expected: all pass.

- [ ] **Step 6: Review and commit**

Specification review checks one reader call, model-free execution, exact citations, gaps versus errors, and deterministic ordering. Code-quality review checks cancellation identity, defensive copying, no unbounded error content, and no lifecycle/provider leakage.

```bash
git add internal/query/service.go internal/query/projection.go internal/query/service_test.go internal/query/projection_test.go
git commit -m "Execute cited temporal trends"
```

### Task D1.4: Lock the expanded Project Atlas unit acceptance

**Files:**
- Create: `internal/query/project_atlas_test.go`
- Modify only if a failing acceptance test exposes a D1 contract defect: `core/temporal/*.go`, `internal/query/*.go`

- [ ] **Step 1: Build the complete synthetic Project Atlas fixture**

The fixture must include immutable synthetic sections/spans and canonical observations for:

- hypothetical initial delivery commitment;
- later observed changed commitment;
- support and contradiction for one value;
- responsibility transfer between two stable entity IDs;
- a mention whose authority coverage differs by cutoff;
- unknown valid time and an uncertainty window;
- current/historical admission coverage;
- at least two exact predicates; and
- confidence values deliberately ordered opposite to factual support.

No name, email, URL, transcript, credential, or private corpus value may come from a real source.

- [ ] **Step 2: Write and run the acceptance test**

```go
func TestProjectAtlasTrendContractPreservesCitedTemporalEvidence(t *testing.T)
```

```bash
go test ./internal/query -run TestProjectAtlasTrendContractPreservesCitedTemporalEvidence -count=1
```

Expected: pass only when the result has exact before/after facts, a changed commitment, unresolved hypothesis/uncertainty/conflict, supporting and contradicting citations, stable ordering, explicit gaps, and the normalized knowledge scope.

- [ ] **Step 3: Run the D1 completion gate**

```bash
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
git diff --check
```

Expected: all pass.

- [ ] **Step 4: Run independent whole-branch review**

The reviewer must confirm:

- no PostgreSQL/CLI/provider/manager dependency entered `core` or `internal/query`;
- typed state contains no delimiter encoding;
- all four request/result shapes validate even though only trend executes;
- trend is fully cited, deterministic, gap-aware, and confidence-independent; and
- D1 documentation does not claim PostgreSQL or CLI completion.

- [ ] **Step 5: Fix findings, rerun the complete gate, and commit**

```bash
git add core/temporal internal/query
git commit -m "Prove synthetic temporal trend behavior"
```

## D1 Completion Criteria

- Typed canonical state replaces string-composed state keys/values.
- `internal/query` exposes the approved provider-neutral request/result/read-port contract.
- Trend executes completely against one synthetic snapshot with exact citations and contribution provenance.
- Every validation, ordering, conflict, uncertainty, gap, cancellation, cutoff, and confidence invariant has a deterministic unit test.
- D1 passes all deterministic repository gates and a whole-branch review.
- PostgreSQL, CLI, and subsequent intents remain explicitly incomplete.

---

# Phase D2 — Historical PostgreSQL Trend Reader

## D2 Invariants

1. One adapter call owns one `REPEATABLE READ, READ ONLY` transaction from begin through commit.
2. The adapter evaluates current or cutoff-effective identity/admission authority inside that same snapshot.
3. Direct entity terms require entity existence by cutoff but do not require a resolution decision.
4. Mention terms resolve only when entity, mention, proposal, accepted resolution decision, mention admission, and identity-decision admission are all effective by cutoff.
5. Observation admission is effective by cutoff; extraction-run admission is not a second observation gate.
6. Evidence span and document version must exist by cutoff; immutable provenance is preserved.
7. Valid-time eligibility stays out of SQL and in core.
8. No migration, write, lease, provider call, or adapter type crosses into the public query contract.

### Task D2.1: Add the adapter-owned historical snapshot projection

**Files:**
- Create: `adapters/postgres/temporal_query.go`
- Create: `adapters/postgres/temporal_query_test.go`
- Modify: `adapters/postgres/dependency_test.go`

**Interfaces:**
- Produces adapter-owned values:

```go
type TemporalEntityMatch uint8

const (
    TemporalEntityMatchAll TemporalEntityMatch = iota + 1
    TemporalEntityMatchAny
)

type TemporalQuerySelection struct {
    EntityIDs     []identity.EntityID
    EntityMatch   TemporalEntityMatch
    Predicates    []observation.Predicate
    Selections    []temporal.TemporalSelection
    KnowledgeAsOf *time.Time
}

type TemporalQuerySnapshot struct {
    Entities     []TemporalEntityRecord
    Observations []TemporalObservationRecord
    Coverage     []TemporalCoverageRecord
}

func (database *Database) LoadTemporalQuerySnapshot(
    context.Context,
    TemporalQuerySelection,
    TemporalSnapshotObserver,
) (TemporalQuerySnapshot, error)

type TemporalSnapshotObserver interface {
    StartTemporalSnapshot(context.Context, TemporalSnapshotAttributes) (
        context.Context,
        func(error),
    )
}
```

Use `*time.Time == nil` only at this adapter edge for current knowledge; normalize non-nil values immediately. `TemporalSnapshotAttributes` contains only cutoff presence and bounded input-count buckets; the finish callback derives a bounded outcome class. Neither carries identifiers, predicates, SQL, source metadata, or full errors. The adapter starts and finishes the snapshot observation around the transaction but remains independent of OpenTelemetry.

- [ ] **Step 1: Write fake-pgx boundary tests**

Add tests that prove:

```go
func TestTemporalQuerySnapshotRequiresNormalizedBoundedSelection(t *testing.T)
func TestTemporalQuerySnapshotBeginsRepeatableReadOnlyTransaction(t *testing.T)
func TestTemporalQuerySnapshotCommitsAfterAllAuthorityObservationAndEvidenceReads(t *testing.T)
func TestTemporalQuerySnapshotRollsBackAndPreservesCancellation(t *testing.T)
func TestTemporalQuerySnapshotDoesNotApplyValidTimeOrConfidencePolicy(t *testing.T)
func TestTemporalQuerySnapshotFinishesBoundedObservationOnSuccessAndFailure(t *testing.T)
```

Extend the existing query test fake with only the `pgx.TxOptions`, ordered-query, row-error, commit, and rollback observations required by these tests.

- [ ] **Step 2: Run and prove red**

```bash
(cd adapters/postgres && go test . -run 'TestTemporalQuerySnapshot' -count=1)
```

- [ ] **Step 3: Implement one historical authority projection**

Use explicit SQL/CTEs with a single normalized cutoff parameter:

- `visible_entities`: `entities.recorded_at <= cutoff` when historical;
- `effective_resolution_decisions`: accepted decision visible by cutoff with no visible successor, joined to visible proposal/mention/entity;
- `effective_admissions`: decision visible by cutoff with no visible successor, partitioned by target kind;
- `qualified_observations`: visible observation with effective admitted `TargetObservation`;
- resolved endpoints: direct entity, or effective admitted mention plus effective admitted accepted identity decision;
- exact entity match: `all` requires every requested ID among the two resolved endpoints; `any` requires at least one;
- exact optional predicate filter;
- evidence/document/section projection with recorded-time visibility and immutable metadata.

Do not interpolate IDs, predicates, or timestamps into SQL. Use pgx parameters/arrays. Order rows by stable recorded time and IDs, but still let `internal/query` impose public ordering.

- [ ] **Step 4: Return structured coverage**

Produce closed coverage for:

- requested entity absent at scope;
- observation authority excluded;
- mention unresolved at scope;
- entity-match or predicate exclusion; and
- observation candidate retained for core valid-time evaluation.

Do not expose SQL, database URLs, or private values in errors.

- [ ] **Step 5: Run adapter unit and dependency tests**

```bash
(cd adapters/postgres && go test . -run 'TestTemporalQuerySnapshot|TestDependency' -count=1)
! rg -n 'stacks/internal|internal/analysis|cobra|viper|google|bedrock|anthropic|openai' adapters/postgres
```

- [ ] **Step 6: Review and commit**

Review SQL specifically for successor visibility at cutoff, independent admission targets, read-only transaction ownership, context propagation, and direct-entity semantics.

```bash
git add adapters/postgres/temporal_query.go adapters/postgres/temporal_query_test.go adapters/postgres/dependency_test.go
git commit -m "Read historical temporal snapshots"
```

### Task D2.2: Bridge adapter snapshots into the consumer-owned port

**Files:**
- Create: `internal/query/postgres.go`
- Create: `internal/query/postgres_observability.go`
- Create: `internal/query/postgres_test.go`
- Create: `internal/query/postgres_integration_test.go`

**Interfaces:**
- Consumes: `postgres.LoadTemporalQuerySnapshot` and D1 `Reader`.
- Produces:

```go
type PostgresRepository struct {
    Database interface {
        LoadTemporalQuerySnapshot(
            context.Context,
            postgres.TemporalQuerySelection,
            postgres.TemporalSnapshotObserver,
        ) (postgres.TemporalQuerySnapshot, error)
    }
    SnapshotObserver postgres.TemporalSnapshotObserver
}

func (repository PostgresRepository) Read(
    context.Context,
    ReadSelection,
) (ReadSnapshot, error)
```

- [ ] **Step 1: Write bridge mapping and cancellation tests**

```go
func TestPostgresRepositoryMapsNormalizedSelectionAndExactSnapshot(t *testing.T)
func TestPostgresRepositoryPreservesCanonicalTermsGroundingAndEvidenceRoles(t *testing.T)
func TestPostgresRepositoryRejectsMalformedAdapterRecords(t *testing.T)
func TestPostgresRepositoryPreservesCancellation(t *testing.T)
func TestPostgresRepositoryContainsNoSQLOrDriverPolicy(t *testing.T)
func TestPostgresRepositorySnapshotSpanUsesOnlyBoundedAttributes(t *testing.T)
```

- [ ] **Step 2: Run and prove red**

```bash
go test ./internal/query -run 'TestPostgresRepository' -count=1
```

- [ ] **Step 3: Implement the mechanical bridge**

Translate match mode, predicates, entity IDs, and optional cutoff without changing semantics. Translate every adapter observation/citation/coverage record into D1 types and defensively copy slices. The bridge must not:

- start a transaction;
- query SQL;
- apply valid-time selection;
- choose a state;
- drop counterevidence;
- fetch content; or
- introduce manager-specific predicate logic.

In `postgres_observability.go`, implement the adapter's narrow observer with the root `internal/observability` helper and an injected OpenTelemetry tracer. It starts `stacks.postgres.temporal_snapshot`, uses only the adapter's bounded attributes, calls `observability.FinishSpan`, and never records IDs, predicates, SQL, source metadata, citation text, database URLs, or full errors.

- [ ] **Step 4: Add real PostgreSQL cancellation acceptance**

Use `postgrestest.NewDatabase(t)`, the admin URL for isolated schema setup, and the application URL for the reader. Cancel the context before `Read` and assert `errors.Is(err, context.Canceled)`.

- [ ] **Step 5: Run focused tests and commit**

```bash
go test ./internal/query -run 'TestPostgresRepository' -count=1
git add internal/query/postgres.go internal/query/postgres_observability.go internal/query/postgres_test.go internal/query/postgres_integration_test.go
git commit -m "Bridge PostgreSQL temporal snapshots"
```

### Task D2.3: Prove Project Atlas historical PostgreSQL acceptance

**Files:**
- Create: `adapters/postgres/temporal_query_integration_test.go`
- Modify only for defects exposed by the acceptance test: `adapters/postgres/temporal_query.go`, `internal/query/postgres.go`, `internal/query/service.go`

- [ ] **Step 1: Seed a complete synthetic canonical corpus through public adapter writes**

Use isolated synthetic documents, sections, spans, entities, mentions, proposals, accepted/corrected decisions, admissions, and observations. Reuse constructors and public transaction methods; do not insert around canonical invariants except where a concurrency transaction must deliberately control commit order.

- [ ] **Step 2: Add historical authority tests**

```go
func TestTemporalQueryPostgresCurrentAndAsOfAuthorityRemainIndependent(t *testing.T)
func TestTemporalQueryPostgresLaterSupersessionDoesNotRewriteEarlierCutoff(t *testing.T)
func TestTemporalQueryPostgresEntityMatchAllAndAny(t *testing.T)
func TestTemporalQueryPostgresRoundTripsExactCitationsAndCanonicalTerms(t *testing.T)
func TestTemporalQueryPostgresUsesOneCoherentSnapshotDuringConcurrentAuthorityChange(t *testing.T)
func TestTemporalQueryPostgresPerformsNoWritesOrLeaseClaims(t *testing.T)
```

For concurrency, hold the query transaction after its first authority read with a test-only synchronization hook at the adapter boundary, commit a superseding decision on another connection, then release the reader. Assert the first result remains internally old/coherent and a subsequent read sees the new authority. Do not use sleeps.

- [ ] **Step 3: Run local PostgreSQL acceptance**

```bash
make db-up
make db-migrate
make db-status
make test-integration ENV_FILE=.env
```

Expected:

- all three canonical core migrations are applied and no migration is pending;
- core and configured optional fingerprints are clean;
- Project Atlas current/as-of results pass;
- the concurrent snapshot test is coherent;
- no write/lease/provider path is exercised.

- [ ] **Step 4: Prove migration immutability**

```bash
git diff origin/main...HEAD -- adapters/postgres/coremigrations/migrations adapters/postgres/directorymigrations/migrations
```

Expected: no output.

- [ ] **Step 5: Run complete D2 gates**

```bash
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
git diff --check
make db-status
make test-integration ENV_FILE=.env
```

- [ ] **Step 6: Whole-branch review and commit**

The reviewer checks the full SQL cutoff truth table, transaction isolation/access mode, cancellation identity, exact citation completeness, entity match semantics, no migration changes, and no provider/private fixtures.

```bash
git add adapters/postgres/temporal_query_integration_test.go adapters/postgres/temporal_query.go internal/query
git commit -m "Prove historical temporal queries in PostgreSQL"
```

## D2 Completion Criteria

- One repeatable-read, read-only adapter call reconstructs current or historical authority coherently.
- Exact canonical observations and citations reach the D1 service without SQL/driver leakage.
- Project Atlas proves current/as-of, later supersession invisibility, `all`/`any`, cancellation, read-only behavior, and concurrent snapshot coherence.
- Migration bytes/fingerprints remain unchanged and clean.
- D2 passes deterministic and local PostgreSQL gates plus whole-branch review.
- No CLI command exists yet.

---

# Phase D3 — CLI Trend Transport

## D3 Invariants

1. `query trend` parses exact flags into a typed request before database access.
2. `CommandQuery` requires only the database URL and query limits—never source, directory, model, or provider settings.
3. `config validate query` is offline and opens no database.
4. JSON exactly matches the normative v1 contract; text exposes the same facts, uncertainty, gaps, and role-separated citations.
5. Composition opens one canonical database owner lazily for the selected query invocation and closes it once.

### Task D3.1: Add typed query configuration and offline validation

**Files:**
- Modify: `internal/config/application.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/document.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/loader_test.go`
- Modify: `internal/config/document_test.go`
- Modify: `internal/config/database_test.go`
- Modify: `.env.example`
- Modify: `config.example.yaml`
- Modify: `config.example.json`

**Interfaces:**

```go
const (
    defaultQueryMaxEntities   = 16
    defaultQueryMaxPredicates = 32
    defaultQueryMaxChronology = 1000

    QueryMaxEntitiesEnvironmentVariable   = "STACKS_QUERY_MAX_ENTITIES"
    QueryMaxPredicatesEnvironmentVariable = "STACKS_QUERY_MAX_PREDICATES"
    QueryMaxChronologyEnvironmentVariable = "STACKS_QUERY_MAX_CHRONOLOGY"
)

type QuerySettings struct {
    MaxEntities   int
    MaxPredicates int
    MaxChronology int
}

const CommandQuery Command = "query"
```

- [ ] **Step 1: Write failing defaults/precedence/range tests**

Cover inclusive ranges `1..64`, `1..256`, and `1..10000`; environment-over-file-over-default precedence; strict YAML/JSON schema; independent loads; and errors that name only configuration keys/environment names, never values.

- [ ] **Step 2: Prove red**

```bash
go test ./internal/config -run 'Test.*Query' -count=1
```

- [ ] **Step 3: Implement typed loading and validation**

Add file keys `query.max_entities`, `query.max_predicates`, `query.max_chronology`, explicit environment bindings, defaults, schema entries, and `Settings.Validate(CommandQuery)`. Add `CommandQuery` to database URL validation. Do not require migration URL, providers, Google paths, manager IDs, or model configuration.

- [ ] **Step 4: Update examples without secrets**

Add the three non-secret limits. Do not add database credentials or example entity IDs.

- [ ] **Step 5: Run, review, and commit**

```bash
go test ./internal/config -count=1
git add internal/config .env.example config.example.yaml config.example.json
git commit -m "Configure bounded temporal queries"
```

### Task D3.2: Parse exact trend syntax into a typed invocation

**Files:**
- Modify: `internal/cli/runner.go`
- Modify: `internal/cli/runner_test.go`
- Create: `internal/cli/query_input.go`
- Create: `internal/cli/query_input_test.go`
- Modify: `internal/cli/config.go`
- Modify: `internal/cli/config_test.go`
- Modify: `internal/app/bootstrap.go`
- Modify: `internal/app/bootstrap_test.go`
- Modify: `internal/app/execute.go`
- Modify: `internal/app/execute_test.go`

**Interfaces:**

```go
type QueryOutput string

const (
    QueryOutputText QueryOutput = "text"
    QueryOutputJSON QueryOutput = "json"
)

type QueryInput struct {
    Request query.Request
    Output  QueryOutput
}
```

Add `Query *QueryInput` to `cli.Invocation`, `CommandQuery`, and `ActionTrend`.

- [ ] **Step 1: Write exhaustive parser tests**

Test exact repeatable `--entity`/`--predicate`, default `all` and `text`, exactly one slash per window, RFC 3339 normalization, half-open ordering, optional cutoff, duplicate/blank rejection, unsupported/positional input rejection, fresh command-tree flag isolation, and config flag placement.

- [ ] **Step 2: Prove red**

```bash
go test ./internal/cli ./internal/app -run 'Test.*Query|Test.*Trend' -count=1
```

- [ ] **Step 3: Implement the Cobra leaf**

Construct:

```text
stacks query trend --entity <id>... --before <start>/<end> --after <start>/<end>
  [--entity-match all|any] [--predicate <exact>...] [--known-as-of <rfc3339>]
  [--output text|json]
```

Parse flags in `internal/cli`; call `temporal.Between("before", ...)`, `temporal.Between("after", ...)`, and `temporal.KnownAsOf(...)`; do not pass raw strings to the service. Unsupported syntax must fail before application bootstrap/database construction.

- [ ] **Step 4: Add offline query configuration validation**

`stacks config validate query` selects `CommandQuery`, validates settings, emits only `configuration valid for query`, and never starts runtime dependencies.

- [ ] **Step 5: Run, review, and commit**

```bash
go test ./internal/cli ./internal/app -run 'Test.*Query|Test.*Trend' -count=1
git add internal/cli internal/app
git commit -m "Parse temporal trend commands"
```

### Task D3.3: Render deterministic v1 text and JSON

**Files:**
- Create: `internal/cli/query.go`
- Create: `internal/cli/query_json.go`
- Create: `internal/cli/query_text.go`
- Create: `internal/cli/query_test.go`
- Create: `internal/cli/query_json_test.go`
- Create: `internal/cli/query_text_test.go`

**Interfaces:**

```go
type QueryService interface {
    Query(context.Context, query.Request) (query.Result, error)
}

type QueryCommand struct {
    Service QueryService
    Output  io.Writer
}
```

- [ ] **Step 1: Write renderer parity and wire-shape tests**

Use one fully populated trend result and assert:

- exact schema version and top-level keys;
- exactly one `result.trend`;
- required normalized request fields;
- UTC microsecond RFC3339Nano `Z`;
- exact typed term/extent/contribution/citation shapes;
- `[]` rather than `null`;
- omitted optional locator/text/grounding fields;
- role-separated citation arrays;
- deterministic bytes across reordered inputs;
- text and JSON expose identical fact/change/unresolved/gap/citation IDs and roles;
- writer failures and malformed unions return errors without partial success.

- [ ] **Step 2: Prove red**

```bash
go test ./internal/cli -run 'TestQuery(Command|JSON|Text|Renderer)' -count=1
```

- [ ] **Step 3: Implement typed DTO conversion and validation**

Use explicit JSON DTO structs. The only map-like behavior permitted is a custom fixed one-key result union marshaler that validates the tag/member pair. No `map[string]any`. Call `query.ValidateResult` before writing.

- [ ] **Step 4: Implement concise text output**

Show intent, before/after windows, knowledge scope, facts, changes, unresolved items, gaps, contributions, and separate `supporting citations` / `contradicting citations` sections. Citation text is printed only when already present in the result; the renderer performs no lookup.

- [ ] **Step 5: Run, review, and commit**

```bash
go test ./internal/cli -run 'TestQuery' -count=1
git add internal/cli/query*.go
git commit -m "Render cited temporal trends"
```

### Task D3.4: Compose trend lazily and document the operator boundary

**Files:**
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`
- Modify: `cmd/stacks/canonical_composition_test.go`
- Modify: `Makefile`
- Modify: `README.md`

- [ ] **Step 1: Write composition tests first**

```go
func TestQueryTrendOpensOneCanonicalDatabaseAndClosesItOnce(t *testing.T)
func TestQueryTrendConstructsNoSourceDirectoryModelOrProvider(t *testing.T)
func TestQueryValidationFailsBeforeDatabaseConstruction(t *testing.T)
func TestQueryTrendPreservesCancellationAndBoundedTelemetry(t *testing.T)
```

- [ ] **Step 2: Prove red**

```bash
go test ./cmd/stacks -run 'TestQuery' -count=1
```

- [ ] **Step 3: Implement lazy composition**

After `CommandQuery` validation, open one application-role canonical database, create the root `query` PostgreSQL snapshot observer from the process tracer, inject it into `query.PostgresRepository`, create `query.Service` with configured limits and tracer, and register one `cli.QueryCommand`. Do not call model/disclosure/source/directory constructors. Reuse existing shutdown ownership so the database closes once.

- [ ] **Step 4: Document exact trend usage and boundary**

Add a `query-trend` Make target that loads the ignored `ENV_FILE` and forwards `ARGS` without embedding IDs. Document exact syntax, result/gap semantics, current versus `known-as-of`, configuration limits, citation disclosure, and that local acceptance is provider-free.

- [ ] **Step 5: Run CLI smoke without private data**

```bash
go run ./cmd/stacks query --help
go run ./cmd/stacks query trend --help
go run ./cmd/stacks config validate query
```

Expected: help succeeds without database/provider construction; config validation succeeds only when local non-secret settings/database URL requirements are met and does not connect.

- [ ] **Step 6: Run D3 deterministic and PostgreSQL gates**

```bash
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

- [ ] **Step 7: Whole-branch review and commit**

Review exact flag syntax, strict pre-I/O failures, JSON normative shape, text/JSON parity, lazy construction, shutdown, telemetry privacy, and documentation truthfulness.

```bash
git add cmd/stacks Makefile README.md
git commit -m "Expose cited temporal trends"
```

## D3 Completion Criteria

- `stacks query trend` and `stacks config validate query` implement the exact approved syntax and isolation.
- Text and JSON deterministically render the same cited typed trend.
- Query settings use exact defaults/ranges and require no provider/model/source configuration.
- Composition constructs only the canonical database/query boundary and closes it once.
- Deterministic and local PostgreSQL gates plus whole-branch review pass.
- Point, trajectory, and causal CLI execution remain explicitly incomplete.

---

# Phase D4 — Point, Trajectory, and Explicit Causal Intents

## D4 Invariants

1. All intents reuse the D1-D3 request/result/snapshot contract; no intent-specific repository or schema is added.
2. Point reconstructs state at one instant without turning absence into a negative fact.
3. Trajectory partitions only at canonical valid-time boundaries, orders transitions deterministically, preserves unresolved material, and fails atomically above limit.
4. Causal output uses only admitted `stacks.causal.v1/causes` observations. Chronology, shared entity, confidence, or similarity never manufactures a link.
5. Every new CLI leaf has exact parser, text, JSON, unit, PostgreSQL, and cancellation acceptance.

### Task D4.1: Implement pure point-in-time reconstruction

**Files:**
- Create: `core/temporal/point.go`
- Create: `core/temporal/point_test.go`
- Modify: `internal/query/service.go`
- Modify: `internal/query/result.go`
- Modify: `internal/query/service_test.go`
- Modify: `internal/query/project_atlas_test.go`

**Interface:**

```go
type PointSummary struct {
    Selection  TemporalSelection
    Facts      []Fact
    Unresolved []UnresolvedFact
}

func ReconstructState(
    selection TemporalSelection,
    scope KnowledgeScope,
    candidates []StateCandidate,
) (PointSummary, error)
```

- [ ] Write failing tests for instant inclusion, half-open interval endpoints, open intervals, unknown/window uncertainty, conflict, hypothesis, counterevidence-only, cutoff independence, input order, and confidence independence.
- [ ] Run:

```bash
(cd core && go test ./temporal -run 'TestReconstructState' -count=1)
```

- [ ] Implement point eligibility with shared aggregation primitives, not by widening a point into an arbitrary window.
- [ ] Wire point payload projection/citations/gaps through `Service.Query`.
- [ ] Run core/query tests, two independent reviews, and commit:

```bash
git add core/temporal/point.go core/temporal/point_test.go internal/query
git commit -m "Reconstruct cited temporal state"
```

### Task D4.2: Implement bounded deterministic trajectories

**Files:**
- Create: `core/temporal/trajectory.go`
- Create: `core/temporal/trajectory_test.go`
- Modify: `internal/query/service.go`
- Modify: `internal/query/result.go`
- Modify: `internal/query/order.go`
- Modify: `internal/query/service_test.go`
- Modify: `internal/query/project_atlas_test.go`

**Interface:**

```go
type Transition struct {
    Kind       ChangeKind
    Key        StateKey
    ValidTime  observation.TemporalExtent
    Before     *Fact
    After      *Fact
    Unresolved []UnresolvedFact
}

func BuildTrajectory(
    selection TemporalSelection,
    scope KnowledgeScope,
    candidates []StateCandidate,
) ([]Transition, error)
```

- [ ] Write failing tests for boundary partitioning, added/removed/changed states, overlapping conflict, uncertainty, identical instants, stable ties, and no confidence ordering.
- [ ] Prove red:

```bash
(cd core && go test ./temporal -run 'TestBuildTrajectory' -count=1)
```

- [ ] Implement deterministic partition/aggregate/diff/order from canonical temporal extents.
- [ ] In the service, calculate the complete transitions, compare count to requested/configured limits, and return `ErrLimitExceeded` with no partial result.
- [ ] Run reviews and commit:

```bash
git add core/temporal/trajectory.go core/temporal/trajectory_test.go internal/query
git commit -m "Build bounded cited trajectories"
```

### Task D4.3: Implement explicit causal-chain retrieval

**Files:**
- Create: `core/temporal/causal.go`
- Create: `core/temporal/causal_test.go`
- Modify: `internal/query/service.go`
- Modify: `internal/query/result.go`
- Modify: `internal/query/order.go`
- Modify: `internal/query/service_test.go`
- Modify: `internal/query/project_atlas_test.go`

**Interface:**

```go
const CausalPredicate observation.Predicate = "stacks.causal.v1/causes"

type CausalLink struct {
    Cause                    observation.Term
    Effect                   observation.Term
    ObservationIDs           []observation.ObservationID
    SupportingEvidenceIDs    []evidence.EvidenceID
    ContradictingEvidenceIDs []evidence.EvidenceID
}

func BuildCausalChain(
    selection TemporalSelection,
    scope KnowledgeScope,
    candidates []StateCandidate,
) ([]CausalLink, error)
```

- [ ] Write failing tests proving explicit predicate-only inclusion, typed effect-to-next-cause equality, chronological ordering, cutoff behavior, counterevidence preservation, no-confidence selection, chronology-only rejection, no-causal-evidence gap, and limit failure.
- [ ] Prove red:

```bash
(cd core && go test ./temporal -run 'TestBuildCausalChain' -count=1)
```

- [ ] Implement exact causal observation selection/linking. Never infer a link from temporal adjacency or a shared requested entity.
- [ ] Wire causal payload and gap projection through the service.
- [ ] Run reviews and commit:

```bash
git add core/temporal/causal.go core/temporal/causal_test.go internal/query
git commit -m "Retrieve explicit cited causal chains"
```

### Task D4.4: Expose and accept all remaining CLI leaves

**Files:**
- Modify: `internal/cli/runner.go`
- Modify: `internal/cli/runner_test.go`
- Modify: `internal/cli/query_input.go`
- Modify: `internal/cli/query_input_test.go`
- Modify: `internal/cli/query.go`
- Modify: `internal/cli/query_json.go`
- Modify: `internal/cli/query_json_test.go`
- Modify: `internal/cli/query_text.go`
- Modify: `internal/cli/query_text_test.go`
- Modify: `adapters/postgres/temporal_query_integration_test.go`
- Modify: `internal/query/postgres_integration_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write exact parser tests for all leaves**

Cover:

```text
query point --entity ... --at ... [common flags]
query trajectory --entity ... --between ... --limit ... [common flags]
query causal --entity ... --between ... --limit ... [no --predicate]
```

Assert missing/zero/negative/over-maximum limits and causal predicate flags fail before database access.

- [ ] **Step 2: Add text/JSON payload tests**

Assert exact point, trajectory, and causal union members; before/after omission rules; non-null arrays; transition valid time; causal contributions/citations; gap placement only at envelope level; and renderer parity.

- [ ] **Step 3: Implement leaves using the existing query service**

Do not add commands or services per intent. The causal leaf supplies the sole causal predicate internally.

- [ ] **Step 4: Extend PostgreSQL acceptance**

Project Atlas must prove:

- point before/at/after a boundary;
- trajectory with responsibility transfer, conflict, and uncertainty;
- positive two-link explicit causal chain with counterevidence;
- chronology without explicit causal predicate produces `no-causal-evidence`;
- current versus historical cutoff changes authority, not valid-time meaning; and
- each limit overflow returns no partial result.

- [ ] **Step 5: Run all four synthetic CLI smoke commands**

Use only fixture IDs seeded into the isolated test database; do not use private IDs:

```bash
go run ./cmd/stacks query point --help
go run ./cmd/stacks query trend --help
go run ./cmd/stacks query trajectory --help
go run ./cmd/stacks query causal --help
```

- [ ] **Step 6: Run D4 complete gates**

```bash
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

- [ ] **Step 7: Whole-branch review and commit**

The reviewer independently attempts to find inferred causality, partial limit output, provider construction, valid/recorded conflation, unstable ordering, nullable JSON collections, and intent-specific SQL duplication.

```bash
git add core/temporal internal/query internal/cli adapters/postgres/temporal_query_integration_test.go README.md
git commit -m "Expose all cited temporal intents"
```

## D4 Completion Criteria

- Point, trend, trajectory, and causal intents execute through the same typed service and historical snapshot.
- All four exact CLI leaves and normative text/JSON payloads pass.
- Trajectory/causal limits fail atomically; causality is explicit-predicate-only.
- Deterministic and PostgreSQL acceptance plus whole-branch review pass.
- Specialized manager-confidence code still exists pending D5 parity/removal.

---

# Phase D5 — Generic Parity and Manager-Confidence Removal

## D5 Invariants

1. Removal begins only after generic parity passes through the public typed service, CLI/JSON transport, and canonical PostgreSQL.
2. Parity means chronology, exact support, exact counterevidence, conflicts/uncertainty, and gaps—not manager labels, scores, conclusions, or prose.
3. Generic packages must never import or name the manager-confidence vertical.
4. Historical design documents remain; runtime/current docs and code lose specialized commands/configuration/policy.
5. Removal does not alter canonical migration bytes, ledgers, fingerprints, or stored observation semantics.

### Task D5.1: Prove generic typed, CLI, and PostgreSQL evidence parity

**Files:**
- Create: `internal/query/parity_test.go`
- Modify: `internal/cli/query_json_test.go`
- Modify: `adapters/postgres/temporal_query_integration_test.go`

- [ ] **Step 1: Define the generic two-person responsibility fixture**

Use generic entities such as `entity:atlas-owner` and `entity:atlas-reviewer`, generic predicates, synthetic exact spans, dated/unknown observations, supporting/contradicting evidence, reviewed mention identity, and current/as-of admission changes. Do not use employee/manager roles, confidence labels, analysis conclusions, transcript types, or private text.

- [ ] **Step 2: Write the public typed-service parity test**

```go
func TestGenericTemporalQueryPreservesVerticalEvidenceParity(t *testing.T)
```

Assert the typed result preserves the same evidence shape needed by a downstream bounded interpretation: chronology, support, counterevidence, conflicts, uncertainty, and explicit gaps.

- [ ] **Step 3: Write CLI/JSON parity**

Marshal the same result through `cli.QueryCommand`; decode into the exact v1 DTO and assert no evidence category or temporal distinction is lost.

- [ ] **Step 4: Write PostgreSQL parity**

Persist the fixture through canonical adapter writes and run the public `query.Service` through `query.PostgresRepository`. Assert structural equality with the in-memory typed-service expected result after canonical ordering.

- [ ] **Step 5: Run all parity gates**

```bash
go test ./internal/query -run TestGenericTemporalQueryPreservesVerticalEvidenceParity -count=1
go test ./internal/cli -run 'Test.*Parity' -count=1
make test-integration ENV_FILE=.env
```

Expected: all pass before any specialized file is deleted.

- [ ] **Step 6: Independent parity review and commit**

The reviewer verifies equivalence is evidence-structural, not manager-specific output mimicry, and checks that the test uses only public typed/CLI/PostgreSQL boundaries.

```bash
git add internal/query/parity_test.go internal/cli/query_json_test.go adapters/postgres/temporal_query_integration_test.go
git commit -m "Prove generic temporal evidence parity"
```

### Task D5.2: Decouple canonical interaction mapping from analysis policy

**Files:**
- Create: `internal/extract/observation.go`
- Create: `internal/extract/observation_test.go`
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/ingest/canonical_test.go`
- Modify: `internal/source/drive/chronology_test.go`

- [ ] **Step 1: Write extraction-owned predicate tests**

Move the observable tests from `internal/analysis/observation_test.go` into the extraction contract and add an import-boundary assertion:

```go
func TestInteractionObservationPredicateRoundTripsEveryExtractV2Signal(t *testing.T)
func TestInteractionObservationPredicateRejectsUnknownCategoryAndDirection(t *testing.T)
func TestIngestionBuildsCanonicalInteractionObservationsWithoutAnalysisImport(t *testing.T)
```

- [ ] **Step 2: Run and prove the new boundary is red**

```bash
go test ./internal/extract ./internal/ingest -run 'TestInteractionObservationPredicate|TestIngestionBuildsCanonicalInteraction' -count=1
```

- [ ] **Step 3: Move the narrow versioned mapping**

Because `extract-v2` still emits interaction observations, move only:

```go
func InteractionObservationPredicate(category, direction string) (observation.Predicate, error)
func ParseInteractionObservationPredicate(
    observation.Predicate,
) (category, direction string, err error)
```

to `internal/extract/observation.go`. Update ingestion and the provider-neutral chronology test to consume `extract`, then remove their `internal/analysis` imports. Do not move report status, conclusion admission, model narration, pair roles, or analysis policy.

- [ ] **Step 4: Run focused tests and the import scan**

```bash
go test ./internal/extract ./internal/ingest ./internal/source/drive -count=1
! rg -n 'stacks/internal/analysis' internal/ingest internal/source/drive
```

- [ ] **Step 5: Independent reviews and commit**

The specification reviewer confirms `extract-v2` durable predicate bytes remain unchanged. The quality reviewer confirms extraction owns only its schema-to-observation mapping and no manager analysis policy was renamed.

```bash
git add internal/extract/observation.go internal/extract/observation_test.go internal/ingest internal/source/drive/chronology_test.go
git commit -m "Decouple canonical interaction mapping"
```

### Task D5.3: Remove the pair-analysis prompt and configuration contract

**Files:**
- Delete: `internal/extract/prompts/analyze-v1.txt`
- Modify: `internal/extract/schema.go`
- Modify: `internal/extract/schema_test.go`
- Modify: `internal/extract/validate.go`
- Modify: `internal/extract/validate_test.go`
- Modify: `internal/bedrock/client_test.go`
- Modify: `internal/config/application.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/document.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/loader_test.go`
- Modify: `internal/config/document_test.go`
- Modify: `internal/config/database_test.go`
- Modify: `internal/config/model_settings_test.go`

- [ ] **Step 1: Write retired-key and extraction-preservation tests**

```go
func TestConfigDocumentRejectsRetiredAnalysisSection(t *testing.T)
func TestLoadDoesNotBindRetiredAnalysisEnvironmentInputs(t *testing.T)
func TestPromptContractSupportsExtractionOnly(t *testing.T)
func TestExtractV2SchemaAndPromptRemainByteStable(t *testing.T)
```

Capture the current `extract-v2` prompt/schema digest in the test before deleting pair analysis so this task cannot accidentally mutate ingestion.

- [ ] **Step 2: Run the focused tests and prove red**

```bash
go test ./internal/config ./internal/extract ./internal/bedrock -run 'Test(ConfigDocumentRejectsRetired|LoadDoesNotBindRetired|PromptContractSupportsExtractionOnly|ExtractV2Schema)' -count=1
```

- [ ] **Step 3: Delete pair-analysis prompt/schema support**

Remove `AnalysisPromptVersion`, `AnalysisSchemaName`, `AnalysisJSONSchema`, its embedded prompt, schema bytes, and provider transport test case. Keep the provider-neutral `Model` interface and `extract-v2` contract unchanged.

- [ ] **Step 4: Delete specialized configuration**

Remove `analysis.prompt_version`, `STACKS_ANALYSIS_PROMPT_VERSION`, `ManagerConfidenceSettings`, `STACKS_EMPLOYEE_ENTITY_ID`, and `STACKS_MANAGER_ENTITY_ID`. Remove `CommandAnalyze` validation and model/disclosure requirements used only by that command. Strict YAML/JSON validation must reject the retired `analysis` object. Environment values with retired names are no longer read or bound.

- [ ] **Step 5: Run focused tests and scans**

```bash
go test ./internal/config ./internal/extract ./internal/bedrock -count=1
! rg -n 'AnalysisPromptVersion|AnalysisSchemaName|AnalysisJSONSchema|STACKS_ANALYSIS_PROMPT_VERSION|STACKS_EMPLOYEE_ENTITY_ID|STACKS_MANAGER_ENTITY_ID|pair_analysis|analyze-v1' internal
```

- [ ] **Step 6: Independent reviews and commit**

The reviewers check strict retired-key rejection, unchanged `extract-v2`, intact sync provider selection, and absence of secret values in errors.

```bash
git add -A internal/config internal/extract internal/bedrock/client_test.go
git commit -m "Remove pair analysis configuration"
```

### Task D5.4: Remove the analyze command, composition, and policy package

**Files:**
- Delete all `internal/analysis/*.go`
- Delete: `internal/cli/analyze.go`
- Delete: `internal/cli/analyze_test.go`
- Modify: `internal/cli/runner.go`
- Modify: `internal/cli/runner_test.go`
- Modify: `internal/cli/config.go`
- Modify: `internal/cli/config_test.go`
- Modify: `internal/app/bootstrap.go`
- Modify: `internal/app/bootstrap_test.go`
- Modify: `internal/app/execute.go`
- Modify: `internal/app/execute_test.go`
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`
- Modify: `cmd/stacks/canonical_composition_test.go`
- Modify: `Makefile`

- [ ] **Step 1: Record the specialized runtime surface**

```bash
rg -n 'CommandAnalyze|internal/analysis|AnalyzeCommand|analysis\\.Repository|make analyze|^analyze:' internal cmd Makefile
```

- [ ] **Step 2: Remove routing and composition before deleting the package**

Remove the analyze Cobra leaf/config target, app routing, command map entry, model/disclosure construction, repository field, service constructor, telemetry path, and Make target. Preserve sync/model construction and all four generic query leaves.

- [ ] **Step 3: Delete the renderer and policy package**

Delete `internal/cli/analyze.go`, its tests, and every file in `internal/analysis`. Leave no aliases, forwarding wrappers, empty package, deprecated command, or compatibility input.

- [ ] **Step 4: Run focused command/composition tests and scans**

```bash
go test ./internal/cli ./internal/app ./cmd/stacks -count=1
! rg -n 'CommandAnalyze|stacks/internal/analysis|AnalyzeCommand|analysis\\.Repository|^analyze:' internal cmd Makefile
```

- [ ] **Step 5: Independent reviews and commit**

The reviewers confirm generic query composition remains lazy/provider-free, sync still owns its provider path, shutdown behavior remains exact, and no manager policy was renamed.

```bash
git add -A internal/analysis internal/cli internal/app cmd/stacks Makefile
git commit -m "Remove specialized manager analysis"
```

### Task D5.5: Remove runtime documentation/configuration and run final acceptance

**Files:**
- Modify: `.env.example`
- Modify: `config.example.yaml`
- Modify: `config.example.json`
- Modify: `README.md`
- Modify: `AGENTS.md` only where its current project-structure text incorrectly claims `internal/analysis` is active
- Modify: `adapters/postgres/coremigrations/manifest_test.go`

- [ ] **Step 1: Update current documentation to the generic product**

Describe Stacks' four cited temporal query intents, explicit identity workflow prerequisite, query limits, exact citation disclosure, PostgreSQL acceptance, and the absence of query-time provider calls. Remove current operational claims about `analyze`, manager conclusions, employee/manager environment inputs, pair-analysis prompts, and provider-paid analysis acceptance. Keep historical approved specs/plans unchanged.

- [ ] **Step 2: Run the complete deletion audit**

```bash
! rg -n -i 'manager.confidence|manager confidence|manager-confidence|CommandAnalyze|STACKS_EMPLOYEE_ENTITY_ID|STACKS_MANAGER_ENTITY_ID|AnalysisPrompt|pair_analysis|analyze-v1|internal/analysis' \
  --glob '!docs/superpowers/specs/**' --glob '!docs/superpowers/plans/**' .
```

Expected: no runtime or current-documentation matches.

- [ ] **Step 3: Prove migrations and fingerprints are untouched**

```bash
git diff origin/main...HEAD -- adapters/postgres/coremigrations/migrations adapters/postgres/directorymigrations/migrations
make db-status
```

Expected: no migration byte changes; all configured scopes clean.

- [ ] **Step 4: Run full deterministic gates**

```bash
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
git diff --check
```

- [ ] **Step 5: Run full local PostgreSQL acceptance**

```bash
make db-up
make db-migrate
make db-status
make test-integration ENV_FILE=.env
```

Expected: generic parity, all four intents, historical authority, concurrency, citation round-trip, cancellation, no-write, and fingerprints pass against canonical PostgreSQL 18.

- [ ] **Step 6: Run final public CLI smoke**

```bash
go run ./cmd/stacks query point --help
go run ./cmd/stacks query trend --help
go run ./cmd/stacks query trajectory --help
go run ./cmd/stacks query causal --help
go run ./cmd/stacks config validate query
```

Expected: all query help works without external construction; retired `analyze` is absent.

- [ ] **Step 7: Run independent whole-branch review**

Give the reviewer:

- approved design and this plan;
- `git diff origin/main...HEAD`;
- parity evidence from before deletion;
- exact deterministic/PostgreSQL output;
- deletion-audit output; and
- migration immutability output.

The reviewer must confirm every removal-gate item, all four intent contracts, no provider/query coupling, no private fixtures, and no generic import/name of the retired vertical.

- [ ] **Step 8: Fix findings test-first, rerun all gates, and commit**

```bash
git add AGENTS.md README.md .env.example config.example.yaml config.example.json adapters/postgres/coremigrations/manifest_test.go
git commit -m "Document generic temporal queries"
```

## D5 and Plan D Completion Criteria

- Generic typed-service, CLI/JSON, and PostgreSQL parity passed before removal.
- Specialized analyze command, application policy, repository, signal/report types, prompt/schema, configuration, composition, telemetry, Make target, and current documentation are removed.
- Historical design documents remain unchanged.
- All four cited intents pass deterministic and local PostgreSQL acceptance.
- Canonical migrations, ledgers, and fingerprints remain unchanged and clean.
- A fresh whole-branch reviewer confirms no generic package imports or names the retired vertical.
- The final report states exactly which deterministic/local PostgreSQL gates passed and explicitly states that Drive, Directory, model-provider, cloud, web, cache, and private-corpus acceptance were not exercised.

## Final Delivery Boundary

After a phase is locally committed, reviewed, and green:

1. report the phase commits, exact checks, PostgreSQL status where applicable, and any unvalidated external boundaries;
2. stop for user approval;
3. do not push or create a PR until explicitly asked; and
4. do not start the next phase until the current phase is reviewed and merged and a fresh chat verifies current `main`.
