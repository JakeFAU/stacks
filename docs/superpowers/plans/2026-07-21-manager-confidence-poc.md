# Manager Confidence Signal PoC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local CLI that ingests tabbed Gemini meeting Docs from one Drive folder, resolves one employee-manager pair, and reports cautious transcript-cited changes in observable interaction signals.

**Architecture:** PostgreSQL stores immutable document/tab versions, evidence, append-only identity decisions, temporal observations, and analysis provenance. Google Drive/Docs and Amazon Bedrock are narrow I/O boundaries; deterministic Go code owns tab classification, resolution state, citation validation, chronology, admission rules, and report status.

**Tech Stack:** Go 1.26, standard-library CLI, PostgreSQL 18 with pgvector, pgx v5, Google Drive/Docs APIs with OAuth2, AWS SDK for Go v2 Bedrock Converse, Zap, OpenTelemetry.

## Global Constraints

- Read only direct Google Docs children of the configured folder; do not recurse into folders or follow out-of-scope links.
- Always set `includeTabsContent=true` and recursively traverse child tabs.
- Preserve transcript, Gemini notes, and other tabs separately; a signal requires transcript evidence.
- Auto-resolve only accepted emails or one unique accepted alias; every other match remains reviewable.
- Analyze one configured pair without embedding employee/manager roles in the entity schema.
- Validate Bedrock region, model ID, prompt versions, maximum output tokens, and retry limits.
- Treat model output as untrusted until schema, enums, references, dates, and exact citations validate.
- Never log transcript text, prompts, model output, names, emails, Drive titles, or Drive URLs.
- Keep valid time separate from recorded time; use synthetic private-data-free tests.
- Do not add embeddings, graph databases, a web UI, concepts, org discovery, or generic traversal.
- Follow red-green-refactor and run `make fmt`, `make test`, and `make staticcheck` before completion.

## File map

| Path | Responsibility |
| --- | --- |
| `internal/app/execute.go`, `internal/cli/*.go` | Command routing, dependency wiring, argument parsing, and rendering |
| `internal/config/poc.go` | Command-specific PoC configuration validation |
| `internal/source/source.go`, `internal/source/drive/*.go` | Provider-neutral source contracts, OAuth, direct-folder discovery, and tabbed Docs |
| `internal/knowledge/document.go` | Immutable document/tab versions and evidence spans |
| `internal/entity/*.go` | Entities, aliases, mentions, proposals, resolver, and decisions |
| `internal/extract/*.go`, `internal/extract/prompts/*.txt` | Structured schemas, prompts, and exact citation validation |
| `internal/bedrock/client.go` | Converse structured-output adapter and retry classification |
| `internal/ingest/service.go` | Idempotent version and extraction orchestration |
| `internal/analysis/*.go` | Finite signals, chronology, admission policy, and pair report |
| `internal/storage/*.go` | pgx repositories and visible transactions |
| `internal/doctor/service.go` | Read-only database, Google, AWS, model, and logging checks |
| `db/migrations/00002_manager_confidence_poc.sql` | Forward-only PoC schema and invariants |
| `README.md`, `.env.example`, `Makefile` | Exact workflow, non-secret config names, and verification targets |

---

### Task 1: Command routing and typed PoC configuration

**Files:**
- Create: `internal/app/execute.go`, `internal/app/execute_test.go`
- Create: `internal/cli/runner.go`, `internal/cli/runner_test.go`
- Create: `internal/config/poc.go`
- Modify: `cmd/stacks/main.go`, `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces: `app.Execute(ctx context.Context, args []string, settings config.Settings, runtime Runtime, stdout, stderr io.Writer) error`
- Produces: `cli.Runner.Run(ctx context.Context, args []string) error`
- Produces: `PoCSettings.Validate(command config.Command) error`

- [ ] **Step 1: Write failing tests** proving no args route to `serve`, unknown commands fail, missing PoC settings do not break `serve`, `sync` requires all corpus/disclosure values, and normalized transcript/notes title sets cannot overlap.

```go
func TestRunnerDefaultsToServe(t *testing.T) {
    called := false
    runner := Runner{Commands: map[string]Command{"serve": CommandFunc(func(context.Context, []string) error { called = true; return nil })}}
    if err := runner.Run(context.Background(), nil); err != nil { t.Fatal(err) }
    if !called { t.Fatal("serve was not called") }
}
```

- [ ] **Step 2: Run RED:** `go test ./internal/config ./internal/cli ./internal/app` must fail because the new contracts do not exist.
- [ ] **Step 3: Implement** `PoCSettings` fields for database URL, folder ID, OAuth files, transcript/notes title sets, AWS profile/region/model, max tokens/attempts, prompt versions, and pair entity IDs. Default only max attempts (`5`) and prompt versions (`extract-v1`, `analyze-v1`); require all other command-specific values.
- [ ] **Step 4: Implement** a standard-library command router. Keep `cmd/stacks` limited to config, observability, signals, `app.Execute`, shutdown, and exit status. Preserve no-argument HTTP server behavior.
- [ ] **Step 5: Run GREEN:** `go test ./internal/config ./internal/cli ./internal/app ./internal/httpapi` must pass.
- [ ] **Step 6: Commit:** `git add cmd/stacks internal/app internal/cli internal/config && git commit -m "Add Stacks command routing"`.

---

### Task 2: Tab-aware source and immutable evidence

**Files:**
- Create: `internal/source/source.go`
- Create: `internal/source/drive/tabs.go`, `internal/source/drive/tabs_test.go`
- Create: `internal/knowledge/document.go`, `internal/knowledge/document_test.go`

**Interfaces:**
- Produces: `source.Source.List/Get`, `source.Document`, `source.Tab`, and `source.TabRole`
- Produces: `drive.FlattenTabs(roots []*docs.Tab, classifier TabClassifier) ([]source.Tab, error)`
- Produces: `knowledge.NewDocumentVersion` and `knowledge.NewEvidenceSpan`

- [ ] **Step 1: Write failing tab tests** where notes are first and transcript second, transcript is nested, two transcript titles are ambiguous, and no transcript exists.

```go
func TestFlattenTabsFindsTranscriptAfterNotes(t *testing.T) {
    roots := []*docs.Tab{tab("t.notes", "Meeting notes", "summary"), tab("t.transcript", "Transcript", "Alex: synthetic words")}
    got, err := FlattenTabs(roots, NewTabClassifier([]string{"transcript"}, []string{"meeting notes"}))
    if err != nil { t.Fatal(err) }
    if got[1].Role != source.TabRoleTranscript { t.Fatalf("role = %v", got[1].Role) }
}
```

- [ ] **Step 2: Run RED:** `go test ./internal/source/drive` must fail.
- [ ] **Step 3: Implement** recursive child-tab traversal, paragraph/table/TOC text extraction, deterministic UI order, explicit title classification, and exactly-one-transcript validation. Retain tab ID/title/parent/path/order/role/text.
- [ ] **Step 4: Write failing evidence tests** proving tab order/content changes affect SHA-256 document identity, invalid offsets fail, and exact quote mismatch fails.
- [ ] **Step 5: Run RED:** `go test ./internal/knowledge -run 'TestDocumentVersion|TestEvidenceSpan'` must fail.
- [ ] **Step 6: Implement** length-prefixed ordered hashing and UTF-8 byte-offset validation. Citations retain provider document ID, immutable tab ID, and exact local text.
- [ ] **Step 7: Add dependencies and run GREEN:** `go get google.golang.org/api@latest golang.org/x/oauth2@latest && go mod tidy && go test ./internal/source/drive ./internal/knowledge`.
- [ ] **Step 8: Commit:** `git add go.mod go.sum internal/source internal/knowledge && git commit -m "Define tabbed meeting evidence"`.

---

### Task 3: PostgreSQL schema and repositories

**Files:**
- Create: `db/migrations/00002_manager_confidence_poc.sql`
- Create: `internal/storage/postgres.go`, `documents.go`, `entities.go`, `analysis.go`, `integration_test.go`
- Modify: `Makefile`

**Interfaces:**
- Produces: `storage.Open(ctx, databaseURL) (*pgxpool.Pool, error)`
- Produces: transaction-scoped repositories consumed by ingestion, review, and analysis

- [ ] **Step 1: Write the forward-only migration** for source documents, document/tab versions, evidence spans, entities/aliases, mentions, proposals/candidates/decisions, observations/evidence, signals, analysis runs, and analysis inputs.
- [ ] **Step 2: Encode invariants** with unique/check/foreign-key constraints: provider document identity; document digest identity; tab identity per version; valid spans; finite status/category/direction enums; one effective non-superseded decision; one analysis input digest. Do not add a destructive Down migration.
- [ ] **Step 3: Apply schema:** `make db-up && make db-migrate && make db-status` must show migrations `00001` and `00002` applied.
- [ ] **Step 4: Write failing integration tests** gated by `STACKS_TEST_DATABASE_URL` for idempotent version insertion, append-only correction with one effective decision, transaction rollback, and analysis deduplication.

```go
func TestPutDocumentVersionIsIdempotent(t *testing.T) { /* insert twice; same ID, created=false second time */ }
func TestCorrectionLeavesOneEffectiveDecision(t *testing.T) { /* two historical, one effective */ }
```

- [ ] **Step 5: Run RED:** `STACKS_TEST_DATABASE_URL="$STACKS_DATABASE_URL" go test ./internal/storage` must fail.
- [ ] **Step 6: Implement** pgx v5 repositories returning domain values, with transaction ownership at document completion, resolution decision, and analysis completion boundaries. Errors include stable IDs, never private text.
- [ ] **Step 7: Add `test-integration`** requiring `STACKS_TEST_DATABASE_URL`; run `make test` and the explicit integration target to GREEN.
- [ ] **Step 8: Commit:** `git add db/migrations internal/storage Makefile go.mod go.sum && git commit -m "Persist meeting graph evidence"`.

---

### Task 4: Conservative people resolution and CLI review

**Files:**
- Create: `internal/entity/entity.go`, `entity_test.go`, `resolver.go`, `resolver_test.go`
- Create: `internal/cli/entities.go`, `entities_test.go`, `review.go`, `review_test.go`
- Modify: `internal/storage/entities.go`

**Interfaces:**
- Produces: `entity.Resolver.Resolve(Mention, []EntitySnapshot) Resolution`
- Produces: `ReviewService.List/Show/Accept/Reject/Create`

- [ ] **Step 1: Write failing resolver tests** for accepted exact email, unique accepted alias, duplicate alias ambiguity, ranked guess stability, and no candidate. Assert a best guess never populates accepted `EntityID`.
- [ ] **Step 2: Run RED:** `go test ./internal/entity` must fail.
- [ ] **Step 3: Implement** kind-neutral entities with `KindPerson`, separate Unicode-aware name/email normalization, deterministic ranking by confidence then entity ID, and auto-resolution from accepted identifiers only.
- [ ] **Step 4: Write failing review/CLI tests** for explicit proposal IDs, stale/superseded failures, append-only accept/reject/create/correct, and private output on stdout but not logger/telemetry.
- [ ] **Step 5: Implement** exactly these commands:

```text
stacks entities list
stacks entities show <entity-id>
stacks review list
stacks review show <proposal-id>
stacks review accept <proposal-id> <entity-id>
stacks review reject <proposal-id>
stacks review create <proposal-id> --name <name> [--email <email>]
stacks review correct <effective-decision-id> <entity-id>
```

`correct` appends a decision that supersedes the named effective decision; it never updates or deletes the prior row.

- [ ] **Step 6: Run GREEN:** `go test ./internal/entity ./internal/cli ./internal/storage` must pass.
- [ ] **Step 7: Commit:** `git add internal/entity internal/cli internal/storage && git commit -m "Add reviewable person resolution"`.

---

### Task 5: Google OAuth and tab-aware Drive client

**Files:**
- Create: `internal/source/drive/oauth.go`, `oauth_test.go`, `client.go`, `client_test.go`
- Create: `internal/cli/auth.go`, `auth_test.go`

**Interfaces:**
- Produces: `Authorizer.Authorize(ctx) error`
- Produces: `Client.List(ctx, folderID)` and `Client.Get(ctx, documentID)` implementing `source.Source`

- [ ] **Step 1: Write failing HTTP-transport tests** asserting the Drive query contains the exact parent, `trashed=false`, and Google-Doc MIME type, while Docs `Get` sends `includeTabsContent=true`.
- [ ] **Step 2: Run RED:** `go test ./internal/source/drive -run 'TestList|TestGet'` must fail.
- [ ] **Step 3: Implement** minimal Drive fields and `Documents.Get(id).IncludeTabsContent(true).Context(ctx).Do()`, immediately converting Google types into source contracts.
- [ ] **Step 4: Write failing OAuth tests** for read-only scopes, random-state mismatch, owner-only `0600` token writes, atomic replacement, redacted errors, and no auth mutation from doctor.
- [ ] **Step 5: Implement** `stacks auth google` with external client/token paths, ephemeral `127.0.0.1` callback, cryptographically random state, printed authorization URL, matching-code exchange, and no service-account/domain-wide-delegation path.
- [ ] **Step 6: Run GREEN:** `go test ./internal/source/drive ./internal/cli` must pass.
- [ ] **Step 7: Commit:** `git add internal/source/drive internal/cli && git commit -m "Read tabbed Gemini meeting Docs"`.

---

### Task 6: Bedrock structured extraction and validation

**Files:**
- Create: `internal/extract/schema.go`, `schema_test.go`, `validate.go`, `validate_test.go`
- Create: `internal/extract/prompts/extract-v1.txt`, `analyze-v1.txt`
- Create: `internal/bedrock/client.go`, `client_test.go`

**Interfaces:**
- Produces: `extract.Model.Generate(ctx, Request) (Response, error)`
- Produces: `extract.ValidateExtraction(SubmittedText, ExtractionOutput) error`
- Produces: Bedrock implementation using Converse structured output

- [ ] **Step 1: Write failing validation tests** for invented/mismatched offsets, notes-only signal evidence, unknown categories/directions, unknown references, invalid dates, and non-finite confidence.

```go
func TestValidateExtractionRejectsInventedCitation(t *testing.T) { /* exact quote differs at offsets */ }
func TestValidateExtractionRejectsNotesOnlySignal(t *testing.T) { /* every citation is notes */ }
```

- [ ] **Step 2: Run RED:** `go test ./internal/extract` must fail.
- [ ] **Step 3: Implement** embedded versioned prompts, JSON Schemas with `additionalProperties:false`, required IDs/offsets, finite enums, and conversion only after validation.
- [ ] **Step 4: Write failing Bedrock tests** asserting configured model ID, explicit `MaxTokens`, JSON Schema `OutputConfig`, token/latency capture, no private request metadata, bounded throttling retries, and no AccessDenied retry.
- [ ] **Step 5: Implement** AWS SDK v2 Converse with `InferenceConfig.MaxTokens`, `OutputConfig.TextFormat` JSON Schema, supported stop-reason validation, private-output isolation, and adaptive retry classification.
- [ ] **Step 6: Add dependencies and run GREEN:** `go get github.com/aws/aws-sdk-go-v2/config@latest github.com/aws/aws-sdk-go-v2/service/bedrock@latest github.com/aws/aws-sdk-go-v2/service/bedrockruntime@latest && go mod tidy && go test ./internal/extract ./internal/bedrock`.
- [ ] **Step 7: Commit:** `git add go.mod go.sum internal/extract internal/bedrock && git commit -m "Validate Bedrock meeting extraction"`.

---

### Task 7: Idempotent sync vertical slice

**Files:**
- Create: `internal/ingest/service.go`, `service_test.go`
- Create: `internal/cli/sync.go`, `sync_test.go`
- Modify: `internal/app/execute.go`, `internal/storage/documents.go`, `internal/storage/entities.go`

**Interfaces:**
- Produces: `ingest.Service.Sync(ctx) (Summary, error)`
- Consumes: source, model, resolver, and repository interfaces defined beside the service

- [ ] **Step 1: Write failing orchestration tests** proving unchanged versions make zero model calls; one changed tab creates one version; one malformed document does not discard another; retries resume durable state; unknown meeting time stays unknown; repeated sync duplicates nothing.

```go
func TestSyncSkipsExtractionForUnchangedVersion(t *testing.T) { /* model calls == 0 */ }
func TestSyncContinuesAfterOneDocumentFails(t *testing.T) { /* completed=1, failed=1 */ }
```

- [ ] **Step 2: Run RED:** `go test ./internal/ingest` must fail.
- [ ] **Step 3: Implement** list/fetch/classify/digest, create pending version, submit separately labeled transcript/notes, validate, atomically persist evidence/proposals/observations, then mark complete. Persist only bounded failure code and retry count.
- [ ] **Step 4: Write failing CLI tests** proving per-document outcomes are only `unchanged`, `completed`, `incomplete`, or `failed`, with operational IDs and counts but no titles/names.
- [ ] **Step 5: Implement and wire** `stacks sync`, one meaningful ingestion span, and bounded decision events.
- [ ] **Step 6: Run GREEN:** `go test ./internal/ingest ./internal/cli ./internal/app && make test` must pass.
- [ ] **Step 7: Commit:** `git add internal/ingest internal/cli internal/app internal/storage && git commit -m "Sync meeting evidence idempotently"`.

---

### Task 8: Temporal pair analysis and cited report

**Files:**
- Create: `internal/analysis/signal.go`, `signal_test.go`, `service.go`, `service_test.go`
- Create: `internal/cli/analyze.go`, `analyze_test.go`
- Modify: `internal/storage/analysis.go`, `internal/app/execute.go`

**Interfaces:**
- Produces: `analysis.Service.Analyze(ctx, employeeID, managerID string) (Report, error)`
- Produces: `analysis.AdmitConclusion(AdmissionInput) ReportStatus`

- [ ] **Step 1: Write failing admission tests** for fewer than two dated meetings, later weakening with earlier comparison, pending pair identities, conflicts, unknown time, and unsupported decline proposals.

```go
func TestAdmitConclusionRequiresTwoDatedMeetings(t *testing.T) { /* insufficient */ }
func TestAdmitConclusionAcceptsCitedLaterWeakening(t *testing.T) { /* possible decline */ }
```

- [ ] **Step 2: Run RED:** `go test ./internal/analysis` must fail.
- [ ] **Step 3: Implement** typed signal categories/directions, stable valid-time ordering, separate unknown-time section, confidence-neutral conflict preservation, and structural conclusion admission.
- [ ] **Step 4: Write failing service tests** for accepted pair requirement, exact input digest, cached identical run, correction-driven new run with old provenance retained, counterevidence, and transcript-only inputs.
- [ ] **Step 5: Implement** compact validated-signal synthesis, persisted ordered inputs/model/prompt/result/citations, and `stacks analyze` rendering conclusion, limitations, chronology, counterevidence, gaps, and tab URLs.
- [ ] **Step 6: Run GREEN:** `go test ./internal/analysis ./internal/cli ./internal/storage && make test` must pass.
- [ ] **Step 7: Commit:** `git add internal/analysis internal/cli internal/storage internal/app && git commit -m "Analyze manager interaction signals"`.

---

### Task 9: Doctor, documentation, and live acceptance

**Files:**
- Create: `internal/doctor/service.go`, `service_test.go`
- Create: `internal/cli/doctor.go`, `doctor_test.go`
- Modify: `internal/app/execute.go`, `README.md`, `.env.example`, `Makefile`

**Interfaces:**
- Produces: `doctor.Service.Check(ctx) Report`
- Produces: `stacks doctor` without auth, extraction, or persistence side effects

- [ ] **Step 1: Write failing doctor tests** for database, Google token/folder/tab roles, AWS credentials, model availability, and invocation-logging states `disabled`, `enabled`, or `unknown`. Assert mutating fake counters remain zero.
- [ ] **Step 2: Run RED:** `go test ./internal/doctor ./internal/cli -run TestDoctor` must fail.
- [ ] **Step 3: Implement** bounded read-only checks. AccessDenied while inspecting invocation logging yields `unknown`, never a false safe result. Expired Google auth directs to `stacks auth google`.
- [ ] **Step 4: Document** all non-secret variables and exact workflow: database up/migrate, auth, doctor, sync, review, analyze. Explain tabs, external OAuth files, Bedrock disclosure/logging, bounded conclusions, corrections, and observable-signal—not hidden-state—scope.
- [ ] **Step 5: Run full verification:** `make fmt && make test && make staticcheck && make build && git diff --check` must all exit 0.
- [ ] **Step 6: Run database verification:** `make db-up && make db-migrate && STACKS_TEST_DATABASE_URL="$STACKS_DATABASE_URL" make test-integration` must pass.
- [ ] **Step 7: Run live acceptance when credentials exist:** authenticate, doctor, sync at least two tabbed Gemini Docs, repeat sync with no new versions/model work, resolve the pair, inspect every analysis citation, correct one identity, and prove a new analysis retains old provenance. Never copy private text into Git or reports.
- [ ] **Step 8: If credentials fail, stop honestly** and report test-complete but not live-validated.
- [ ] **Step 9: Commit:** `git add internal/doctor internal/cli internal/app README.md .env.example Makefile && git commit -m "Document manager signal workflow"`.

---

## Final review gate

- Map every approved-spec requirement to a completed task.
- Confirm no real transcript, name, email, Drive URL, prompt, or model output entered Git or telemetry.
- Review the full diff for accidental RAG, generic graph, web, or concept scope.
- Report unit, static analysis, build, PostgreSQL integration, Google live, and Bedrock live results separately.
- Do not push, open a PR, merge, deploy, or enable cloud logging without explicit user instruction.
