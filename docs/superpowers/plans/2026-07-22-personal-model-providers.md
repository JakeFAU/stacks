# Personal Model Providers and Restricted Disclosure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow the manager-confidence workflow to use personal OpenAI or Anthropic credentials with synthetic personal Google Drive documents while making future restricted-data execution fail closed unless Bedrock is selected and invocation logging is confirmed disabled.

**Architecture:** Preserve `extract.Model` as the only generation boundary. Add typed provider/data-mode policy, two official-SDK edge adapters, provider-neutral telemetry and provenance, and a pre-source restricted-disclosure gate in the composition root. Keep all deterministic extraction, validation, admission, caching, retry/resume, and temporal analysis behavior above the provider boundary unchanged.

**Tech Stack:** Go 1.26, PostgreSQL/pgvector with Goose migrations, OpenTelemetry, official OpenAI Go SDK v3, official Anthropic Go SDK, AWS SDK for Go v2, Google Drive/Docs APIs, and synthetic private-data-free fixtures.

## Global Constraints

- Never print, copy, log, trace, commit, or place real API keys, OAuth tokens, private document text, prompts containing private text, or raw provider responses in errors.
- Keep normal Google Drive/Docs OAuth read-only. Synthetic Google Doc creation is a separate permission boundary and is not part of this implementation.
- Require explicit `STACKS_DATA_MODE`, `STACKS_MODEL_PROVIDER`, and `STACKS_MODEL_ID` for `doctor`, `sync`, and `analyze`; do not guess a provider or model.
- Permit OpenAI and Anthropic only in `personal` mode. In `restricted` mode, permit only Bedrock and require a confirmed-disabled Bedrock invocation-logging state before Google OAuth or document discovery.
- Submit the existing embedded prompt and JSON Schema unchanged. Do not strip schema keywords or weaken deterministic validation for provider compatibility.
- Disable SDK retry layers. Each adapter owns exactly `STACKS_MODEL_MAX_ATTEMPTS` attempts and never falls back to a different provider.
- Preserve the existing Bedrock extraction and analysis digest byte sequences exactly. Use a new provider-qualified namespace only for OpenAI and Anthropic identities.
- Backfill existing model-derived rows as `model_provider='bedrock'` and `data_mode='legacy'`; never claim a disclosure mode that was not enforced historically.
- Use only synthetic content for direct-provider and personal-Drive acceptance. A successful personal-provider run is not company-IP, Bedrock-runtime, or company-Google acceptance.
- Add tests before implementation for every behavior change. Keep every commit coherent and do not mix unrelated cleanup.

## File map

| Path | Responsibility |
| --- | --- |
| `internal/modelpolicy/policy.go` | Provider, data-mode, region, and disclosure invariants shared by configuration, orchestration, storage, and telemetry |
| `internal/config/poc.go`, `internal/config/config.go` | Typed common model settings, provider credentials, normalized environment variables, and command-specific validation |
| `internal/modeltelemetry/recorder.go` | Provider-neutral bounded model metrics and span events |
| `internal/openai/client.go` | Stateless OpenAI Responses structured-output adapter with exact retries and safe errors |
| `internal/anthropic/client.go` | Stateless Anthropic Messages structured-output adapter with exact retries and safe errors |
| `internal/bedrock/client.go` | Existing Bedrock adapter updated only to consume shared policy/telemetry contracts |
| `internal/ingest/service.go`, `internal/analysis/service.go` | Provider-qualified identities, Bedrock-compatible digests, and disclosure provenance on completed runs |
| `db/migrations/00010_model_provider_provenance.sql` | Provider/data-mode provenance and provider-aware Bedrock-region constraints |
| `internal/storage/documents.go`, `internal/storage/analysis.go` | Persist and retrieve provider/data-mode provenance without changing atomicity or lease behavior |
| `internal/doctor/service.go`, `internal/doctor/providers.go` | Provider-neutral read-only checks and the fail-closed Bedrock disclosure preflight |
| `cmd/stacks/model.go`, `cmd/stacks/main.go` | Provider selection and ordering of static validation, disclosure preflight, Google access, storage, and model construction |
| `.env.example`, `README.md` | Safe configuration examples, personal/restricted operating procedures, and acceptance boundaries |

---

### Task 1: Define provider and disclosure policy types

**Files:**
- Create: `internal/modelpolicy/policy.go`
- Create: `internal/modelpolicy/policy_test.go`

**Interfaces:**
- Produces: `modelpolicy.Provider`, `modelpolicy.DataMode`, and bounded constants
- Produces: `modelpolicy.Invocation{Provider, DataMode, Region}.Validate() error`
- Produces: `modelpolicy.Provider.Valid() bool`, `modelpolicy.DataMode.ValidForNewRun() bool`

- [ ] **Step 1: Write failing tests** for the complete policy matrix: personal permits all three providers; restricted permits only Bedrock; Bedrock requires a trimmed non-empty region; direct providers require an empty region; `legacy` is valid only as stored historical provenance and not for a new invocation.

```go
func TestInvocationValidateRejectsRestrictedDirectProvider(t *testing.T) {
    invocation := Invocation{Provider: ProviderOpenAI, DataMode: DataModeRestricted}
    if err := invocation.Validate(); err == nil {
        t.Fatal("expected restricted OpenAI invocation to fail")
    }
}

func TestInvocationValidateRequiresBedrockRegionOnly(t *testing.T) {
    tests := []struct {
        name string
        invocation Invocation
        wantErr bool
    }{
        {"bedrock missing region", Invocation{Provider: ProviderBedrock, DataMode: DataModePersonal}, true},
        {"bedrock region", Invocation{Provider: ProviderBedrock, DataMode: DataModePersonal, Region: "us-east-1"}, false},
        {"openai region", Invocation{Provider: ProviderOpenAI, DataMode: DataModePersonal, Region: "us-east-1"}, true},
        {"anthropic no region", Invocation{Provider: ProviderAnthropic, DataMode: DataModePersonal}, false},
    }
    // Run the table and compare err != nil with wantErr.
}
```

- [ ] **Step 2: Run RED:** `go test ./internal/modelpolicy` must fail because the package does not exist.
- [ ] **Step 3: Implement** the closed vocabularies below. Keep storage-only `DataModeLegacy` out of `ValidForNewRun`.

```go
type Provider string

const (
    ProviderBedrock   Provider = "bedrock"
    ProviderOpenAI    Provider = "openai"
    ProviderAnthropic Provider = "anthropic"
)

type DataMode string

const (
    DataModePersonal   DataMode = "personal"
    DataModeRestricted DataMode = "restricted"
    DataModeLegacy     DataMode = "legacy"
)

type Invocation struct {
    Provider Provider
    DataMode DataMode
    Region   string
}
```

- [ ] **Step 4: Run GREEN:** `go test ./internal/modelpolicy` must pass.
- [ ] **Step 5: Commit:** `git add internal/modelpolicy && git commit -m "Define model disclosure policy"`.

---

### Task 2: Normalize model configuration and reject legacy model variables

**Files:**
- Modify: `internal/config/poc.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/poc_test.go` if configuration tests are split during implementation
- Modify: `.env.example`

**Interfaces:**
- Produces: `config.ModelSettings{Provider, DataMode, ModelID, MaxOutputTokens, MaxAttempts, AWSProfile, AWSRegion, OpenAIAPIKey, AnthropicAPIKey}`
- Replaces: `STACKS_BEDROCK_MODEL_ID`, `STACKS_BEDROCK_MAX_TOKENS`, and `STACKS_BEDROCK_MAX_ATTEMPTS`
- Adds: `STACKS_DATA_MODE`, `STACKS_MODEL_PROVIDER`, `STACKS_MODEL_ID`, `STACKS_MODEL_MAX_OUTPUT_TOKENS`, and `STACKS_MODEL_MAX_ATTEMPTS`

- [ ] **Step 1: Write failing environment-loading tests** proving that common model settings load without defaults for mode/provider/model, attempts retain the current default of `5`, provider credentials are read only into memory, and no test failure or formatted error contains a secret value.
- [ ] **Step 2: Write failing validation tests** proving:
  - `serve`, `auth`, `entities`, and `review` retain their existing minimal requirements;
  - `doctor`, `sync`, and `analyze` require explicit mode/provider/model;
  - restricted OpenAI/Anthropic fails before a boundary is built;
  - Bedrock requires `STACKS_AWS_REGION`, OpenAI requires `OPENAI_API_KEY`, and Anthropic requires `ANTHROPIC_API_KEY`;
  - direct providers reject a configured AWS region as provider provenance only if it is assigned to the invocation, not merely present for another local workflow;
  - a non-empty legacy model variable name is reported as unsupported without reporting its value;
  - model token and attempt bounds are still positive and attempts do not exceed the existing maximum of `5`.

```go
func TestPoCSettingsRejectsLegacyModelEnvironment(t *testing.T) {
    settings := validSyncSettings()
    settings.LegacyModelEnvironment = []string{BedrockModelIDEnvironmentVariable}
    err := settings.Validate(CommandSync)
    if err == nil || !strings.Contains(err.Error(), BedrockModelIDEnvironmentVariable) {
        t.Fatalf("error = %v", err)
    }
}
```

- [ ] **Step 3: Run RED:** `go test ./internal/config` must fail on the new names and matrix.
- [ ] **Step 4: Implement** `ModelSettings` and replace Bedrock-specific fields in `PoCSettings` with one `Model` field. Use `os.LookupEnv` only to record legacy variable names; never retain or format their values.

```go
const (
    DataModeEnvironmentVariable       = "STACKS_DATA_MODE"
    ModelProviderEnvironmentVariable  = "STACKS_MODEL_PROVIDER"
    ModelIDEnvironmentVariable        = "STACKS_MODEL_ID"
    ModelMaxTokensEnvironmentVariable = "STACKS_MODEL_MAX_OUTPUT_TOKENS"
    ModelMaxAttemptsEnvironmentVariable = "STACKS_MODEL_MAX_ATTEMPTS"
    OpenAIAPIKeyEnvironmentVariable   = "OPENAI_API_KEY"
    AnthropicAPIKeyEnvironmentVariable = "ANTHROPIC_API_KEY"
)

type ModelSettings struct {
    Provider       modelpolicy.Provider
    DataMode       modelpolicy.DataMode
    ModelID        string
    MaxOutputTokens int
    MaxAttempts    int
    AWSProfile     string
    AWSRegion      string
    OpenAIAPIKey   string
    AnthropicAPIKey string
}
```

Do not pass ambient SDK routing settings through implicitly. Add `OPENAI_BASE_URL`, `OPENAI_ORG_ID`, `OPENAI_PROJECT_ID`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, and `ANTHROPIC_PROFILE` to the unsupported-environment-name check for model commands. This slice accepts only the explicit API key plus the providers' production API endpoints.

- [ ] **Step 5: Update `.env.example`** with empty or obviously synthetic placeholders only. Delete the three old Bedrock model variable examples and document that provider/model/mode are intentionally explicit.
- [ ] **Step 6: Run GREEN:** `go test ./internal/config` must pass.
- [ ] **Step 7: Commit:** `git add internal/config .env.example && git commit -m "Normalize model provider configuration"`.

---

### Task 3: Generalize bounded model telemetry

**Files:**
- Create: `internal/modeltelemetry/recorder.go`
- Create: `internal/modeltelemetry/recorder_test.go`
- Modify: `internal/bedrock/client.go`
- Delete after call sites move: `internal/bedrock/recorder.go`
- Delete after call sites move: `internal/bedrock/recorder_test.go`

**Interfaces:**
- Produces: `modeltelemetry.Observation`
- Produces: `modeltelemetry.Recorder`
- Produces: `modeltelemetry.NewMetricsRecorder(metric.Meter) (*MetricsRecorder, error)`

- [ ] **Step 1: Write failing recorder tests** proving only the bounded provider/data-mode enum, model ID, prompt version, outcome, token counts, latency, and attempt count are emitted; invalid observations are dropped; no prompt, input, output, provider error, credential, entity, or source fields exist.

```go
type Observation struct {
    Provider        modelpolicy.Provider
    DataMode        modelpolicy.DataMode
    ModelID         string
    PromptVersion   string
    Outcome         string
    InputTokens     int64
    OutputTokens    int64
    TotalTokens     int64
    WallLatency     time.Duration
    ProviderLatency time.Duration
    Attempts        int
}

type Recorder interface {
    Record(context.Context, Observation)
}
```

- [ ] **Step 2: Run RED:** `go test ./internal/modeltelemetry` must fail.
- [ ] **Step 3: Implement** instruments and events under `stacks.model.*`. Use attribute keys `stacks.model.provider`, `stacks.model.data_mode`, `stacks.model.model_id`, `stacks.model.prompt_version`, and `stacks.model.outcome`.
- [ ] **Step 4: Adapt Bedrock** to accept a shared recorder plus the validated data mode in `bedrock.Options`. The provider is always `bedrock`; keep Bedrock request/response behavior and retry classification unchanged.
- [ ] **Step 5: Run GREEN:** `go test ./internal/modeltelemetry ./internal/bedrock` must pass, including existing privacy and retry tests.
- [ ] **Step 6: Commit:** `git add internal/modeltelemetry internal/bedrock && git commit -m "Generalize model invocation telemetry"`.

---

### Task 4: Add the stateless OpenAI Responses adapter

**Files:**
- Create: `internal/openai/client.go`
- Create: `internal/openai/client_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `openai.New(apiKey string, options Options) (*Client, error)`
- Produces: `openai.Client.Generate(context.Context, extract.Request) (extract.Response, error)`
- Consumes: official `github.com/openai/openai-go/v3` SDK with SDK retries disabled

- [ ] **Step 1: Add the pinned SDK:** `go get github.com/openai/openai-go/v3@v3.31.0 && go mod tidy`.
- [ ] **Step 2: Write failing request-contract tests** around a fake `responsesAPI` asserting:
  - the exact embedded prompt/schema contract is verified before the fake is called;
  - `Instructions` is the reviewed system prompt and `Input.OfString` is the private input;
  - `Model` and `MaxOutputTokens` are explicit;
  - `Store` is explicitly false and `Background` is explicitly false;
  - `Text.Format` is `responses.ResponseFormatTextConfigParamOfJSONSchema(request.SchemaName, schemaMap)` with `Strict` explicitly true;
  - no tools, files, conversation, previous-response ID, hosted execution, or connector fields are set.

```go
type responsesAPI interface {
    New(context.Context, responses.ResponseNewParams, ...option.RequestOption) (*responses.Response, error)
}

format := responses.ResponseFormatTextConfigParamOfJSONSchema(request.SchemaName, schema)
format.OfJSONSchema.Strict = openaisdk.Bool(true)
params := responses.ResponseNewParams{
    Background:      openaisdk.Bool(false),
    Instructions:    openaisdk.String(request.SystemPrompt),
    Input:           responses.ResponseNewParamsInputUnion{OfString: openaisdk.String(request.Input)},
    MaxOutputTokens: openaisdk.Int(int64(client.maxOutputTokens)),
    Model:           responses.ResponsesModel(client.modelID),
    Store:           openaisdk.Bool(false),
    Text: responses.ResponseTextConfigParam{
        Format: format,
    },
}
```

- [ ] **Step 3: Write failing response tests** for one completed `output_text`, malformed JSON, refusal content, incomplete status, multiple/non-text content blocks, missing/negative usage, and returned-model mismatch. Only a single completed JSON text result may return success.
- [ ] **Step 4: Write failing retry/error tests** proving exact attempt counts for 429/408/5xx and retryable transport errors; terminal behavior for 400/401/403/404, refusals, invalid output, and context cancellation; mapping 401 to `extract.ErrAuthentication` and 403 to `extract.ErrAuthorization`; and absence of raw API bodies from returned errors.
- [ ] **Step 5: Run RED:** `go test ./internal/openai` must fail.
- [ ] **Step 6: Implement** the adapter with `responses.NewResponseService(option.WithEnvironmentProduction(), option.WithAPIKey(apiKey), option.WithMaxRetries(0))`, avoiding `openaisdk.NewClient` so ambient SDK organization, project, and base-URL variables cannot alter routing. Decode `request.JSONSchema` into `map[string]any`, but submit it unchanged semantically; reject decoding failure before network access. Own a context-aware bounded exponential backoff with no randomness in tests by injecting a `wait` function or deterministic retry delay.
- [ ] **Step 7: Record** one `modeltelemetry.Observation` per completed `Generate`, never per attempt, and finish the owning `stacks.model.generate` span explicitly.
- [ ] **Step 8: Run GREEN:** `go test ./internal/openai` must pass.
- [ ] **Step 9: Commit:** `git add go.mod go.sum internal/openai && git commit -m "Add OpenAI structured model adapter"`.

---

### Task 5: Add the stateless Anthropic Messages adapter

**Files:**
- Create: `internal/anthropic/client.go`
- Create: `internal/anthropic/client_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `anthropic.New(apiKey string, options Options) (*Client, error)`
- Produces: `anthropic.Client.Generate(context.Context, extract.Request) (extract.Response, error)`
- Consumes: official `github.com/anthropics/anthropic-sdk-go` SDK with SDK retries disabled

- [ ] **Step 1: Add the pinned SDK:** `go get github.com/anthropics/anthropic-sdk-go@v1.59.0 && go mod tidy`.
- [ ] **Step 2: Write failing request-contract tests** around a fake `messagesAPI` asserting the exact system prompt, private user input, model, token bound, and unchanged JSON Schema are sent; `Tools`, `Container`, cache-control, files, batches, and managed-agent features remain unset.

```go
type messagesAPI interface {
    New(context.Context, anthropicsdk.MessageNewParams, ...option.RequestOption) (*anthropicsdk.Message, error)
}

params := anthropicsdk.MessageNewParams{
    MaxTokens: int64(client.maxOutputTokens),
    Messages: []anthropicsdk.MessageParam{
        anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(request.Input)),
    },
    Model: anthropicsdk.Model(client.modelID),
    OutputConfig: anthropicsdk.OutputConfigParam{
        Format: anthropicsdk.JSONOutputFormatParam{Schema: schema},
    },
    System: []anthropicsdk.TextBlockParam{{Text: request.SystemPrompt}},
}
```

- [ ] **Step 3: Write failing response tests** for exactly one text block with `StopReasonEndTurn`, invalid JSON, `StopReasonMaxTokens`, `StopReasonRefusal`, tool/pause/stop-sequence outcomes, multiple or non-text blocks, negative usage, and returned-model mismatch.
- [ ] **Step 4: Write failing retry/error tests** with the same exact-attempt, cancellation, auth/authorization, safe-error, and telemetry guarantees as OpenAI. Calculate total usage as input plus cache-creation plus cache-read plus output tokens while preserving the provider's authoritative output total.
- [ ] **Step 5: Run RED:** `go test ./internal/anthropic` must fail.
- [ ] **Step 6: Implement** with `anthropicsdk.NewMessageService(option.WithEnvironmentProduction(), option.WithAPIKey(apiKey), option.WithMaxRetries(0))`, avoiding `anthropicsdk.NewClient` so ambient SDK profile, auth-token, and base-URL settings cannot alter credential or routing policy. Use the same bounded adapter-owned retry policy and no cross-provider fallback.
- [ ] **Step 7: Run GREEN:** `go test ./internal/anthropic` must pass.
- [ ] **Step 8: Commit:** `git add go.mod go.sum internal/anthropic && git commit -m "Add Anthropic structured model adapter"`.

---

### Task 6: Qualify derivation and analysis identities by provider

**Files:**
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/ingest/validate.go`
- Modify: `internal/ingest/validate_test.go`
- Modify: `internal/analysis/service.go`
- Modify: `internal/analysis/service_test.go`

**Interfaces:**
- Adds: `Provider modelpolicy.Provider` to `ingest.DerivationIdentity` and `analysis.AnalysisIdentity`
- Adds: `DataMode modelpolicy.DataMode` to `ingest.Completion` and `analysis.Completion`
- Preserves: existing Bedrock digest bytes for every previously valid identity

- [ ] **Step 1: Write a Bedrock compatibility fixture test** using a fixed document/version/identity and a literal pre-change digest hex captured from the current main branch before editing. Add the equivalent literal fixture for `analysis.ComputeInputDigest`.

```go
func TestComputeDerivationDigestPreservesBedrockV5Bytes(t *testing.T) {
    version, err := knowledge.NewDocumentVersion(knowledge.DocumentVersionInput{
        Provider: "drive", ProviderDocumentID: "document-compat", Title: "Synthetic Meeting",
        Locator: "https://example.invalid/document", ProviderVersion: "version-1", ProviderRevision: "revision-1",
        ModifiedAt: time.Date(2026, time.July, 21, 11, 0, 0, 0, time.UTC),
        RecordedAt: time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC),
        Tabs: []source.Tab{{ID: "transcript-tab", Title: "Transcript", Path: []string{"Transcript"}, Role: source.TabRoleTranscript, Text: "Synthetic transcript."}},
    })
    if err != nil { t.Fatal(err) }
    got, err := ComputeDerivationDigest(version, DerivationIdentity{
        Provider: modelpolicy.ProviderBedrock,
        Region: "us-east-1", ModelID: "synthetic-model", MaxTokens: 256,
        PromptVersion: extract.ExtractionPromptVersion,
        SchemaDigest: sha256.Sum256(extract.ExtractionJSONSchema()),
    })
    if err != nil { t.Fatal(err) }
    if hex.EncodeToString(got[:]) != "b000ccd59675147eebe18c0698207b23ab154160413ec40800c09dbb32e266e1" {
        t.Fatalf("digest = %x", got)
    }
}
```

The analysis compatibility fixture uses employee `aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee`, manager `11111111-2222-3333-4444-555555555555`, prompt `analyze-v1`, the current `AnalysisPolicyVersion`, region `us-east-1`, model `synthetic-model`, 256 tokens, and one `signal` input whose ID is `99999999-aaaa-bbbb-cccc-dddddddddddd` and digest is SHA-256 of `signal-input`. Its literal expected digest is `0e0dabec4a207d0c8803b506d6b7bdee726120ff84f620f7b793d9fa483ff4a7`.

- [ ] **Step 2: Write provider-separation tests** proving identical input/model/token values produce different OpenAI and Anthropic digests and do not collide with Bedrock. Prove data mode does not alter a digest.
- [ ] **Step 3: Run RED:** `go test ./internal/ingest ./internal/analysis` must fail because provider-aware identities do not exist.
- [ ] **Step 4: Implement validation** through `modelpolicy.Invocation`. Bedrock identities require region; direct identities require no region. Services receive `Provider` and `DataMode`; they place mode only in completion provenance after an actual generation succeeds.
- [ ] **Step 5: Implement digest branching:** retain the existing `stacks.extraction-derivation.v5` and existing analysis byte-write order for Bedrock without inserting provider bytes. For direct providers use new namespaces `stacks.extraction-derivation.v6.provider` and `stacks.pair-analysis-input.v2.provider`, writing provider before model ID and retaining all existing immutable fields.
- [ ] **Step 6: Run GREEN:** `go test ./internal/ingest ./internal/analysis` must pass, including literal compatibility fixtures, cache reuse, stale-input, retry/resume, and admission behavior.
- [ ] **Step 7: Commit:** `git add internal/ingest internal/analysis && git commit -m "Qualify model derivations by provider"`.

---

### Task 7: Add migration 00010 and persist model provenance

**Files:**
- Create: `db/migrations/00010_model_provider_provenance.sql`
- Modify: `internal/storage/documents.go`
- Modify: `internal/storage/documents_test.go`
- Modify: `internal/storage/analysis.go`
- Modify: `internal/storage/analysis_test.go`
- Modify: `internal/storage/migration_test.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/doctor/providers.go`
- Modify: `internal/doctor/providers_test.go`

**Schema contract:**
- `extraction_runs.model_provider` and `extraction_runs.data_mode` are non-null after backfill.
- `analysis_runs.model_provider` and `analysis_runs.data_mode` are required for completed model reports and may remain null for historical/in-progress non-model states where the current schema permits absent model identity.
- Bedrock rows require non-empty `bedrock_region`; OpenAI/Anthropic rows require `bedrock_region IS NULL`.
- Existing rows become `bedrock` / `legacy` without changing other provenance or digests.

- [ ] **Step 1: Extend migration contract tests** to require migration 00010, the new columns, bounded check constraints, legacy backfill, and provider-aware region constraint. Update `doctor.requiredMigrationVersions()` to include `10` only after the RED assertion exists.
- [ ] **Step 2: Write PostgreSQL-gated upgrade tests** that migrate an existing schema through 00009, insert representative extraction and completed analysis rows, apply 00010, and assert `bedrock/legacy` backfill without digest changes.
- [ ] **Step 3: Write PostgreSQL constraint tests** proving Bedrock without region fails, direct provider with region fails, invalid provider/mode fails, and valid personal OpenAI/Anthropic rows succeed.
- [ ] **Step 4: Run RED:** with local integration URLs loaded, run `go test ./internal/storage ./internal/doctor -run 'TestMigration|TestModelProvider|TestRequiredMigration'`; it must fail before migration 00010 exists.
- [ ] **Step 5: Implement the forward-only migration** in this order so every statement is valid against existing data:
  1. add nullable `model_provider` and `data_mode` columns;
  2. backfill rows that already carry model identity to `bedrock` / `legacy`;
  3. drop the extraction-run non-null constraint on `bedrock_region` while retaining non-blank-when-present validation;
  4. make extraction provider/mode non-null;
  5. add bounded provider/mode checks;
  6. add provider-aware region checks;
  7. add a completed-analysis check requiring provider/mode/model/tokens/report provenance together.
- [ ] **Step 6: Update storage SQL** so extraction lease creation and analysis completion write provider, nullable region, and data mode atomically. Reads reconstruct provider-aware identities. Never infer provider from a nullable region in application code.
- [ ] **Step 7: Extend integration behavior** to exercise migration upgrade, extraction lease acquisition/reclamation, retry/resume, stable Bedrock cache lookup, provider-separated direct caches, admission quarantine, alias lifecycle, analysis deduplication, and persisted provider/data-mode provenance.
- [ ] **Step 8: Run GREEN:** `go test ./internal/storage ./internal/doctor` with both test database URLs must pass.
- [ ] **Step 9: Commit:** `git add db/migrations/00010_model_provider_provenance.sql internal/storage internal/doctor/providers.go internal/doctor/providers_test.go && git commit -m "Persist model provider provenance"`.

---

### Task 8: Make doctor provider-neutral and add the restricted Bedrock gate

**Files:**
- Modify: `internal/doctor/service.go`
- Modify: `internal/doctor/service_test.go`
- Modify: `internal/doctor/providers.go`
- Modify: `internal/doctor/providers_test.go`
- Create: `internal/doctor/openai.go`
- Create: `internal/doctor/openai_test.go`
- Create: `internal/doctor/anthropic.go`
- Create: `internal/doctor/anthropic_test.go`

**Interfaces:**
- Produces: `doctor.ModelProbe{CheckCredentials, CheckModel}` or an equivalently small consumer-owned interface
- Produces: `doctor.DisclosureProbe.InvocationLogging(context.Context) (InvocationLoggingState, error)`
- Produces: `doctor.RequireRestrictedDisclosure(context.Context, modelpolicy.Invocation, DisclosureProbe) error`

- [ ] **Step 1: Write failing gate tests** proving personal mode performs no logging inspection; restricted direct providers fail immediately; restricted Bedrock permits only `disabled`; and `enabled`, `unknown`, access denied, timeout, cancellation, or any inspection error fail closed with bounded errors.

```go
func TestRequireRestrictedDisclosureFailsClosed(t *testing.T) {
    probe := fakeDisclosureProbe{state: InvocationLoggingUnknown}
    err := RequireRestrictedDisclosure(context.Background(), modelpolicy.Invocation{
        Provider: modelpolicy.ProviderBedrock,
        DataMode: modelpolicy.DataModeRestricted,
        Region: "us-east-1",
    }, probe)
    if !errors.Is(err, ErrDisclosureNotConfirmed) {
        t.Fatalf("error = %v", err)
    }
}
```

- [ ] **Step 2: Write failing doctor-order tests** proving doctor remains read-only and never invokes a runtime model. It should report database, Google, provider credential/model metadata, data mode, and disclosure status using bounded messages. Restricted mode must render a failed disclosure check unless disabled is confirmed.
- [ ] **Step 3: Write direct-provider probe tests** around the SDK model metadata endpoints. OpenAI uses `openai.NewModelService(explicitOptions...).Get(ctx, modelID)`; Anthropic uses `anthropic.NewModelService(explicitOptions...).Get(ctx, modelID, anthropic.ModelGetParams{})`. The explicit options are the production environment, API key, and zero SDK retries. Cache the successful metadata result so a credential check followed by a model check does not duplicate network work.
- [ ] **Step 4: Run RED:** `go test ./internal/doctor` must fail.
- [ ] **Step 5: Implement** provider-neutral check names (`model.credentials`, `model.availability`, `model.disclosure`) and messages. Keep AWS STS plus Bedrock control-plane inspection for Bedrock; do not construct Bedrock Runtime in doctor.
- [ ] **Step 6: Implement safe direct probes** with SDK retries disabled and bounded auth/authorization/not-found/unavailable results. Do not return API response bodies.
- [ ] **Step 7: Run GREEN:** `go test ./internal/doctor ./internal/cli -run 'TestDoctor|TestRequireRestrictedDisclosure'` must pass.
- [ ] **Step 8: Commit:** `git add internal/doctor internal/cli && git commit -m "Enforce restricted model disclosure checks"`.

---

### Task 9: Select providers at the composition root and gate source access

**Files:**
- Create: `cmd/stacks/model.go`
- Create: `cmd/stacks/model_test.go`
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`
- Modify: `internal/cli/sync_test.go`
- Modify: `internal/cli/analyze_test.go`

**Interfaces:**
- Produces: `newModel(settings config.ModelSettings, recorder modeltelemetry.Recorder, tracer trace.Tracer) (extract.Model, error)`
- Produces: a lazy `newDoctorProviderProbe` selected by the same typed provider
- Enforces order: static validation -> restricted disclosure preflight -> Google/source construction -> storage/model construction -> command execution

- [ ] **Step 1: Write failing constructor tests** proving each provider returns only its corresponding adapter; missing credentials are rejected without appearing in errors; unsupported providers fail; and no custom base URL or fallback path exists.
- [ ] **Step 2: Write failing boundary-order tests** with call-recording fakes. For restricted Bedrock, assert `InvocationLogging` happens before Google OAuth/source open. For a failed gate, assert Google, PostgreSQL, and model runtime constructors are never called. For restricted OpenAI/Anthropic, assert no external constructor is called at all.

```go
func TestRestrictedSyncChecksDisclosureBeforeGoogle(t *testing.T) {
    calls := []string{}
    runtime := fakeRuntime{
        checkLogging: func(context.Context) (doctor.InvocationLoggingState, error) {
            calls = append(calls, "logging")
            return doctor.InvocationLoggingDisabled, nil
        },
        openGoogle: func(context.Context) (source.Source, error) {
            calls = append(calls, "google")
            return fakeSource{}, nil
        },
    }
    // Execute sync and assert calls starts with ["logging", "google"].
}
```

- [ ] **Step 3: Run RED:** `go test ./cmd/stacks ./internal/cli` must fail.
- [ ] **Step 4: Implement** a three-case switch in `cmd/stacks/model.go`; do not create a registry. Pass provider/data mode/region into ingestion and analysis services and shared telemetry.
- [ ] **Step 5: Refactor `pocCommandProvider`** so static `PoCSettings.Validate(command)` completes before any boundary construction. For `sync` and `analyze`, run `RequireRestrictedDisclosure` before calling any Google constructor or reading a source document. A cached result may be reused without a new provider disclosure, but the command still applies the pre-source restricted gate because determining cache identity requires source access.
- [ ] **Step 6: Build doctor dynamically** from the selected provider. Keep all probes lazy and close only resources actually opened.
- [ ] **Step 7: Run GREEN:** `go test ./cmd/stacks ./internal/cli ./internal/app` must pass.
- [ ] **Step 8: Commit:** `git add cmd/stacks internal/cli && git commit -m "Wire selectable model providers"`.

---

### Task 10: Document safe local operation and migrate ignored environment names

**Files:**
- Modify: `README.md`
- Modify: `.env.example`
- Modify locally but never stage: `.env`

- [ ] **Step 1: Add documentation tests or grep assertions** where practical so README command names and `.env.example` variables match the implemented configuration constants.
- [ ] **Step 2: Update README** with separate procedures for:
  - personal OpenAI and personal Anthropic operation;
  - restricted Bedrock operation and its fail-closed logging inspection;
  - `doctor` as non-invoking readiness inspection;
  - explicit paid runtime acceptance;
  - synthetic-only personal Drive content;
  - the difference between `store:false` and contractual Zero Data Retention;
  - still-unvalidated Bedrock quota, company Drive/IAM, and company-IP acceptance.
- [ ] **Step 3: Update ignored `.env` mechanically without displaying values.** Rename only the three old variable keys to their normalized names when present. Do not read the file into terminal output, copy it, stage it, or include it in a patch. Verify only with quiet exit-status checks such as `rg -q '^STACKS_MODEL_ID=' .env`; never print matching lines.
- [ ] **Step 4: Verify ignore protection:** `git check-ignore -q .env` must succeed and `git status --short` must not list `.env`.
- [ ] **Step 5: Run docs/config tests:** `go test ./internal/config ./cmd/stacks` must pass.
- [ ] **Step 6: Commit:** `git add README.md .env.example && git commit -m "Document personal model provider operation"`.

---

### Task 11: Run local PostgreSQL, deterministic, and bounded live acceptance

**Files:**
- Modify only if a concrete failure is reproduced test-first: the smallest relevant implementation/test file
- Do not add private or provider-returned fixtures to the repository

- [ ] **Step 1: Confirm scope before validation:** `git status --short --branch`, `git diff --check`, and `find db/migrations -maxdepth 1 -type f | sort` must show only the intended branch work and migrations through 00010.
- [ ] **Step 2: Start and migrate local PostgreSQL** with `make db-up`, `make db-migrate`, and `make db-status`. Expected result: pgvector PostgreSQL is reachable and every migration through 00010 is applied.
- [ ] **Step 3: Run every PostgreSQL-gated test** using the existing direnv-provided `STACKS_TEST_DATABASE_URL` and `STACKS_TEST_MIGRATION_DATABASE_URL`: `make test-integration`. Do not print either URL.
- [ ] **Step 4: Run doctor database checks** and confirm migration status through 00010. Provider checks may report missing quota or credentials only if the selected provider is not configured; do not invoke a model from doctor.
- [ ] **Step 5: Run deterministic verification:** `make fmt`, `make test`, `go test -race ./...`, `make staticcheck`, `make build`, `make db-status`, and `git diff --check`. Every command must succeed before claiming completion.
- [ ] **Step 6: Review migration behavior** by rerunning the integration cases for upgrade/backfill, extraction lease contention/reclamation, alias lifecycle, admission quarantine, stable Bedrock digest compatibility, provider cache separation, and retry/resume.
- [ ] **Step 7: Before paid calls, confirm** the configured personal Drive folder contains only synthetic test documents suitable for disclosure and set a one-document/one-provider bounded run. If this cannot be confirmed without viewing private contents, stop and report live acceptance as unvalidated.
- [ ] **Step 8: With explicit paid-call approval, run OpenAI acceptance** using `STACKS_DATA_MODE=personal`, `STACKS_MODEL_PROVIDER=openai`, and an explicit compatible `STACKS_MODEL_ID`: first `stacks doctor`, then a bounded `stacks sync`, then `stacks analyze`. Record only exit status, bounded counts, provider/model identifiers, and run IDs; never record source or model text.
- [ ] **Step 9: With explicit paid-call approval, repeat for Anthropic** using the same synthetic corpus and bounded workflow. Do not compare providers by silently reusing a cross-provider cache; provider-qualified digests must create separate derivations.
- [ ] **Step 10: If any concrete failure appears,** use `superpowers:systematic-debugging`, add a regression test that reproduces it, implement the smallest fix, and rerun Steps 2-9 as applicable.
- [ ] **Step 11: Perform an independent whole-diff review** against the approved design, focusing on disclosure ordering, secret/error redaction, exact schema submission, retry multiplication, Bedrock digest compatibility, migration safety, and false acceptance claims.
- [ ] **Step 12: Final report** exact commands and results in four separate groups: deterministic checks, local PostgreSQL/pgvector integration, synthetic personal Google/OpenAI/Anthropic acceptance, and still-unvalidated Bedrock/company-IP/company-Google gates. Do not push, open a PR, merge, deploy, or enable cloud logging.

---

## Completion criteria

- `sync` and `analyze` use Bedrock, OpenAI, or Anthropic only through `extract.Model` and explicit typed configuration.
- Restricted mode cannot reach Google or a direct provider and cannot reach Bedrock source input unless logging is confirmed disabled.
- OpenAI requests are stateless with `store:false`; Anthropic requests use native structured output; both submit the existing schema unchanged and have exact bounded retries.
- New extraction and completed-analysis rows carry provider and data-mode provenance; old rows remain auditable as Bedrock/legacy.
- Existing Bedrock digest fixtures remain byte-identical; direct-provider digests cannot collide across providers.
- Migration status and every PostgreSQL-gated invariant pass through 00010.
- Full tests, race tests, Staticcheck, build, formatting, and migration checks pass.
- The final report never equates personal-provider acceptance with Bedrock quota, company-IP approval, or company Google Drive acceptance.
