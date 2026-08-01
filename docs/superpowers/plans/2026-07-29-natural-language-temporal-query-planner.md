# Natural-Language Temporal Query Planner Implementation Plan

**Status:** Implemented and published. A bounded synthetic OpenAI validation
was completed after separate authorization; see the
[Plan E closeout report](../reports/2026-08-01-plan-e-closeout.md). Unchecked
task and gate boxes below preserve the original executable plan; completion
evidence is recorded in the closeout report rather than rewritten into the
historical plan.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a planner-only `stacks query ask` workflow that turns one bounded private question into the existing closed temporal query request, validates and audits that request, and executes it through the unchanged deterministic cited-query service.

**Architecture:** A new provider-neutral `internal/queryplan` package owns the exact `query-plan-v1` prompt/schema, pre-disclosure validation, strict proposal decoding, deterministic request composition, and the transport-neutral ask service. Existing provider clients add planner-specific methods while retaining their exact extraction contracts; CLI, application, and root composition then read stdin, construct the selected provider lazily, and open PostgreSQL only after a normalized request exists. The existing `internal/query`, `core/temporal`, PostgreSQL bridge, result validation, and citation-preserving renderers remain authoritative.

**Tech Stack:** Go 1.26.0, PostgreSQL 18, pgx v5.10.0, Cobra v1.10.2, Viper v1.21.0, OpenAI Responses API, Anthropic Messages API, AWS Bedrock Converse, Zap/OpenTelemetry, Go's standard `testing` package, and repository-pinned Staticcheck 2026.1.

## Global Constraints

- This plan implements only natural-language planning followed by deterministic execution. Narration, entity matching, persistence, caching, HTTP/web UX, new intents, and schema changes remain separate future designs.
- Model output is untrusted structured input. Only `query.NormalizeRequest`, `core/temporal`, and `query.Service` may establish request validity, temporal semantics, retrieval, conflict handling, causality eligibility, limits, citations, and final results.
- Every canonical entity ID comes from the caller, is validated before disclosure, is omitted from the provider request, and is attached unchanged to the locally composed request. Reject a question containing any supplied canonical ID verbatim.
- Require an explicit RFC3339 reference instant for every ask invocation. Normalize it to UTC microsecond precision and never use the wall clock to interpret the question.
- The planner receives only the question, normalized reference instant, configured query limits, entity count, and the statement that IDs are attached locally. It never receives IDs, evidence, facts, citations, source text, database state, or prior results.
- Valid time and recorded time remain independent. Limits fail explicitly; no result or proposal is silently truncated.
- Causal-chain planning requires the exact existing `query.CausalPredicate`; temporal precedence cannot establish causality.
- The exact contract versions are `query-plan-v1`, `temporal_query_plan_v1`, and `query-ask-v1`. Prompt and schema bytes are embedded, isolated, fingerprinted, and accepted only as an exact pair.
- OpenAI and Anthropic remain personal-data-only. Restricted data remains Bedrock-only and must pass the existing fail-closed invocation-logging preflight before any provider invocation.
- Same-provider retries are allowed only for 429/provider throttling, approved transient server/unavailable failures, and approved retryable transport failures. There is no provider/model failover, and SDK retries remain disabled or neutralized.
- `STACKS_QUERY_PLANNER_TIMEOUT` defaults to `60s`, accepts `1s` through `5m`, and bounds provider attempts plus backoff. `STACKS_QUERY_PLANNER_MAX_QUESTION_BYTES` defaults to `16384`, accepts `1` through `65536`, and the CLI reads at most limit plus one byte.
- Provider/model settings are required only for `query ask`. Existing typed point, trend, trajectory, and causal commands remain provider-free and byte-compatible.
- No stdout is written until planning, normalization, retrieval, result validation, and complete rendering all succeed.
- Never log or trace questions, private model input, prompt/schema bodies, raw or decoded output, IDs, predicates, timestamps, normalized requests, facts, conflicts, gaps, names, citations, provider IDs or bodies, SQL, database URLs, or credentials.
- Use only synthetic fixtures. Do not inspect secrets, invoke live providers, or read private source contents during implementation or local acceptance.
- Preserve every canonical migration byte and fingerprint. Any migration diff stops implementation and requires a separately approved forward-migration design.
- Use a fresh implementation subagent for every numbered task. After focused tests pass, use a fresh specification reviewer and then a different fresh code-quality reviewer; keep the implementer available to fix findings test-first.
- At every E1-E4 phase gate, use another fresh whole-phase reviewer. After E4, use a fresh whole-branch reviewer with the approved design, this plan, all task reports, and the complete branch diff.
- Design, planning, local implementation, publication, live-provider validation, and private-corpus validation are separate approval gates. Do not push, open a PR, merge, invoke a provider, or use an administrator bypass without explicit approval for that gate.

---

## Branch, Subagent, and Review Protocol

- [ ] Before implementation, verify that the approved planning commit is present and that the implementation branch descends from freshly fetched `origin/main`:

```bash
git fetch origin
git status --short --branch
git rev-parse HEAD
git rev-parse origin/main
git merge-base HEAD origin/main
git log -1 --format='%H %s'
```

Expected: the worktree is clean; `origin/main` contains `c6852d2` or a reviewed successor; the design and this plan are committed; no history rewrite occurs. If `origin/main` moved, inspect the intervening diff before creating the implementation branch.

- [ ] Read the complete governing material before editing:

```bash
sed -n '1,500p' AGENTS.md
sed -n '1,1000p' README.md
sed -n '1,240p' docs/superpowers/specs/2026-07-22-personal-model-providers-design.md
sed -n '1,1400p' docs/superpowers/specs/2026-07-28-natural-language-temporal-query-planner-design.md
sed -n '1,2600p' docs/superpowers/plans/2026-07-29-natural-language-temporal-query-planner.md
sed -n '1,800p' docs/superpowers/specs/2026-07-25-open-source-temporal-engine-design.md
sed -n '1,700p' docs/superpowers/specs/2026-07-25-canonical-postgres-reset-design.md
sed -n '1,1100p' docs/superpowers/specs/2026-07-26-cited-temporal-query-design.md
sed -n '1,2000p' docs/superpowers/plans/2026-07-26-cited-temporal-query.md
```

- [ ] Record the pre-change baseline and migration fingerprints:

```bash
make test
make staticcheck
git diff --check
find adapters/postgres -path '*/migrations/*' -type f -print0 | sort -z | xargs -0 shasum -a 256
```

Expected: all three checks pass. Save the migration digest output in the local E1 report and compare it again at E4.

- [ ] For each task, create `.superpowers/sdd/plan-e/task-N-brief.md`, replacing `N` with the current task number, containing only the approved design, this plan's global constraints, the current task, branch evidence, and its consumed interfaces. Dispatch a fresh implementer; never reuse an implementer for another numbered task.

- [ ] After the implementer reports focused green tests, save its report as `.superpowers/sdd/plan-e/task-N-report.md`, replacing `N` with the current task number; dispatch a fresh specification reviewer, fix every actionable finding test-first, then dispatch a different fresh code-quality reviewer. Repeat review after material fixes.

- [ ] At each phase gate, provide a fresh reviewer with the phase diff, task reports, exact test output, and a checklist for authority, identity, time, privacy, provider boundaries, citation preservation, and excluded scope. Do not begin the next phase while actionable findings remain.

## Dependency and Phase Map

```text
E1 exact planner contract
  -> E1 input/proposal composition
      -> E1 transport-neutral orchestration + audit
          -> E2 OpenAI planner adapter
          -> E2 Anthropic planner adapter
          -> E2 Bedrock planner adapter
              -> E3 planner configuration
                  -> E3 CLI stdin + audit rendering
                      -> E3 application stdin routing
                          -> E3 lazy root composition
                              -> E4 deterministic/PostgreSQL parity + docs
                                  -> final whole-branch acceptance and review
```

The three E2 provider tasks may be implemented independently after E1 is approved, but integrate and review them sequentially so each provider's focused tests and extraction-regression checks run against the current branch head.

## File Map

### E1 — exact planner boundary

- Modify `internal/query/contract.go` and `contract_test.go`: export `ValidateLimits` without changing its validation policy; `NormalizeRequest` delegates to it.
- Create `internal/queryplan/contract.go` and `contract_test.go`: consumer-owned model, input, metadata, execution, and exact prompt-contract types.
- Create `internal/queryplan/dependency_test.go`: prevent provider SDK, provider adapter, Cobra, Viper, pgx, PostgreSQL adapter, and HTTP transport imports.
- Create `internal/queryplan/prompts/query-plan-v1.txt`: exact versioned planner system prompt.
- Create `internal/queryplan/schema.go` and `schema_test.go`: exact embedded JSON Schema and contract fingerprints.
- Create `internal/queryplan/input.go` and `input_test.go`: pre-disclosure normalization and deterministic private input serialization.
- Create `internal/queryplan/proposal.go` and `proposal_test.go`: strict wire decoding, bounded cannot-plan error, intent-specific conversion, and local ID attachment.
- Create `internal/queryplan/service.go` and `service_test.go`: planner timeout, model invocation, normalized execution, result parity, audit envelope, cancellation precedence, and stdout-independent orchestration.
- Modify `internal/modeltelemetry/recorder.go` and `recorder_test.go`: record question byte count as one histogram measurement without attributes.

### E2 — provider adapters

- Modify `internal/openai/client.go` and `client_test.go`: add `queryplan.Model`, exact planner request mapping, returned attempts/latency metadata, and preserve extraction behavior.
- Modify `internal/anthropic/client.go` and `client_test.go`: add the same consumer contract using native Messages structured output and terminal refusal/incomplete behavior.
- Modify `internal/bedrock/client.go` and `client_test.go`: add the same consumer contract using Converse structured output while preserving adaptive same-provider retries and restricted-mode eligibility at the root preflight.

### E3 — configuration, CLI, application, and composition

- Modify `internal/config/application.go`, `config.go`, `loader.go`, `document.go`, and their tests: add `CommandQueryAsk`, typed planner settings, exact defaults/ranges, strict file keys, and command-specific validation.
- Modify `.env.example`, `config.example.yaml`, and `config.example.json`: document only the new non-secret planner settings.
- Modify `internal/cli/runner.go`, `runner_test.go`, `config.go`, and `config_test.go`: add `query ask` and `config validate query ask`.
- Create `internal/cli/query_ask.go`, `query_ask_test.go`, `query_ask_json.go`, `query_ask_json_test.go`, `query_ask_text.go`, and `query_ask_text_test.go`: flag-only invocation, bounded stdin, pre-provider input normalization, lazy service factory, and atomic audit rendering.
- Modify `internal/cli/query_json.go`, `query_json_test.go`, `query_text.go`, and `query_text_test.go`: expose private reusable conversion/render helpers without changing typed command bytes.
- Modify `internal/app/execute.go`, `execute_test.go`, `bootstrap.go`, and `bootstrap_test.go`: pass injected stdin to commands and select `CommandQueryAsk` only for the ask leaf.
- Modify `cmd/stacks/main.go`, `main_test.go`, `model.go`, `model_test.go`, and `canonical_composition_test.go`: construct planner providers lazily, share the normalized query executor, enforce disclosure policy, and prove no unrelated dependency is constructed.
- Create `cmd/stacks/query.go` and `query_test.go`: lazy root-owned PostgreSQL executor shared by typed and planned query paths.

### E4 — parity, integration, and accurate documentation

- Create `internal/queryplan/parity_test.go`: four-intent fake-planner/direct-service exact parity plus conflict/citation/gap/cutoff preservation.
- Create `internal/queryplan/postgres_integration_test.go`: four-intent planned/direct parity against the synthetic canonical PostgreSQL corpus.
- Modify `Makefile` and `scripts/check-test-integration-packages.sh`: include `./internal/queryplan` in explicit integration acceptance.
- Modify `README.md`: document stdin usage, exact audit semantics, typed fallback, configuration, privacy boundary, and unrun live-provider boundary.

---

# Phase E1 — Exact Contract and Deterministic Composition

## E1 Invariants

1. `internal/queryplan` has no provider SDK, Cobra, Viper, pgx, PostgreSQL adapter, HTTP framework, or filesystem dependency.
2. Contract lookup accepts exactly `query-plan-v1`, returns isolated prompt/schema bytes, and exposes no arbitrary prompt path.
3. Input normalization happens before provider construction and never places IDs in the serialized model request.
4. Strict proposal decoding rejects unknown fields, trailing JSON, invalid status/reason pairs, invalid labels, bad timestamps, invented fields, and configured-limit violations.
5. Only a request accepted by `query.NormalizeRequest` can reach the executor.
6. The result returned by the executor must describe the exact normalized request; no model is called after retrieval.

### Task 1: Define and lock the exact planner contract

**Files:**
- Modify: `internal/query/contract.go`
- Modify: `internal/query/contract_test.go`
- Create: `internal/queryplan/contract.go`
- Create: `internal/queryplan/contract_test.go`
- Create: `internal/queryplan/dependency_test.go`
- Create: `internal/queryplan/schema.go`
- Create: `internal/queryplan/schema_test.go`
- Create: `internal/queryplan/prompts/query-plan-v1.txt`

**Interfaces:**
- Consumes: `query.Limits`, `query.Request`, `query.Result`, `identity.EntityID`, `modelpolicy.Provider`, and `timepoint.Normalize`.
- Produces:

```go
const (
    PromptVersion       = "query-plan-v1"
    SchemaName          = "temporal_query_plan_v1"
    OutputSchemaVersion = "query-ask-v1"
)

type Input struct {
    Question      string
    EntityIDs     []identity.EntityID
    ReferenceTime time.Time
}

type Usage struct {
    InputTokens  int64
    OutputTokens int64
    TotalTokens  int64
}

type ModelRequest struct {
    PromptVersion string
    SystemPrompt  string
    Input         string
    SchemaName    string
    JSONSchema    []byte
}

type ModelResponse struct {
    Output          json.RawMessage
    Provider        modelpolicy.Provider
    ModelID         string
    PromptVersion   string
    SchemaName      string
    Usage           Usage
    Attempts        int
    WallLatency     time.Duration
    ProviderLatency time.Duration
}

type Model interface {
    Plan(context.Context, ModelRequest) (ModelResponse, error)
}

type Executor interface {
    Query(context.Context, query.Request) (query.Result, error)
}

type PlannerMetadata struct {
    Provider        modelpolicy.Provider
    ModelID         string
    PromptVersion   string
    SchemaName      string
    Usage           Usage
    Attempts        int
    WallLatency     time.Duration
    ProviderLatency time.Duration
}

type Execution struct {
    SchemaVersion string
    ReferenceTime time.Time
    Request       query.Request
    Planner       PlannerMetadata
    Result        query.Result
}

type Contract struct {
    Version      string
    SystemPrompt string
    SchemaName   string
    JSONSchema   []byte
}

func PromptContract(version string) (Contract, error)
func query.ValidateLimits(limits query.Limits) error
```

- [ ] **Step 1: Export the existing query-limit policy with a failing compatibility test**

Add this test before renaming the private helper:

```go
func TestValidateLimitsMatchesNormalizeRequestPolicy(t *testing.T) {
    limits := Limits{MaxEntities: 0, MaxPredicates: 1, MaxChronology: 1}
    if err := ValidateLimits(limits); err == nil || err.Error() != "query limits must be positive" {
        t.Fatalf("ValidateLimits() error = %v", err)
    }
}
```

- [ ] **Step 2: Run the focused query contract test red**

Run:

```bash
go test ./internal/query -run TestValidateLimitsMatchesNormalizeRequestPolicy -count=1
```

Expected: FAIL because `ValidateLimits` is undefined.

- [ ] **Step 3: Export the helper without changing behavior**

Rename `validateLimits` to `ValidateLimits`, update `NormalizeRequest` to call it, and retain the exact positive-limit error:

```go
func ValidateLimits(limits Limits) error {
    if limits.MaxEntities <= 0 || limits.MaxPredicates <= 0 || limits.MaxChronology <= 0 {
        return fmt.Errorf("query limits must be positive")
    }
    return nil
}
```

- [ ] **Step 4: Write the exact system prompt**

Create `internal/queryplan/prompts/query-plan-v1.txt` with these complete contents:

```text
You are the Stacks temporal query planner. Convert exactly one private natural-language question into the closed temporal query proposal defined by the supplied JSON Schema.

Your authority is limited to proposing intent, entity-match policy, predicate filters, explicit valid-time selections, an independent recorded-time knowledge scope, and a chronology limit. Canonical entity IDs are supplied and attached locally by Stacks. Never invent, request, emit, remove, replace, or resolve entity IDs.

Use the supplied reference_time as the only anchor for relative temporal language. Emit explicit RFC3339 timestamps. Keep valid time separate from recorded time. Do not use unstated current time, timezone, identity, facts, evidence, citations, confidence, or causal assumptions.

Use status "executable" with reason "none" only when the question is unambiguous, supported by one of the four closed intents, and has enough temporal detail to produce a complete representable request. Otherwise use status "cannot-plan" with exactly one of "ambiguous-question", "unsupported-question", or "insufficient-temporal-detail", and emit the required empty sentinels for every request field.

For point-in-time use one point selection labeled "point". For trend-comparison use two ordered window selections labeled "before" and "after". For trajectory use one window labeled "between". Use causal-chain only for explicit causal wording, with the sole predicate "stacks.causal.v1/causes". Temporal precedence alone is not causality. Point and trend use chronology_limit 0; trajectory and causal-chain use a positive limit no greater than the supplied configured limit.

Return only the structured proposal. Do not narrate, explain, answer the question, cite sources, or add fields.
```

- [ ] **Step 5: Add contract lookup and isolated-byte tests**

Cover the exact constants, unsupported versions, nonblank prompt, exact schema name, valid JSON, and isolated schema copies:

```go
func TestPromptContractIsExactAndIsolated(t *testing.T) {
    first, err := PromptContract(PromptVersion)
    if err != nil {
        t.Fatal(err)
    }
    second, err := PromptContract(PromptVersion)
    if err != nil {
        t.Fatal(err)
    }
    if first.Version != "query-plan-v1" || first.SchemaName != "temporal_query_plan_v1" {
        t.Fatalf("contract identity = %#v", first)
    }
    first.JSONSchema[0] ^= 0xff
    if bytes.Equal(first.JSONSchema, second.JSONSchema) {
        t.Fatal("PromptContract() returned aliased schema bytes")
    }
    if _, err := PromptContract("query-plan-v2"); err == nil {
        t.Fatal("PromptContract(query-plan-v2) error = nil")
    }
}
```

- [ ] **Step 6: Run the new planner contract tests red**

Run:

```bash
go test ./internal/queryplan -run 'TestPromptContract|TestPlannerContractConstants' -count=1
```

Expected: FAIL because the package and contract do not exist.

- [ ] **Step 7: Implement the exact schema**

Embed `queryPlanSchema` in `schema.go`. Use this complete logical shape, preserving every `required` and `additionalProperties: false`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "status",
    "reason",
    "intent",
    "entity_match",
    "predicates",
    "selections",
    "knowledge_scope",
    "chronology_limit"
  ],
  "properties": {
    "status": {"type": "string", "enum": ["executable", "cannot-plan"]},
    "reason": {
      "type": "string",
      "enum": [
        "none",
        "ambiguous-question",
        "unsupported-question",
        "insufficient-temporal-detail"
      ]
    },
    "intent": {
      "type": "string",
      "enum": ["", "point-in-time", "trend-comparison", "trajectory", "causal-chain"]
    },
    "entity_match": {"type": "string", "enum": ["", "all", "any"]},
    "predicates": {
      "type": "array",
      "maxItems": 256,
      "items": {"type": "string", "minLength": 1}
    },
    "selections": {
      "type": "array",
      "maxItems": 2,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["kind", "label", "at", "start", "end"],
        "properties": {
          "kind": {"type": "string", "enum": ["point", "window"]},
          "label": {"type": "string", "enum": ["point", "before", "after", "between"]},
          "at": {"type": "string"},
          "start": {"type": "string"},
          "end": {"type": "string"}
        }
      }
    },
    "knowledge_scope": {
      "type": "object",
      "additionalProperties": false,
      "required": ["kind", "as_of"],
      "properties": {
        "kind": {"type": "string", "enum": ["", "current", "as-of"]},
        "as_of": {"type": "string"}
      }
    },
    "chronology_limit": {"type": "integer", "minimum": 0, "maximum": 10000}
  }
}
```

Use `//go:embed prompts/query-plan-v1.txt` for the prompt, keep schema bytes package-private, and return `append([]byte(nil), queryPlanSchema...)`.

- [ ] **Step 8: Lock prompt and schema digests**

In `schema_test.go`, compute SHA-256 over the exact embedded bytes and replace the expected literals once from the reviewed files:

```go
func TestPromptAndSchemaDigests(t *testing.T) {
    contract, err := PromptContract(PromptVersion)
    if err != nil {
        t.Fatal(err)
    }
    if got := fmt.Sprintf("%x", sha256.Sum256([]byte(contract.SystemPrompt))); got != expectedPromptSHA256 {
        t.Fatalf("prompt SHA-256 = %s", got)
    }
    if got := fmt.Sprintf("%x", sha256.Sum256(contract.JSONSchema)); got != expectedSchemaSHA256 {
        t.Fatalf("schema SHA-256 = %s", got)
    }
}
```

The implementer must put the two computed 64-character digests into named test constants in the same commit; an empty or wildcard expected digest is not acceptable.

- [ ] **Step 9: Implement the consumer-owned types and validation helpers**

Add the exact types in the interface block. Give `Usage` a private `valid()` method requiring nonnegative values and `TotalTokens >= InputTokens + OutputTokens` without overflow. Keep all slices defensively copied when crossing a public boundary.

- [ ] **Step 10: Add an executable dependency boundary**

Use `go list -json .` from `internal/queryplan/dependency_test.go` and reject direct production imports beginning with:

```text
stacks/internal/openai
stacks/internal/anthropic
stacks/internal/bedrock
github.com/openai/
github.com/anthropics/
github.com/aws/
github.com/spf13/cobra
github.com/spf13/viper
github.com/jackc/pgx/
github.com/JakeFAU/stacks/adapters/postgres
stacks/internal/cli
stacks/internal/config
stacks/internal/httpapi
```

The test package may use standard-library `os/exec`; production files may use OpenTelemetry interfaces and `internal/observability`.

- [ ] **Step 11: Run E1 contract tests green**

Run:

```bash
go test ./internal/query ./internal/queryplan -run 'TestValidateLimits|TestPromptContract|TestPlannerContract|TestPromptAndSchema' -count=1
go test ./internal/query ./internal/queryplan -count=1
```

Expected: PASS.

- [ ] **Step 12: Commit the exact contract**

```bash
git add internal/query/contract.go internal/query/contract_test.go \
  internal/queryplan/contract.go internal/queryplan/contract_test.go \
  internal/queryplan/dependency_test.go \
  internal/queryplan/schema.go internal/queryplan/schema_test.go \
  internal/queryplan/prompts/query-plan-v1.txt
git commit -m "Define temporal query planner contract"
```

### Task 2: Validate private input and compose only closed requests

**Files:**
- Create: `internal/queryplan/input.go`
- Create: `internal/queryplan/input_test.go`
- Create: `internal/queryplan/proposal.go`
- Create: `internal/queryplan/proposal_test.go`

**Interfaces:**
- Consumes: Task 1 `Input`, `PromptContract`, `query.ValidateLimits`, `query.NormalizeRequest`, `query.CausalPredicate`, `temporal.At`, `temporal.Between`, `temporal.CurrentKnowledge`, and `temporal.KnownAsOf`.
- Produces:

```go
type CannotPlanReason string

const (
    CannotPlanAmbiguous                  CannotPlanReason = "ambiguous-question"
    CannotPlanUnsupported                CannotPlanReason = "unsupported-question"
    CannotPlanInsufficientTemporalDetail CannotPlanReason = "insufficient-temporal-detail"
)

type CannotPlanError struct {
    Reason CannotPlanReason
}

func (err CannotPlanError) Error() string {
    switch err.Reason {
    case CannotPlanAmbiguous, CannotPlanUnsupported, CannotPlanInsufficientTemporalDetail:
        return "query planner cannot plan: " + string(err.Reason)
    default:
        return "query planner cannot plan"
    }
}
func NormalizeInput(input Input, limits query.Limits, maxQuestionBytes int) (Input, error)
func modelRequestFor(input Input, limits query.Limits) (ModelRequest, error)
func composeRequest(output json.RawMessage, entityIDs []identity.EntityID, limits query.Limits) (query.Request, error)
```

- [ ] **Step 1: Write pre-disclosure input table tests**

Use synthetic IDs `entity-atlas-001` and `entity-atlas-002`. Cover nil/blank/oversized/invalid UTF-8 questions, zero reference time, missing/blank/duplicate/over-limit IDs, invalid query limits, and an ID appearing verbatim in the question. Assert every error excludes all supplied markers.

```go
func TestNormalizeInputRejectsIDDisclosure(t *testing.T) {
    input := Input{
        Question:      "What changed for entity-atlas-001?",
        EntityIDs:     []identity.EntityID{"entity-atlas-001"},
        ReferenceTime: time.Date(2026, 7, 29, 12, 0, 0, 0, time.FixedZone("synthetic", -4*60*60)),
    }
    _, err := NormalizeInput(input, query.Limits{MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20}, 1024)
    if err == nil || strings.Contains(err.Error(), "entity-atlas-001") {
        t.Fatalf("NormalizeInput() error = %v", err)
    }
}
```

- [ ] **Step 2: Run the input tests red**

Run:

```bash
go test ./internal/queryplan -run TestNormalizeInput -count=1
```

Expected: FAIL because `NormalizeInput` is undefined.

- [ ] **Step 3: Implement defensive input normalization**

`NormalizeInput` must:

```go
func NormalizeInput(input Input, limits query.Limits, maxQuestionBytes int) (Input, error) {
    if err := query.ValidateLimits(limits); err != nil {
        return Input{}, errors.New("query planner limits are invalid")
    }
    if maxQuestionBytes < minimumQuestionByteLimit ||
        maxQuestionBytes > maximumQuestionByteLimit ||
        len(input.Question) > maxQuestionBytes ||
        !utf8.ValidString(input.Question) || strings.TrimSpace(input.Question) == "" {
        return Input{}, errors.New("query planner question is invalid")
    }
    if input.ReferenceTime.IsZero() {
        return Input{}, errors.New("query planner reference time is required")
    }
    entityIDs, err := normalizeEntityIDs(input.EntityIDs, limits.MaxEntities)
    if err != nil {
        return Input{}, err
    }
    for _, entityID := range entityIDs {
        if strings.Contains(input.Question, string(entityID)) {
            return Input{}, errors.New("query planner question contains a canonical entity ID")
        }
    }
    return Input{
        Question: input.Question,
        EntityIDs: entityIDs,
        ReferenceTime: timepoint.Normalize(input.ReferenceTime),
    }, nil
}
```

`normalizeEntityIDs` trims, rejects blanks/duplicates/over-limit values, sorts a defensive copy for canonical attachment, and never formats an ID into an error.

- [ ] **Step 4: Write deterministic private-model-input tests**

Assert two calls are byte-equal, reference time is UTC microseconds, the payload includes `entity_count`, configured limits, and `entity_ids_attached_locally: true`, and no supplied ID appears:

```go
func TestModelRequestForOmitsCanonicalIDs(t *testing.T) {
    request, err := modelRequestFor(normalizedSyntheticInput(t), query.Limits{
        MaxEntities: 4, MaxPredicates: 8, MaxChronology: 20,
    })
    if err != nil {
        t.Fatal(err)
    }
    if strings.Contains(request.Input, "entity-atlas-001") {
        t.Fatal("model input contains a canonical entity ID")
    }
    const want = `{"question":"What changed last quarter?","reference_time":"2026-07-29T16:00:00Z","entity_count":1,"entity_ids_attached_locally":true,"limits":{"max_entities":4,"max_predicates":8,"max_chronology":20}}`
    if request.Input != want {
        t.Fatalf("ModelRequest.Input = %s", request.Input)
    }
}
```

- [ ] **Step 5: Implement deterministic serialization**

Use a fixed struct, not a map:

```go
type privateModelInput struct {
    Question                 string             `json:"question"`
    ReferenceTime            string             `json:"reference_time"`
    EntityCount              int                `json:"entity_count"`
    EntityIDsAttachedLocally bool               `json:"entity_ids_attached_locally"`
    Limits                   privateModelLimits `json:"limits"`
}
```

Define `minimumQuestionByteLimit = 1` and `maximumQuestionByteLimit = 64 * 1024`. `modelRequestFor` loads `PromptContract(PromptVersion)`, serializes with `json.Marshal`, and copies the schema bytes into `ModelRequest`.

- [ ] **Step 6: Write strict proposal-decoder tests**

Use table cases for malformed JSON, a second JSON value, unknown top-level field `entity_ids`, unknown nested fields, invalid enum text, missing required fields, every cannot-plan reason, invalid status/reason pair, wrong labels/kinds/counts, non-RFC3339 timestamps, inverted windows, invalid knowledge scope, duplicate/excessive predicates, and invalid chronology limits.

The executable point fixture is:

```json
{
  "status": "executable",
  "reason": "none",
  "intent": "point-in-time",
  "entity_match": "all",
  "predicates": ["assigned_to"],
  "selections": [
    {"kind": "point", "label": "point", "at": "2026-06-01T12:00:00-04:00", "start": "", "end": ""}
  ],
  "knowledge_scope": {"kind": "current", "as_of": ""},
  "chronology_limit": 0
}
```

The cannot-plan fixture is:

```json
{
  "status": "cannot-plan",
  "reason": "ambiguous-question",
  "intent": "",
  "entity_match": "",
  "predicates": [],
  "selections": [],
  "knowledge_scope": {"kind": "", "as_of": ""},
  "chronology_limit": 0
}
```

- [ ] **Step 7: Run proposal tests red**

Run:

```bash
go test ./internal/queryplan -run 'TestComposeRequest|TestCannotPlan' -count=1
```

Expected: FAIL because proposal decoding and conversion do not exist.

- [ ] **Step 8: Implement the exact wire DTO and one-value strict decoder**

```go
type proposalWire struct {
    Status          *string          `json:"status"`
    Reason          *string          `json:"reason"`
    Intent          *string          `json:"intent"`
    EntityMatch     *string          `json:"entity_match"`
    Predicates      *[]string        `json:"predicates"`
    Selections      *[]selectionWire `json:"selections"`
    KnowledgeScope  *knowledgeWire   `json:"knowledge_scope"`
    ChronologyLimit *int             `json:"chronology_limit"`
}

type selectionWire struct {
    Kind  *string `json:"kind"`
    Label *string `json:"label"`
    At    *string `json:"at"`
    Start *string `json:"start"`
    End   *string `json:"end"`
}

type knowledgeWire struct {
    Kind *string `json:"kind"`
    AsOf *string `json:"as_of"`
}

type proposal struct {
    Status          string
    Reason          string
    Intent          string
    EntityMatch     string
    Predicates      []string
    Selections      []selection
    KnowledgeScope  knowledge
    ChronologyLimit int
}

type selection struct {
    Kind  string
    Label string
    At    string
    Start string
    End   string
}

type knowledge struct {
    Kind string
    AsOf string
}
```

Decode `proposalWire` from `bytes.NewReader(output)` with `DisallowUnknownFields`, require one successful decode, then require the next decode to return `io.EOF`. Reject every nil top-level or nested pointer before copying values into `proposal`; this makes missing fields and explicit JSON `null` invalid while preserving an explicit empty array. Map every structural failure to `errors.New("query planner proposal is invalid")`; do not include decoder text.

- [ ] **Step 9: Implement closed status and intent conversion**

For `cannot-plan`, require all request fields to equal the approved empty sentinels and return `CannotPlanError{Reason: CannotPlanReason(value.Reason)}`. For executable output:

- `point-in-time`: one `point`/`point` selection, nonempty `at`, empty `start/end`, limit zero.
- `trend-comparison`: two `window` selections labeled `before`, `after`, empty `at`, limit zero.
- `trajectory`: one `window` selection labeled `between`, positive limit.
- `causal-chain`: one `window` selection labeled `between`, positive limit, and predicates exactly `[]string{"stacks.causal.v1/causes"}`.
- `current`: empty `as_of`; `as-of`: nonempty RFC3339 `as_of`.

Parse with `time.Parse(time.RFC3339, value)`, construct temporal values through core constructors, convert every predicate through `observation.NewPredicate`, attach a defensive copy of all normalized IDs, and finish with:

```go
    normalized, err := query.NormalizeRequest(query.Request{
        Intent:         intent,
        EntityIDs:      append([]identity.EntityID(nil), entityIDs...),
        EntityMatch:    query.EntityMatch(value.EntityMatch),
        Predicates:     predicates,
        Selections:     selections,
        KnowledgeScope: knowledgeScope,
        Limit:          value.ChronologyLimit,
    }, limits)
    if err != nil {
        return query.Request{}, errors.New("query planner proposal is invalid")
    }
    return normalized, nil
```

All failures except `CannotPlanError` use bounded operation-only errors.

- [ ] **Step 10: Prove all caller IDs are attached and model fields cannot supply IDs**

Add a two-ID trend test that asserts canonical sorted IDs, normalized UTC microsecond times, independent `KnownAsOf`, and exact predicates. Add a JSON case containing `entity_ids` and assert strict rejection before request construction.

- [ ] **Step 11: Run Task 2 tests green**

Run:

```bash
go test ./internal/queryplan -run 'TestNormalizeInput|TestModelRequestFor|TestComposeRequest|TestCannotPlan' -count=1
go test ./internal/queryplan ./internal/query -count=1
```

Expected: PASS.

- [ ] **Step 12: Commit deterministic composition**

```bash
git add internal/queryplan/input.go internal/queryplan/input_test.go \
  internal/queryplan/proposal.go internal/queryplan/proposal_test.go
git commit -m "Compose validated temporal query plans"
```

### Task 3: Orchestrate planning, execution, audit, and safe telemetry

**Files:**
- Create: `internal/queryplan/service.go`
- Create: `internal/queryplan/service_test.go`
- Modify: `internal/modeltelemetry/recorder.go`
- Modify: `internal/modeltelemetry/recorder_test.go`

**Interfaces:**
- Consumes: Tasks 1-2 `Model`, `Executor`, `NormalizeInput`, `modelRequestFor`, `composeRequest`, `Execution`, and `PlannerMetadata`.
- Produces:

```go
type QuestionRecorder interface {
    RecordQuestionBytes(context.Context, int64)
}

type Service struct {
    Model            Model
    Executor         Executor
    Limits           query.Limits
    PlannerTimeout   time.Duration
    MaxQuestionBytes int
    QuestionRecorder QuestionRecorder
    Tracer           trace.Tracer
}

func (service Service) Ask(context.Context, Input) (Execution, error)
func (recorder *modeltelemetry.MetricsRecorder) RecordQuestionBytes(context.Context, int64)
```

- [ ] **Step 1: Write service sequencing tests**

Use fakes that append `model`, `executor`, and `record-question-bytes` to a call list. Assert:

- invalid input calls neither model nor executor;
- model error calls no executor;
- cannot-plan/invalid proposal calls no executor;
- valid proposal calls model then executor exactly once;
- the executor receives the normalized request;
- result/request mismatch fails with no `Execution`;
- question bytes are recorded once after local validation and before model invocation.

```go
type modelFunc func(context.Context, ModelRequest) (ModelResponse, error)
func (fn modelFunc) Plan(ctx context.Context, request ModelRequest) (ModelResponse, error) {
    return fn(ctx, request)
}

type executorFunc func(context.Context, query.Request) (query.Result, error)
func (fn executorFunc) Query(ctx context.Context, request query.Request) (query.Result, error) {
    return fn(ctx, request)
}
```

- [ ] **Step 2: Run orchestration tests red**

Run:

```bash
go test ./internal/queryplan -run TestServiceAsk -count=1
```

Expected: FAIL because `Service.Ask` does not exist.

- [ ] **Step 3: Implement validation and the planning-only deadline**

Define `minimumPlannerTimeout = time.Second` and `maximumPlannerTimeout = 5 * time.Minute`. `Ask` must reject nil context/model/executor, invalid limits, timeout outside `1s..5m`, and a maximum question byte setting outside `minimumQuestionByteLimit..maximumQuestionByteLimit`. Normalize input first. Start one `stacks.query.plan` span without content-bearing attributes, record question bytes, build the exact request, and invoke only `Model.Plan` under:

```go
planningContext, cancel := context.WithTimeout(ctx, service.PlannerTimeout)
response, err := service.Model.Plan(planningContext, modelRequest)
cancel()
```

Use the original caller `ctx` for deterministic execution so the planner timeout does not become a query/database timeout.

- [ ] **Step 4: Implement bounded model-response validation**

Require:

```go
response.Provider.Valid()
strings.TrimSpace(response.ModelID) != ""
response.PromptVersion == PromptVersion
response.SchemaName == SchemaName
response.Attempts > 0
response.Usage.valid()
response.WallLatency >= 0
response.ProviderLatency >= 0
json.Valid(response.Output)
```

Reject failures with `errors.New("query planner response is invalid")`. Never include response values.

- [ ] **Step 5: Implement caller-cancellation precedence**

Use one private helper:

```go
func canonicalContextError(ctx context.Context, err error) error {
    if errors.Is(ctx.Err(), context.Canceled) {
        return context.Canceled
    }
    if errors.Is(ctx.Err(), context.DeadlineExceeded) {
        return context.DeadlineExceeded
    }
    if errors.Is(err, context.Canceled) {
        return context.Canceled
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return context.DeadlineExceeded
    }
    return nil
}
```

Test caller-canceled/provider-deadline and caller-deadline/provider-canceled conflicts for both model and executor failures.

For non-context failures, wrap the adapter's already-bounded model error as `fmt.Errorf("plan temporal query: %w", err)` and the deterministic executor error as `fmt.Errorf("execute planned temporal query: %w", err)`. This preserves `errors.Is` for reviewed sentinels such as `query.ErrLimitExceeded` without retaining raw provider details.

- [ ] **Step 6: Validate result/request parity**

Normalize the returned result with `query.NormalizeResult`, then compare its intent, entity IDs, entity-match, predicates, selections, knowledge scope, and limit to the exact normalized request. Return only:

```go
Execution{
    SchemaVersion: OutputSchemaVersion,
    ReferenceTime: normalizedInput.ReferenceTime,
    Request:       normalizedRequest,
    Planner: PlannerMetadata{
        Provider: response.Provider, ModelID: response.ModelID,
        PromptVersion: response.PromptVersion, SchemaName: response.SchemaName,
        Usage: response.Usage, Attempts: response.Attempts,
        WallLatency: response.WallLatency, ProviderLatency: response.ProviderLatency,
    },
    Result: normalizedResult,
}
```

Defensively copy request/result slices. A mismatch returns `errors.New("query planner result does not match the normalized request")`.

- [ ] **Step 7: Add the question-byte histogram test**

Extend `MetricsRecorder` with:

```go
questionBytes metric.Int64Histogram
```

Create it as:

```go
meter.Int64Histogram(
    "stacks.query.planner.question.bytes",
    metric.WithDescription("Private temporal question byte count"),
    metric.WithUnit("By"),
)
```

`RecordQuestionBytes` ignores negative values and calls `questionBytes.Record(ctx, value)` with no `metric.WithAttributes`. Use the manual metric reader to assert the value is recorded and the datapoint has zero attributes.

- [ ] **Step 8: Add privacy-span tests**

Capture spans with synthetic markers in the question, model input, raw proposal, IDs, predicates, timestamps, citations, provider body, database URL, and credential. Assert none appears in span name, status description, attributes, events, or returned errors. Assert the successful `stacks.query.plan` span has `codes.Ok`.

- [ ] **Step 9: Run Task 3 tests green**

Run:

```bash
go test ./internal/queryplan ./internal/modeltelemetry -count=1
go test -race ./internal/queryplan ./internal/modeltelemetry -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit orchestration and telemetry**

```bash
git add internal/queryplan/service.go internal/queryplan/service_test.go \
  internal/modeltelemetry/recorder.go internal/modeltelemetry/recorder_test.go
git commit -m "Execute audited temporal query plans"
```

## E1 Phase Review Gate

- [ ] Dispatch a fresh phase reviewer. Require confirmation that `internal/queryplan` imports no provider/transport/database packages, IDs cannot enter the private model request, all timestamps are explicit and canonical, strict decoding has no permissive path, `query.NormalizeRequest` remains final request authority, and invalid plans cannot call the executor.

- [ ] Address findings test-first, rerun:

```bash
go test ./internal/query ./internal/queryplan ./internal/modeltelemetry -count=1
go test -race ./internal/queryplan ./internal/modeltelemetry -count=1
git diff --check
```

- [ ] Save the reviewed E1 diff and report under `.superpowers/sdd/plan-e/phase-e1-report.md`. Stop if any prompt/schema, privacy, authority, or scope finding remains.

---

# Phase E2 — Provider Adapter Support

## E2 Invariants

1. Every concrete provider client implements both `extract.Model` and `queryplan.Model`; neither consumer contract becomes generic or imports provider types.
2. A provider method verifies the exact consumer-owned prompt/schema pair before network access.
3. Existing `Generate(extract.Request)` behavior and tests remain unchanged.
4. One adapter-owned retry loop controls the exact configured attempt count. Retry attempts reuse identical request parameters, provider, model, data mode, prompt, schema, token limit, and private input.
5. Refusal, incomplete/truncated output, invalid JSON, model mismatch, auth/authz, cancellation, and deadline are terminal.
6. Returned planner metadata is bounded and complete; raw provider request/response bodies and errors never escape.

For each provider, use an adapter-private request/response core rather than duplicating retry and response-envelope logic:

```go
type structuredRequest struct {
    promptVersion string
    systemPrompt  string
    input         string
    schemaName    string
    jsonSchema    []byte
    validate      func() error
}

type structuredUsage struct {
    inputTokens  int64
    outputTokens int64
    totalTokens  int64
}

type structuredResponse struct {
    output          json.RawMessage
    usage           structuredUsage
    modelID         string
    promptVersion   string
    schemaName      string
    attempts        int
    wallLatency     time.Duration
    providerLatency time.Duration
}
```

`Generate` maps its exact `extract.Request` through this private core and back to `extract.Response`. `Plan` maps `queryplan.ModelRequest` through the same private core and back to `queryplan.ModelResponse`. The validator closure must call the appropriate consumer's exact `PromptContract`; it must not accept an arbitrary prompt or schema.

### Task 4: Add the OpenAI planner adapter

**Files:**
- Modify: `internal/openai/client.go`
- Modify: `internal/openai/client_test.go`

**Interfaces:**
- Consumes: E1 `queryplan.ModelRequest`, `queryplan.ModelResponse`, `queryplan.PromptContract`, and existing OpenAI `Options`.
- Produces:

```go
func (client *Client) Plan(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error)

var _ extract.Model = (*Client)(nil)
var _ queryplan.Model = (*Client)(nil)
```

- [ ] **Step 1: Add exact planner request-shape tests**

Build a valid request from `queryplan.PromptContract(queryplan.PromptVersion)` and assert the fake Responses API receives:

```go
Background:      openaisdk.Bool(false)
Instructions:    openaisdk.String(request.SystemPrompt)
Input:           responses.ResponseNewParamsInputUnion{OfString: openaisdk.String(request.Input)}
MaxOutputTokens: openaisdk.Int(client.maxOutputTokens)
Model:           responses.ResponsesModel(client.modelID)
Reasoning.Effort: shared.ReasoningEffortNone
Store:           openaisdk.Bool(false)
Text.Format.OfJSONSchema.Name: queryplan.SchemaName
Text.Format.OfJSONSchema.Strict: true
```

Also assert no tools, previous-response ID, conversation, background job, file, or hosted capability is set.

- [ ] **Step 2: Add exact-contract rejection tests**

Mutate prompt version, prompt body, schema name, schema bytes, and empty input one at a time. Assert `errors.Is(err, ErrInvalidRequest)`, zero API calls, and no private marker in the error.

- [ ] **Step 3: Run OpenAI planner tests red**

Run:

```bash
go test ./internal/openai -run 'TestClientPlan|TestPlanner' -count=1
```

Expected: FAIL because `Client.Plan` is undefined.

- [ ] **Step 4: Refactor the existing method through an adapter-private core**

Move the existing retry loop into:

```go
func (client *Client) generateStructured(
    ctx context.Context,
    request structuredRequest,
) (response structuredResponse, resultErr error)
```

Keep `responsesAPI.New`, `option.WithMaxRetries(0)`, retry delays, retryable classification, response-envelope checks, telemetry outcomes, and bounded errors unchanged. Set `attempts` to the successful attempt and `wallLatency` to the nonnegative elapsed duration. Map existing extraction usage/latency exactly back into `extract.Response`.

- [ ] **Step 5: Implement the planner-specific validator and mapping**

```go
func plannerStructuredRequest(request queryplan.ModelRequest) structuredRequest {
    return structuredRequest{
        promptVersion: request.PromptVersion,
        systemPrompt: request.SystemPrompt,
        input: request.Input,
        schemaName: request.SchemaName,
        jsonSchema: append([]byte(nil), request.JSONSchema...),
        validate: func() error {
            contract, err := queryplan.PromptContract(request.PromptVersion)
            if err != nil ||
                request.SystemPrompt != contract.SystemPrompt ||
                request.SchemaName != contract.SchemaName ||
                !bytes.Equal(request.JSONSchema, contract.JSONSchema) ||
                request.Input == "" {
                return ErrInvalidRequest
            }
            return nil
        },
    }
}
```

Return:

```go
queryplan.ModelResponse{
    Output: append(json.RawMessage(nil), response.output...),
    Provider: modelpolicy.ProviderOpenAI,
    ModelID: response.modelID,
    PromptVersion: response.promptVersion,
    SchemaName: response.schemaName,
    Usage: queryplan.Usage{
        InputTokens: response.usage.inputTokens,
        OutputTokens: response.usage.outputTokens,
        TotalTokens: response.usage.totalTokens,
    },
    Attempts: response.attempts,
    WallLatency: response.wallLatency,
    ProviderLatency: response.providerLatency,
}
```

OpenAI does not report one trusted provider-side latency for this response contract, so `providerLatency` is exactly zero; `wallLatency` covers all attempts and backoff.

- [ ] **Step 6: Lock same-provider retry and terminal behavior**

Test these exact sequences with the fake API:

```text
429 -> completed JSON                     calls=2, Attempts=2
500 -> completed JSON                     calls=2, Attempts=2
retryable net.OpError timeout -> success  calls=2, Attempts=2
401                                      calls=1
403                                      calls=1
refusal content                           calls=1
incomplete status                         calls=1
invalid JSON                              calls=1
returned model mismatch                   calls=1
caller cancellation                       no further call/backoff
caller deadline                           no further call/backoff
```

Assert repeated `ResponseNewParams` are deeply equal and all error strings exclude the private question, schema body, raw provider body, API key, and provider request ID markers.

- [ ] **Step 7: Run all OpenAI tests, including extraction regression**

Run:

```bash
go test ./internal/openai -count=1
go test -race ./internal/openai -count=1
```

Expected: PASS; existing extraction tests remain green.

- [ ] **Step 8: Commit the OpenAI adapter**

```bash
git add internal/openai/client.go internal/openai/client_test.go
git commit -m "Plan temporal queries with OpenAI"
```

### Task 5: Add the Anthropic planner adapter

**Files:**
- Modify: `internal/anthropic/client.go`
- Modify: `internal/anthropic/client_test.go`

**Interfaces:**
- Consumes: E1 `queryplan.ModelRequest`, `queryplan.ModelResponse`, `queryplan.PromptContract`, and existing Anthropic `Options`.
- Produces:

```go
func (client *Client) Plan(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error)

var _ extract.Model = (*Client)(nil)
var _ queryplan.Model = (*Client)(nil)
```

- [ ] **Step 1: Add exact native structured-output request tests**

Assert the fake Messages API receives exactly one user text message containing `request.Input`, one system text block containing the exact prompt, the configured model/token limit, and:

```go
OutputConfig: anthropicsdk.OutputConfigParam{
    Format: anthropicsdk.JSONOutputFormatParam{Schema: decodedSchema},
}
```

Assert no tools, files, batches, prompt-caching option, or managed-agent feature is present.

- [ ] **Step 2: Add prompt/schema mutation tests**

Mutate version, prompt, schema name, schema bytes, and input; require `ErrInvalidRequest`, zero API calls, and redacted errors.

- [ ] **Step 3: Run Anthropic planner tests red**

Run:

```bash
go test ./internal/anthropic -run 'TestClientPlan|TestPlanner' -count=1
```

Expected: FAIL because `Client.Plan` is undefined.

- [ ] **Step 4: Refactor through the same adapter-private shape**

Create Anthropic-local `structuredRequest`, `structuredUsage`, `structuredResponse`, and:

```go
func (client *Client) generateStructured(
    ctx context.Context,
    request structuredRequest,
) (response structuredResponse, resultErr error)
```

Preserve `option.WithMaxRetries(0)`, existing retry classification/backoff, cache-token-safe arithmetic, end-turn requirement, exact one text block, model match, JSON validity, telemetry, and bounded errors. `Generate` must still return the exact existing extraction response.

- [ ] **Step 5: Implement `Plan` and bounded metadata mapping**

Use `queryplan.PromptContract` for planner validation, return provider `modelpolicy.ProviderAnthropic`, and map attempts, wall latency, provider latency, and usage exactly as in Task 4's `queryplan.ModelResponse`.

Anthropic does not report one trusted provider-side latency in the selected Messages response, so return zero provider latency and the measured aggregate wall latency.

- [ ] **Step 6: Lock retry/refusal/incomplete behavior**

Test:

```text
429 -> end_turn JSON                       calls=2, Attempts=2
503 -> end_turn JSON                       calls=2, Attempts=2
retryable transport timeout -> success     calls=2, Attempts=2
401 or 403                                 calls=1
refusal/non-end_turn stop reason            calls=1
multiple content blocks                     calls=1
missing usage                               calls=1
invalid JSON                                calls=1
returned model mismatch                     calls=1
caller cancellation/deadline                no retry
```

Assert retry request structs are deeply equal and no private/provider marker enters errors or telemetry.

- [ ] **Step 7: Run all Anthropic tests**

Run:

```bash
go test ./internal/anthropic -count=1
go test -race ./internal/anthropic -count=1
```

Expected: PASS, including unchanged extraction cases.

- [ ] **Step 8: Commit the Anthropic adapter**

```bash
git add internal/anthropic/client.go internal/anthropic/client_test.go
git commit -m "Plan temporal queries with Anthropic"
```

### Task 6: Add the Bedrock planner adapter

**Files:**
- Modify: `internal/bedrock/client.go`
- Modify: `internal/bedrock/client_test.go`

**Interfaces:**
- Consumes: E1 `queryplan.ModelRequest`, `queryplan.ModelResponse`, `queryplan.PromptContract`, existing Bedrock `Options`, and the existing exact adaptive retryer.
- Produces:

```go
func (client *Client) Plan(context.Context, queryplan.ModelRequest) (queryplan.ModelResponse, error)

var _ extract.Model = (*Client)(nil)
var _ queryplan.Model = (*Client)(nil)
```

- [ ] **Step 1: Add exact Converse input tests**

Assert:

```go
&bedrockruntime.ConverseInput{
    ModelId: aws.String(client.modelID),
    InferenceConfig: &types.InferenceConfiguration{
        MaxTokens: aws.Int32(client.maxTokens),
    },
    System: []types.SystemContentBlock{
        &types.SystemContentBlockMemberText{Value: request.SystemPrompt},
    },
    Messages: []types.Message{{
        Role: types.ConversationRoleUser,
        Content: []types.ContentBlock{
            &types.ContentBlockMemberText{Value: request.Input},
        },
    }},
    OutputConfig: &types.OutputConfig{TextFormat: &types.OutputFormat{
        Type: types.OutputFormatTypeJsonSchema,
        Structure: &types.OutputFormatStructureMemberJsonSchema{Value: types.JsonSchemaDefinition{
            Name: aws.String(queryplan.SchemaName),
            Schema: aws.String(string(request.JSONSchema)),
        }},
    }},
    RequestMetadata: nil,
}
```

Assert no tools, guardrail side-channel, provider-managed agent, or request metadata is added.

- [ ] **Step 2: Add exact-contract mutation tests and run red**

Test mutated prompt version/body/schema name/schema bytes and empty input. Run:

```bash
go test ./internal/bedrock -run 'TestClientPlan|TestPlanner' -count=1
```

Expected: FAIL because `Client.Plan` is undefined.

- [ ] **Step 3: Refactor the retry core without changing policy**

Create Bedrock-local private structured types and move the existing adaptive retry lifecycle into:

```go
func (client *Client) generateStructured(
    ctx context.Context,
    request structuredRequest,
) (response structuredResponse, resultErr error)
```

Keep `aws.NopRetryer{}` on the SDK client, the exact configured adaptive retryer owned by `Client`, token return semantics, delay cancellation, outcome classification, required metrics/usage, end-turn, one assistant text block, model configuration, and bounded errors unchanged.

- [ ] **Step 4: Implement `Plan` mapping**

Validate through `queryplan.PromptContract`, return provider `modelpolicy.ProviderBedrock`, and map provider-reported latency plus wall latency, attempts, and usage. Do not move restricted-disclosure inspection into the client; root composition remains its owner.

Converse does not return a separate response model identifier. The adapter returns the configured model ID that was placed in `ConverseInput`; OpenAI and Anthropic continue to require an exact returned-model match.

- [ ] **Step 5: Lock exact retry and terminal sequences**

Test:

```text
ThrottlingException -> success               Attempts=2
ServiceUnavailableException -> success       Attempts=2
approved retryable transport -> success      Attempts=2
UnrecognizedClient/authentication             Attempts=1
AccessDeniedException                         Attempts=1
invalid output/stop reason/missing usage       Attempts=1
caller cancellation/deadline                  no further attempt
retry-token or retry-delay policy error        bounded terminal error
```

Assert both `ConverseInput` values are deeply equal across retries and all synthetic private/provider markers are absent from errors and telemetry.

- [ ] **Step 6: Run all Bedrock tests**

Run:

```bash
go test ./internal/bedrock -count=1
go test -race ./internal/bedrock -count=1
```

Expected: PASS, including unchanged extraction and retry-policy tests.

- [ ] **Step 7: Commit the Bedrock adapter**

```bash
git add internal/bedrock/client.go internal/bedrock/client_test.go
git commit -m "Plan temporal queries with Bedrock"
```

## E2 Phase Review Gate

- [ ] Dispatch one fresh cross-provider reviewer. Require a comparison table for request shape, prompt/schema verification, retry eligibility, attempt accounting, cancellation, terminal refusal/incomplete behavior, metadata, data-mode policy, telemetry redaction, and extraction regression.

- [ ] Run:

```bash
go test ./internal/openai ./internal/anthropic ./internal/bedrock ./internal/extract -count=1
go test -race ./internal/openai ./internal/anthropic ./internal/bedrock -count=1
git diff --check
```

- [ ] Save `.superpowers/sdd/plan-e/phase-e2-report.md`. Confirm from fake transports that no live provider was invoked and that no adapter accepts arbitrary prompt/schema bytes.

---

# Phase E3 — Configuration, CLI, Application, and Lazy Composition

## E3 Invariants

1. `query ask` is the only command that validates planner/model settings or constructs a planner provider.
2. The CLI accepts no positional question or `--question` flag. It reads at most configured maximum plus one byte from injected stdin.
3. Input normalization and restricted-disclosure preflight complete before provider construction; provider construction completes before invocation; PostgreSQL opens only after a normalized proposal exists.
4. The transport-neutral `queryplan.Service` remains usable by a future web handler without Cobra, streams, or root composition types.
5. Existing typed query command output remains byte-for-byte unchanged.
6. Ask text and JSON reuse existing result conversion/rendering and add only an outer private audit envelope.

### Task 7: Add planner-specific configuration and validation

**Files:**
- Modify: `internal/config/application.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/document.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/loader_test.go`
- Modify: `internal/config/document_test.go`
- Modify: `internal/config/model_settings_test.go`
- Modify: `internal/config/database_test.go`
- Modify: `.env.example`
- Modify: `config.example.yaml`
- Modify: `config.example.json`

**Interfaces:**
- Consumes: existing model settings and `CommandQuery`.
- Produces:

```go
const CommandQueryAsk Command = "query-ask"

const (
    QueryPlannerTimeoutEnvironmentVariable = "STACKS_QUERY_PLANNER_TIMEOUT"
    QueryPlannerMaxQuestionBytesEnvironmentVariable = "STACKS_QUERY_PLANNER_MAX_QUESTION_BYTES"
)

type QueryPlannerSettings struct {
    Timeout          time.Duration
    MaxQuestionBytes int
}

type Settings struct {
    // existing fields
    QueryPlanner QueryPlannerSettings
}
```

- [ ] **Step 1: Write default, range, and precedence tests**

Assert defaults `time.Minute` and `16384`; accepted endpoints `1s`, `5m`, `1`, `65536`; rejected values `0s`, `999ms`, `5m1s`, `0`, and `65537`; and environment values override YAML/JSON file values.

- [ ] **Step 2: Write command-isolation tests**

Use empty model settings and assert:

```go
settings.Validate(CommandQuery) == nil
settings.Validate(CommandQueryAsk) contains STACKS_MODEL_PROVIDER
```

With valid explicit model settings, database URL, query limits, and planner settings, `CommandQueryAsk` passes. Directory enabled with only core scope must still pass for both `CommandQuery` and `CommandQueryAsk`.

Also cover:

- personal OpenAI and Anthropic accepted only with their explicit credentials;
- restricted OpenAI/Anthropic rejected by existing invocation policy;
- Bedrock requires exact region/profile policy and accepts restricted mode for later preflight;
- legacy data mode and every unsupported legacy provider environment variable fail before bootstrap;
- typed `CommandQuery` ignores otherwise absent planner/model settings.

- [ ] **Step 3: Run configuration tests red**

Run:

```bash
go test ./internal/config -run 'Test.*QueryPlanner|Test.*CommandQueryAsk' -count=1
```

Expected: FAIL because planner settings and target do not exist.

- [ ] **Step 4: Add named defaults and bounds**

In `application.go`:

```go
const (
    defaultQueryPlannerTimeout          = time.Minute
    minimumQueryPlannerTimeout          = time.Second
    maximumQueryPlannerTimeout          = 5 * time.Minute
    defaultQueryPlannerMaxQuestionBytes = 16 * 1024
    minimumQueryPlannerQuestionBytes    = 1
    maximumQueryPlannerQuestionBytes    = 64 * 1024
)
```

Add environment constants and `CommandQueryAsk`.

- [ ] **Step 5: Implement command-specific validation**

- `DatabaseSettings.validate`: treat `CommandQueryAsk` like `CommandQuery` for URL requirement and directory-scope exemption.
- `QuerySettings.validate`: validate limits for `CommandQuery` and `CommandQueryAsk`.
- `QueryPlannerSettings.validate`: run only for `CommandQueryAsk` and enforce exact ranges.
- `ApplicationSettings.Validate`: for `CommandQueryAsk`, call only `validateModelSettings`; do not require Drive, Directory, corpus titles, ingestion leases, or extraction prompt settings.

`Settings.Validate` calls database, query, planner, then application validation.

- [ ] **Step 6: Bind and parse exact keys**

Use:

```go
configKeyQueryPlannerTimeout          = "query_planner.timeout"
configKeyQueryPlannerMaxQuestionBytes = "query_planner.max_question_bytes"
```

Add defaults, explicit `BindEnv` entries, `durationValue`, `positiveIntegerValue`, and typed assignment in `settingsFrom`.

- [ ] **Step 7: Extend strict YAML/JSON schema tests**

Add:

```go
"query_planner": configObjectSchema(map[string]configSchemaNode{
    "timeout":            configLeafSchema(configSchemaDurationString),
    "max_question_bytes": configLeafSchema(configSchemaInteger),
}),
```

Test valid YAML/JSON and rejection of `query_planner.question_limit`, nested unknown fields, non-duration timeout, and noninteger byte count.

- [ ] **Step 8: Update non-secret examples**

Add to `.env.example`:

```dotenv
STACKS_QUERY_PLANNER_TIMEOUT=1m
STACKS_QUERY_PLANNER_MAX_QUESTION_BYTES=16384
```

Add to YAML:

```yaml
query_planner:
  timeout: 1m
  max_question_bytes: 16384
```

Add the equivalent object to JSON. Do not add credentials or a default provider/model.

- [ ] **Step 9: Run all configuration tests**

Run:

```bash
go test ./internal/config -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 10: Commit configuration**

```bash
git add internal/config .env.example config.example.yaml config.example.json
git commit -m "Configure temporal query planning"
```

### Task 8: Add `query ask`, bounded stdin, and audit rendering

**Files:**
- Modify: `internal/cli/runner.go`
- Modify: `internal/cli/runner_test.go`
- Modify: `internal/cli/config.go`
- Modify: `internal/cli/config_test.go`
- Create: `internal/cli/query_ask.go`
- Create: `internal/cli/query_ask_test.go`
- Create: `internal/cli/query_ask_json.go`
- Create: `internal/cli/query_ask_json_test.go`
- Create: `internal/cli/query_ask_text.go`
- Create: `internal/cli/query_ask_text_test.go`
- Modify: `internal/cli/query_json.go`
- Modify: `internal/cli/query_json_test.go`
- Modify: `internal/cli/query_text.go`
- Modify: `internal/cli/query_text_test.go`

**Interfaces:**
- Consumes: E1 `queryplan.Input`, `queryplan.Execution`, `queryplan.NormalizeInput`, existing `QueryOutput`, and existing query renderers.
- Produces:

```go
const ActionAsk Action = "ask"

type QueryAskInput struct {
    EntityIDs     []identity.EntityID
    ReferenceTime time.Time
    Output        QueryOutput
}

type QueryAskService interface {
    Ask(context.Context, queryplan.Input) (queryplan.Execution, error)
}

type QueryAskServiceFactory func(context.Context) (QueryAskService, error)

type QueryAskCommand struct {
    NewService       QueryAskServiceFactory
    Input            io.Reader
    Output           io.Writer
    Limits           query.Limits
    MaxQuestionBytes int
}

func ValidateQueryAskInvocation(Invocation) error
func (command QueryAskCommand) Run(context.Context, Invocation) error
```

- [ ] **Step 1: Add runner syntax tests**

Assert this parses:

```text
query ask --entity entity-atlas-001 --entity entity-atlas-002 --reference-time 2026-07-29T12:00:00-04:00 --output json
```

Assert missing ID/reference time, duplicate/blank ID, invalid RFC3339, invalid output, positional question, `--question`, `query ask` group misuse, and extra args are rejected before `Execute`.

- [ ] **Step 2: Add the leaf without reading stdin during Cobra parsing**

Add `queryReferenceTimeFlagName = "reference-time"`, `ActionAsk`, and `Invocation.QueryAsk *QueryAskInput`. The ask leaf uses `cobra.NoArgs`, repeatable `--entity`, required `--reference-time`, default `--output text`, and calls:

```go
input, err := parseQueryAsk(command)
if err != nil {
    return err
}
return execute(command, Invocation{
    Command: CommandQuery,
    Action: ActionAsk,
    QueryAsk: &input,
})
```

Do not read `command.InOrStdin()` in the runner.

- [ ] **Step 3: Add `config validate query ask`**

Nest `ask` below the existing config-validation query node and emit:

```text
configuration valid for query ask
```

Retain `config validate query` unchanged.

- [ ] **Step 4: Write bounded-reader and lazy-factory tests**

Use a reader that records the maximum requested bytes and a factory that increments a counter. Assert:

- `io.LimitReader`/`io.ReadAll` consumes at most `MaxQuestionBytes + 1`;
- empty, whitespace, invalid UTF-8, oversize, ID-containing question, invalid local IDs, and invalid limits call the factory zero times;
- valid input calls the factory once and service once;
- service error produces zero stdout;
- renderer error produces zero stdout;
- short write returns `io.ErrShortWrite`.

- [ ] **Step 5: Run query-ask tests red**

Run:

```bash
go test ./internal/cli -run 'Test.*QueryAsk|TestRunner.*Ask|TestConfig.*QueryAsk' -count=1
```

Expected: FAIL because the command does not exist.

- [ ] **Step 6: Implement bounded stdin and pre-provider normalization**

```go
func readBoundedQuestion(input io.Reader, maximum int) (string, error) {
    if input == nil || maximum <= 0 {
        return "", errors.New("query ask input is not configured")
    }
    payload, err := io.ReadAll(io.LimitReader(input, int64(maximum)+1))
    if err != nil {
        return "", errors.New("read query ask question failed")
    }
    if len(payload) > maximum {
        return "", errors.New("query ask question exceeds the configured maximum")
    }
    return string(payload), nil
}
```

`QueryAskCommand.Run` validates the invocation, reads the bounded question, calls `queryplan.NormalizeInput` with CLI flags plus configured limits, then calls `NewService`. This ordering guarantees invalid private input constructs no provider. The service revalidates defense-in-depth.

- [ ] **Step 7: Refactor JSON conversion without changing typed output**

Change:

```go
func queryRequestToJSON(result query.Result) (queryRequestJSON, error)
```

to:

```go
func queryRequestToJSON(request query.Request) (queryRequestJSON, error)
func queryEnvelopeToJSON(result query.Result) (queryEnvelopeJSON, error)
```

`queryEnvelopeToJSON` constructs a request from the normalized result fields. `renderQueryJSON` marshals that envelope plus newline. Lock the current typed point/trend/trajectory/causal golden bytes before and after the refactor.

In `query_text.go`, extract:

```go
func renderQueryRequestText(output *strings.Builder, request query.Request) error
```

The existing `renderQueryText` constructs a request from the validated result, calls this helper, and then renders the result payload and gaps exactly as before. The ask renderer calls the same helper for `execution.Request`.

- [ ] **Step 8: Implement the ask JSON envelope**

Use:

```go
type queryAskEnvelopeJSON struct {
    SchemaVersion string               `json:"schema_version"`
    ReferenceTime string               `json:"reference_time"`
    Plan          queryAskPlanJSON     `json:"plan"`
    Planner       queryAskPlannerJSON  `json:"planner"`
    Query         queryEnvelopeJSON    `json:"query"`
}

type queryAskPlanJSON struct {
    Intent  string           `json:"intent"`
    Request queryRequestJSON `json:"request"`
}

type queryAskUsageJSON struct {
    InputTokens  int64 `json:"input_tokens"`
    OutputTokens int64 `json:"output_tokens"`
    TotalTokens  int64 `json:"total_tokens"`
}

type queryAskPlannerJSON struct {
    Provider               string            `json:"provider"`
    ModelID                string            `json:"model_id"`
    PromptVersion          string            `json:"prompt_version"`
    SchemaName             string            `json:"schema_name"`
    Attempts               int               `json:"attempts"`
    Usage                  queryAskUsageJSON `json:"usage"`
    WallLatencySeconds     float64           `json:"wall_latency_seconds"`
    ProviderLatencySeconds float64           `json:"provider_latency_seconds"`
}
```

`Plan` contains intent plus the exact normalized `queryRequestJSON`; `Planner` contains provider, model ID, prompt version, schema name, attempts, usage, wall latency seconds, and provider latency seconds. `Query` is the exact existing `stacks.temporal-query.v1` envelope. No field contains the question or raw proposal.

- [ ] **Step 9: Implement ask text by prefixing the existing renderer**

Build a complete `strings.Builder`:

```text
query ask schema: query-ask-v1
reference time: 2026-07-29T16:00:00Z
planner: provider=openai model=synthetic-planner-model prompt=query-plan-v1 schema=temporal_query_plan_v1 attempts=1
planner usage: input_tokens=120 output_tokens=40 total_tokens=160
planner latency: wall_seconds=0.250000 provider_seconds=0.200000
validated plan:
intent: point-in-time
entities: [entity-atlas-001]
entity match: all
predicates: [assigned_to]
point point: 2026-06-01T16:00:00Z
knowledge scope: current
limit: 0
deterministic result:
intent: point-in-time
entities: [entity-atlas-001]
entity match: all
predicates: [assigned_to]
point point: 2026-06-01T16:00:00Z
knowledge scope: current
limit: 0
facts:
  (none)
unresolved:
  (none)
gaps:
  (none)
```

Use fixed numeric formatting, omit the question/raw proposal, and do not summarize results.

- [ ] **Step 10: Verify execution parity before rendering**

Both renderers call a helper that confirms:

```go
execution.SchemaVersion == queryplan.OutputSchemaVersion
execution.ReferenceTime is canonical
execution.Request exactly matches execution.Result request fields
execution.Planner prompt/schema are exact
execution.Planner usage/attempt/latency values are valid
query.ValidateResult(execution.Result) == nil
```

Return bounded renderer errors without values.

- [ ] **Step 11: Run all CLI tests and byte-compatibility cases**

Run:

```bash
go test ./internal/cli -count=1
go test -race ./internal/cli -count=1
```

Expected: PASS; existing typed query golden output is unchanged.

- [ ] **Step 12: Commit CLI transport and rendering**

```bash
git add internal/cli
git commit -m "Add audited query ask CLI"
```

### Task 9: Route stdin and the ask validation target through the application

**Files:**
- Modify: `internal/app/execute.go`
- Modify: `internal/app/execute_test.go`
- Modify: `internal/app/bootstrap.go`
- Modify: `internal/app/bootstrap_test.go`
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`

**Interfaces:**
- Consumes: Task 8 `ActionAsk`, `ValidateQueryAskInvocation`, and `QueryAskInput`; Task 7 `config.CommandQueryAsk`.
- Produces:

```go
type CommandProvider interface {
    Commands(
        context.Context,
        config.Settings,
        io.Reader,
        io.Writer,
        io.Writer,
    ) (map[string]cli.Command, error)
}

func Execute(
    context.Context,
    []string,
    SettingsLoader,
    Bootstrap,
    io.Reader,
    io.Writer,
    io.Writer,
) error
```

- [ ] **Step 1: Write application routing tests**

Assert:

- typed query actions select `config.CommandQuery`;
- ask selects `config.CommandQueryAsk`;
- `config validate query ask` selects `CommandQueryAsk`;
- malformed ask invocation fails before load/bootstrap;
- injected stdin reaches `CommandProvider.Commands`;
- help/syntax/config validation do not read stdin;
- invalid ask settings fail before bootstrap/provider construction.

- [ ] **Step 2: Run application tests red**

Run:

```bash
go test ./internal/app -run 'Test.*QueryAsk|TestExecute.*Input' -count=1
```

Expected: FAIL because ask routing/stdin do not exist.

- [ ] **Step 3: Select the validation target by action**

In `targetForInvocation`:

```go
if invocation.Command == cli.CommandQuery {
    if invocation.Action == cli.ActionAsk {
        if err := cli.ValidateQueryAskInvocation(invocation); err != nil {
            return validationTarget{}, errors.New("query ask invocation is invalid")
        }
        return validationTarget{Command: config.CommandQueryAsk}, nil
    }
    if err := cli.ValidateQueryInvocation(invocation); err != nil {
        return validationTarget{}, errors.New("query invocation is invalid")
    }
    return validationTarget{Command: config.CommandQuery}, nil
}
```

In `targetForConfigValidation`, accept only `(CommandQuery, ActionAsk)` as `CommandQueryAsk`; retain `(CommandQuery, "")` as `CommandQuery`.

- [ ] **Step 4: Thread stdin through execution**

Add `stdin io.Reader` before stdout/stderr in `Execute`, `dispatch`, `CommandProvider.Commands`, and `CommandProviderFunc`. Set `cli.Runner.Input = stdin` and pass the same reader to `Commands`. Update every test fake signature and every `Execute` call explicitly with `strings.NewReader("synthetic question\n")` or `io.Discard`.

- [ ] **Step 5: Pass process stdin only at the process entrypoint**

Update the real call to:

```go
app.Execute(ctx, os.Args[1:], configLoader, bootstrap, os.Stdin, os.Stdout, os.Stderr)
```

Do not read `os.Stdin` anywhere else; tests inject readers.

- [ ] **Step 6: Run app and root transport tests**

Run:

```bash
go test ./internal/app ./cmd/stacks -run 'Test.*Execute|Test.*QueryAsk|Test.*CommandProvider' -count=1
go test ./internal/app ./cmd/stacks -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit stdin routing**

```bash
git add internal/app/execute.go internal/app/execute_test.go \
  internal/app/bootstrap.go internal/app/bootstrap_test.go \
  cmd/stacks/main.go cmd/stacks/main_test.go
git commit -m "Route private query input through application"
```

### Task 10: Compose planner providers and PostgreSQL lazily

**Files:**
- Create: `cmd/stacks/query.go`
- Create: `cmd/stacks/query_test.go`
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`
- Modify: `cmd/stacks/model.go`
- Modify: `cmd/stacks/model_test.go`
- Modify: `cmd/stacks/canonical_composition_test.go`

**Interfaces:**
- Consumes: E1 `queryplan.Service`; E2 clients implementing `queryplan.Model`; Tasks 7-9 settings, stdin, and CLI factory.
- Produces:

```go
type queryPlannerModelFactory func(
    context.Context,
    config.ModelSettings,
    modeltelemetry.Recorder,
    trace.Tracer,
) (queryplan.Model, error)

func newQueryPlannerModelWithContext(
    context.Context,
    config.ModelSettings,
    modeltelemetry.Recorder,
    trace.Tracer,
) (queryplan.Model, error)

func questionRecorder(
    modeltelemetry.Recorder,
) queryplan.QuestionRecorder

type temporalQueryExecutor struct {
    Open        func(context.Context, string) (queryDatabase, error)
    DatabaseURL string
    Limits      query.Limits
    Tracer      trace.Tracer
}

func (executor temporalQueryExecutor) Query(
    context.Context,
    query.Request,
) (query.Result, error)
```

- [ ] **Step 1: Write lazy executor tests**

Assert invalid request/limits opens zero databases; valid request opens exactly one, closes exactly one, builds `query.PostgresRepository`, and returns the existing `query.Service` result. Assert caller cancellation takes precedence over a conflicting open/read deadline and database URLs never enter errors.

- [ ] **Step 2: Run executor tests red**

Run:

```bash
go test ./cmd/stacks -run TestTemporalQueryExecutor -count=1
```

Expected: FAIL because `temporalQueryExecutor` does not exist.

- [ ] **Step 3: Implement the shared lazy executor**

Normalize before open:

```go
normalized, err := query.NormalizeRequest(request, executor.Limits)
if err != nil {
    return query.Result{}, err
}
database, err := executor.Open(ctx, executor.DatabaseURL)
if cancellationErr := canonicalContextError(ctx, err); cancellationErr != nil {
    return query.Result{}, cancellationErr
}
if err != nil {
    return query.Result{}, errors.New("open query database failed")
}
defer database.Close()
return (query.Service{
    Reader: query.PostgresRepository{
        Database: database,
        SnapshotObserver: query.PostgresSnapshotObserver{Tracer: executor.Tracer},
    },
    Limits: executor.Limits,
    Tracer: executor.Tracer,
}).Query(ctx, normalized)
```

Use this executor for existing typed `QueryCommand` and planned execution.

- [ ] **Step 4: Write model-factory tests**

For Bedrock, OpenAI, and Anthropic, assert the returned concrete client satisfies `queryplan.Model`, receives exact existing model settings, and does not invoke a provider. Assert unsupported provider is bounded. Existing `newModelWithContext` extraction tests remain unchanged.

- [ ] **Step 5: Implement planner model factory**

Mirror provider selection in `newModelWithContext`, but return `queryplan.Model`. Reuse the same concrete constructors and exact options; do not introduce a generic provider request type or call `context.Background()` from the contextual factory.

- [ ] **Step 6: Add root lifecycle tests before wiring**

Use counters/sentinels to prove:

- typed queries construct no planner;
- invalid ask flags/settings/question/ID disclosure construct no planner and open no DB;
- valid ask performs restricted preflight, constructs exactly one selected provider, invokes model, then opens exactly one DB;
- invalid/cannot-plan model output opens no DB;
- OpenAI/Anthropic restricted mode fails before construction;
- Bedrock restricted mode fails closed when invocation logging is enabled/unknown/denied/timed out;
- no Drive, Directory, source, ingestion, extraction, or canonical write repository is constructed;
- model/provider failure and database failure write no stdout;
- resources close on success and cancellation.

Inject distinct question, prompt, output, ID, predicate, timestamp, citation, provider-body, request-ID, database-URL, and credential markers. Drive the command through `app.Execute` with captured stderr and assert every marker is absent from the returned error and stderr.

- [ ] **Step 7: Run root composition tests red**

Run:

```bash
go test ./cmd/stacks -run 'Test.*QueryAsk|Test.*PlannerModel|TestTemporalQueryExecutor' -count=1
```

Expected: FAIL until composition is wired.

- [ ] **Step 8: Extend `commandRuntime` and composition**

Add:

```go
newQueryPlannerModel queryPlannerModelFactory
```

Default it to `newQueryPlannerModelWithContext`. Change `commandProviderWithRuntime` to receive stdin. In the query command closure:

1. Build one `query.Limits` and `temporalQueryExecutor`.
2. For non-ask actions, retain `ValidateQueryInvocation` and run existing `QueryCommand` with the executor.
3. For ask, validate `CommandQueryAsk` and `ValidateQueryAskInvocation`.
4. Construct `cli.QueryAskCommand` with stdin, stdout, limits, max bytes, and a `NewService` closure.
5. Inside `NewService`, call `requireRestrictedDisclosure`, then `runtime.newQueryPlannerModel`, then return `queryplan.Service`.

The service is:

```go
queryplan.Service{
    Model:            model,
    Executor:         executor,
    Limits:           limits,
    PlannerTimeout:   settings.QueryPlanner.Timeout,
    MaxQuestionBytes: settings.QueryPlanner.MaxQuestionBytes,
    QuestionRecorder: questionRecorder(invocations),
    Tracer:           tracer,
}
```

`questionRecorder` performs a safe structural type assertion to `queryplan.QuestionRecorder`; telemetry remains optional.

- [ ] **Step 9: Prove provider construction occurs after input normalization**

Use a `NewService` counter plus an invalid-ID-disclosure question in `QueryAskCommand` integration. Assert the provider factory and disclosure preflight are both zero calls. Use valid input plus an invalid proposal to assert provider one call, DB zero calls.

- [ ] **Step 10: Run root, CLI, app, and provider composition tests**

Run:

```bash
go test ./cmd/stacks ./internal/app ./internal/cli ./internal/queryplan -count=1
go test -race ./cmd/stacks ./internal/app ./internal/cli ./internal/queryplan -count=1
```

Expected: PASS.

- [ ] **Step 11: Commit lazy composition**

```bash
git add cmd/stacks/query.go cmd/stacks/query_test.go \
  cmd/stacks/main.go cmd/stacks/main_test.go \
  cmd/stacks/model.go cmd/stacks/model_test.go \
  cmd/stacks/canonical_composition_test.go
git commit -m "Compose temporal query planning lazily"
```

## E3 Phase Review Gate

- [ ] Dispatch a fresh phase reviewer. Require a lifecycle diagram derived from code and confirmation of this exact order:

```text
Cobra flags
  -> command-specific config validation
  -> bounded stdin read
  -> local input/ID/reference validation
  -> restricted disclosure preflight
  -> selected provider construction
  -> exact planning request
  -> strict proposal validation + query.NormalizeRequest
  -> PostgreSQL open
  -> unchanged query.Service
  -> complete audit rendering
  -> one stdout write
```

- [ ] Require byte-for-byte typed-query golden confirmation and verify future web transport can instantiate `queryplan.Service` without importing CLI/root types.

- [ ] Run:

```bash
go test ./internal/config ./internal/cli ./internal/app ./cmd/stacks ./internal/queryplan -count=1
go test -race ./internal/cli ./internal/app ./cmd/stacks ./internal/queryplan -count=1
git diff --check
```

- [ ] Save `.superpowers/sdd/plan-e/phase-e3-report.md`. Stop on any provider eagerness, database-before-plan, partial-output, config leakage, typed-query regression, or future-transport coupling.

---

# Phase E4 — Deterministic and PostgreSQL Acceptance

## E4 Invariants

1. Planned and direct execution of the same normalized request are structurally equal for all four intents.
2. Exact citations, conflicts, counterevidence, uncertainty, hypotheses, gaps, valid time, and recorded-time cutoffs survive unchanged.
3. Invalid/cannot-plan output performs no PostgreSQL read.
4. Migration inventory and bytes remain identical to the implementation baseline.
5. Documentation distinguishes local/synthetic/PostgreSQL proof from unrun live-provider and private-corpus validation.

### Task 11: Prove four-intent parity and document only implemented behavior

**Files:**
- Create: `internal/queryplan/parity_test.go`
- Create: `internal/queryplan/postgres_integration_test.go`
- Modify: `Makefile`
- Modify: `scripts/check-test-integration-packages.sh`
- Modify: `README.md`

**Interfaces:**
- Consumes: completed `queryplan.Service`, `query.Service`, `query.PostgresRepository`, and the existing synthetic Project Atlas fixture semantics.
- Produces: explicit fake-planner and real-PostgreSQL planned/direct parity acceptance.

- [ ] **Step 1: Add four-intent deterministic parity fixtures**

Create a table with:

```go
struct {
    name     string
    proposal string
    request  query.Request
}{
    {"point", pointProposal, pointRequest},
    {"trend", trendProposal, trendRequest},
    {"trajectory", trajectoryProposal, trajectoryRequest},
    {"causal", causalProposal, causalRequest},
}
```

For every row, run a fake model through `queryplan.Service`, record the request in an executor backed by the same synthetic `query.Reader`, execute `query.Service` directly with `request`, and require:

```go
reflect.DeepEqual(execution.Request, normalizedDirectRequest)
reflect.DeepEqual(execution.Result, directResult)
```

The synthetic snapshot must contain agreeing support, competing values, contradicting citations, a hypothesis, unresolved mention coverage, an authority exclusion, an explicit causal observation, causal counterevidence, and distinct valid/recorded times.

- [ ] **Step 2: Add recorded-time and limit cases**

Add a planned request with a valid-time window independent of `known-as-of`, and assert late-recorded evidence is absent exactly as in direct execution. Add trajectory/causal chronology overflow and assert `errors.Is(err, query.ErrLimitExceeded)` with no partial execution.

- [ ] **Step 3: Run deterministic parity tests**

Run:

```bash
go test ./internal/queryplan -run 'TestPlannedAndDirect|TestPlanned.*Limit' -count=1
```

Expected: PASS.

- [ ] **Step 4: Add PostgreSQL planned/direct parity**

Follow the existing integration database contract in `internal/query/postgres_integration_test.go`. Insert a fully synthetic corpus through canonical repository APIs, not hand-written persistence shortcuts. For point, trend, trajectory, and causal:

1. use a fake planner proposal;
2. execute through `queryplan.Service` with an executor wrapping `query.PostgresRepository`;
3. execute the exact request directly through `query.Service`;
4. require structural result equality;
5. assert citation IDs/roles, conflict alternatives, counterevidence, gaps, and cutoffs.

Use package-local helpers with explicit names:

```go
type plannerAtlasFixture struct {
    EntityIDs        []identity.EntityID
    PointRequest     query.Request
    TrendRequest     query.Request
    TrajectoryRequest query.Request
    CausalRequest    query.Request
}

type plannerPostgresExecutor struct {
    Database *postgres.Database
    Limits   query.Limits
}

func (executor plannerPostgresExecutor) Query(
    context.Context,
    query.Request,
) (query.Result, error)

func seedPlannerAtlasCorpus(
    t *testing.T,
    ctx context.Context,
    database *postgres.Database,
) plannerAtlasFixture

func assertPlannedDirectParity(t *testing.T, planned queryplan.Execution, direct query.Result)
```

Skip only when the documented test database environment is absent; never invoke a provider.

The complete adapter test run must retain the cleaner PR #31 regression proving one array-backed observation-evidence write operation; Plan E must not weaken or replace that test.

- [ ] **Step 5: Prove invalid output does no PostgreSQL read**

Wrap the real database boundary with a read counter. Feed malformed JSON and `cannot-plan`; require zero snapshot reads. Feed valid output; require exactly one snapshot read.

- [ ] **Step 6: Add `internal/queryplan` to explicit integration acceptance**

Change the Makefile tail to:

```make
go test ./internal/ingest ./internal/directory ./internal/app ./internal/doctor ./internal/query ./internal/queryplan -count=1
```

Extend `scripts/check-test-integration-packages.sh` with a required `./internal/queryplan` case while retaining the retired-analysis exclusion.

- [ ] **Step 7: Run PostgreSQL parity**

Run:

```bash
make db-up
make db-migrate
make db-status
make test-integration ENV_FILE=.env
```

Expected: core migrations current at 3/3; optional directory scope absent/unconfigured; all integration packages pass.

- [ ] **Step 8: Update README with exact implemented boundary**

Document:

```bash
printf '%s\n' 'What changed between the two stated periods?' |
  go run ./cmd/stacks query ask \
    --entity entity-atlas-001 \
    --reference-time 2026-07-29T12:00:00-04:00 \
    --output json
```

State that stdin is private but disclosed to the explicitly configured model provider; IDs and evidence are attached/queried locally; output contains the normalized plan plus unchanged cited deterministic result; typed query commands are the no-provider fallback; there is no narration/entity matching/web UI/persistence; and live-provider/private-corpus validation has not been run.

Add:

```bash
go run ./cmd/stacks config validate query ask
```

and document the two exact planner environment variables with defaults/ranges.

- [ ] **Step 9: Run documentation and terminology checks**

Run:

```bash
rg -n 'manager.confidence|manager-confidence|--question|query ask .+\\?' \
  README.md internal cmd .env.example config.example.yaml config.example.json
make test-retired-analysis-terminology
git diff --check
```

Expected: no restored retired vertical, no question flag/argument documentation, and all checks pass.

- [ ] **Step 10: Commit E4 acceptance and docs**

```bash
git add internal/queryplan/parity_test.go \
  internal/queryplan/postgres_integration_test.go \
  Makefile scripts/check-test-integration-packages.sh README.md
git commit -m "Prove planned temporal query parity"
```

## E4 and Final Whole-Branch Review Gate

- [ ] Compare migration bytes with the saved E1 baseline and the approved base:

```bash
git diff --exit-code c6852d2 -- \
  ':(glob)adapters/postgres/coremigrations/migrations/*.sql' \
  ':(glob)adapters/postgres/directorymigrations/migrations/*.sql' \
  db/init
find adapters/postgres -path '*/migrations/*' -type f -print0 | sort -z | xargs -0 shasum -a 256
```

Expected: no migration or init SQL diff and identical digests.

- [ ] Run the complete acceptance sequence in order:

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

Record each command, exit code, and relevant bounded result. Do not claim provider or private-corpus acceptance.

- [ ] Dispatch a fresh E4 phase reviewer, then a different fresh whole-branch reviewer. Give the final reviewer:

```bash
git status --short --branch
git log --oneline --decorate c6852d2..HEAD
git diff --stat c6852d2...HEAD
git diff c6852d2...HEAD
```

along with the design, this plan, every task/phase report, acceptance output, and migration digest comparison.

- [ ] Require the final reviewer to answer explicitly:

1. Can any model output bypass the closed schema, strict decoder, or `query.NormalizeRequest`?
2. Can any supplied ID or retrieved evidence enter a provider request?
3. Can any invalid plan open PostgreSQL or emit stdout?
4. Can planning alter a deterministic result, citation association, conflict, gap, cutoff, limit, or causal rule?
5. Can a typed query construct a provider or require planner settings?
6. Are same-provider retries exact and cross-provider failover impossible?
7. Can any forbidden private marker enter telemetry/errors/stderr?
8. Did narration, identity matching, web UX, persistence, or migrations enter scope?
9. Are all existing extraction paths and typed outputs preserved?
10. Is every claimed acceptance backed by an exact command result?

- [ ] Fix every actionable finding test-first, repeat the relevant task review, rerun the complete acceptance sequence, and obtain a clean final whole-branch review.

- [ ] Save `.superpowers/sdd/plan-e/phase-e4-report.md` and `.superpowers/sdd/plan-e/final-review-report.md`. Stop with the reviewed local branch; do not push or invoke a provider.

---

## Publication Gate — Not Authorized by Implementation Approval

After Jacob explicitly approves local implementation and separately authorizes publication:

- [ ] Fetch and inspect current `origin/main`, branch ancestry, open PRs, review threads, checks, and every newly opened daily-cleaner PR.
- [ ] Reconcile current `origin/main` into Plan E without rewriting history. Preserve both sides of legitimate test conflicts.
- [ ] Rerun the complete whole-tree and PostgreSQL acceptance sequence on the exact reconciled head.
- [ ] Push and open a PR without administrator bypass. Wait for protected checks on the exact head.
- [ ] Review cleaner PRs for relevance/correctness; merge valid ones sequentially after fresh checks, close obsolete ones, update `main`, reconcile Plan E, and rerun whole-tree acceptance.
- [ ] Merge Plan E only after explicit approval and required protected checks.

## Live-Provider Validation Gate — Separately Authorized

After separate explicit approval:

- [ ] Select exactly one provider/model and a bounded token/attempt budget.
- [ ] Use only synthetic questions, canonical IDs, and synthetic PostgreSQL data.
- [ ] Never print secrets, private questions, prompt/schema bodies, or raw model output.
- [ ] Validate structured-output compatibility, refusal/incomplete handling, audit metadata, and deterministic execution.
- [ ] Report results as provider/model-specific; do not generalize to other providers.
- [ ] Treat each additional provider and any private-corpus run as a separate authorization and acceptance report.

## Completion Criteria

Local Plan E implementation is complete only when all of these are true:

1. `query ask` reads one bounded UTF-8 question only from injected stdin.
2. The caller supplies every canonical ID and an explicit RFC3339 reference instant.
3. Local validation rejects ID disclosure and invalid input before provider construction.
4. Provider input contains no IDs or retrieved evidence.
5. Strict structured output either returns a bounded cannot-plan error or composes one existing closed request accepted by `query.NormalizeRequest`.
6. Invalid/non-executable output performs no database read and no stdout write.
7. Valid output executes through the unchanged Plan D query service.
8. Text/JSON output includes exact audit metadata, normalized request, and unchanged cited result while omitting question/raw output.
9. Same-provider 429/transient retries are bounded and exact; terminal failures do not retry; no failover exists.
10. Existing typed queries remain provider-free and byte-compatible.
11. All privacy sentinels are absent from errors, telemetry, traces, metrics attributes, and stderr.
12. Planned/direct results are structurally equal for all four intents in pure and PostgreSQL tests.
13. Citation/conflict/counterevidence/uncertainty/gap/valid-time/recorded-time associations are unchanged.
14. No migration byte or fingerprint changes.
15. Every task, phase, and whole-branch review has no unresolved actionable finding.
16. The complete acceptance sequence passes.
17. Documentation describes only implemented local behavior and explicitly marks live-provider/private-corpus validation unrun.
18. The branch remains local until publication is separately approved.
