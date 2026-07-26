# Canonical PostgreSQL Reset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the disposable manager-confidence PoC database with fresh embedded canonical core and optional directory schemas, cut every application writer and reader to those contracts, and retire the legacy PostgreSQL path.

**Architecture:** Add `github.com/JakeFAU/stacks/adapters/postgres` as the durable adapter module, with an embedded scoped migration engine and canonical repositories over `stacks_core` plus optional `stacks_directory`. Root application packages retain orchestration and provider policy, translate extraction output into canonical core values, and switch composition once; no runtime path dual-writes old and new schemas. Manager confidence remains a temporary read-only query use case over canonical observations until Plan D replaces it.

**Tech Stack:** Go 1.26.0, PostgreSQL 18 (`pgvector/pgvector:0.8.5-pg18-trixie` image without installing the vector extension), pgx v5.10.0, `embed.FS`, Docker Compose/OrbStack, Zap, OpenTelemetry, Go's standard `testing` package, and repository-pinned Staticcheck 2026.1.

## Global Constraints

- Work in the main checkout at `/Users/jacob/dev/personal/stacks` on a dedicated feature branch created from current `main`; preserve unrelated work.
- Follow `AGENTS.md` and `docs/superpowers/specs/2026-07-25-canonical-postgres-reset-design.md`.
- Use a fresh implementation subagent for each task, a specification review and code-quality review at every task gate, and an independent whole-branch review after final verification.
- Use test-first red/green cycles. Every task ends in a buildable, independently reviewable commit.
- The one-time transition is an explicitly confirmed destructive reset of the repository's local Compose PostgreSQL volume. There is no upgrade or row-copy path from migrations `00001` through `00012`.
- Core and directory use independent embedded manifests and ledgers. There is no manager-confidence migration scope.
- Core IDs are opaque trimmed text. Do not impose UUID parsing or UUID columns on canonical domain identifiers.
- Normalize every persisted timestamp to UTC microsecond precision before digesting, comparing, or writing it. PostgreSQL must never truncate canonical values silently.
- Immutable document, source-revision observation, evidence, entity, mention, observation, identity-decision, and admission-decision payloads are never rewritten to express new state.
- Valid time and recorded time remain independent. Never substitute ingestion or provider-modified time for unknown source-valid time.
- Identity decisions and admission decisions are append-only. Exactly one effective decision may exist for a target; corrections explicitly supersede the effective predecessor.
- A unique exact accepted work-email match may auto-resolve. Name-only matches remain review candidates. Reviewer decisions remain authoritative until explicitly superseded.
- Directory data is additive. Disabled, absent, denied, throttled, stale, or unavailable directory state must not block core ingestion, identity review, or querying.
- Manager confidence is application query policy, never a table, schema, migration, repository DTO, or installation option.
- Do not invent a generic statement-attribution contract in Plan C. Attributed statements remain validated extraction intermediates used to ground interaction evidence until a separate core design earns speaker attribution.
- Drive is a source adapter. Bedrock, Anthropic, and OpenAI are runtime model adapters. Migration, reset, database-command, and doctor database-check tests must construct or invoke none of them.
- The PostgreSQL adapter returns bounded errors and emits no logs or telemetry. Root application owners record meaningful migration, transaction, lease, directory, and reset spans with low-cardinality fields only.
- Do not install the PostgreSQL `vector` extension until a vector-backed storage and query contract exists.
- Use synthetic fixtures and reserved domains only. Never read, print, copy, log, or commit `.env`, `.envrc`, `.secrets/**`, credentials, tokens, private document contents, prompts containing private text, or model output.
- Keep `.env.example` current with safe placeholders. Real values remain only in ignored `.env`.
- Do not deploy, enable cloud logging, push, publish, open a pull request, merge, tag, or change external repository settings without explicit user approval.

---

## File Map

### New core files

- `core/timepoint/timepoint.go`: the single UTC microsecond normalization contract.
- `core/timepoint/timepoint_test.go`: precision, timezone, monotonic-state, and zero-value tests.
- `core/internal/canonicalhash/encoder.go`: length-prefixed canonical digest encoding.
- `core/internal/canonicalhash/encoder_test.go`: collision-boundary and stable-order tests.
- `core/observation/digest.go`: versioned full semantic observation digest.
- `core/identity/authority.go`: durable entities, mentions, proposals, candidates, append-only decisions, and alias assertions.
- `core/identity/authority_test.go`: identity authority and supersession invariants.
- `core/admission/admission.go`: generic append-only admission authority.
- `core/admission/admission_test.go`: target, outcome, reason, and supersession invariants.

### New PostgreSQL adapter module

- `adapters/postgres/go.mod`: isolated adapter dependencies on core and pgx.
- `adapters/postgres/doc.go`: package contract and privacy boundary.
- `adapters/postgres/dependency_test.go`: forbidden-import boundary.
- `adapters/postgres/database.go`: pool ownership and health validation.
- `adapters/postgres/transaction.go`: explicit transaction boundary.
- `adapters/postgres/errors.go`: bounded conflict, not-found, lease, and corrupt-state errors.
- `adapters/postgres/migration/manifest.go`: embedded files, checksums, ownership, grants, and manifest validation.
- `adapters/postgres/migration/migrator.go`: independent ledgers, fixed advisory lock, transactional apply, and exact retry behavior.
- `adapters/postgres/migration/status.go`: structured per-scope status.
- `adapters/postgres/migration/fingerprint.go`: PostgreSQL 18 catalog normalization and semantic fingerprints.
- `adapters/postgres/migration/*_test.go`: pure manifest, migrator, status, and fingerprint tests.
- `adapters/postgres/postgrestest/database.go`: test-only exact temporary-database lifecycle shared by adapter and root integration tests.
- `adapters/postgres/coremigrations/manifest.go`: required core manifest and expected fingerprint.
- `adapters/postgres/coremigrations/migrations/00001_documents_evidence.sql`: canonical source, version, provider-revision observation, section, and evidence schema.
- `adapters/postgres/coremigrations/migrations/00002_identity_admission.sql`: canonical identity and generic admission schema.
- `adapters/postgres/coremigrations/migrations/00003_extraction_observations.sql`: extraction attempts, canonical observations, and evidence roles.
- `adapters/postgres/directorymigrations/manifest.go`: optional directory manifest and expected fingerprint.
- `adapters/postgres/directorymigrations/migrations/00001_directory.sql`: optional snapshot, profile, lookup, and adapter-link state.
- `adapters/postgres/documents.go`: immutable document/version/section/evidence persistence.
- `adapters/postgres/identity.go`: identity proposals, candidates, decisions, aliases, and effective authority.
- `adapters/postgres/extraction.go`: extraction-run identity and attempt lease lifecycle.
- `adapters/postgres/observation.go`: canonical observation codec and persistence.
- `adapters/postgres/admission.go`: generic append-only admission persistence.
- `adapters/postgres/query.go`: generic admitted relationship-observation projection for downstream query use cases.
- `adapters/postgres/directory.go`: optional directory-scope persistence using adapter-neutral inputs.
- Corresponding `*_test.go` and `*_integration_test.go` files for every repository.

### New or split application files

- `internal/ingest/canonical.go`: local-key draft terms/evidence and conversion to canonical core values.
- `internal/ingest/canonical_test.go`: statement and manager-interaction mapping tests.
- `internal/ingest/postgres.go`: consumer-owned atomic ingestion repository over the PostgreSQL adapter.
- `internal/analysis/observation.go`: versioned manager-interaction predicate codec and canonical observation-to-signal translation.
- `internal/analysis/observation_test.go`: predicate, evidence-role, chronology, and bounded-output tests.
- `internal/analysis/postgres.go`: temporary read-only manager-confidence repository over the adapter's generic observation projection.
- `internal/directory/postgres.go`: mapping between the optional directory service contract and adapter-neutral PostgreSQL directory values.
- `internal/localdb/reset.go`: guarded local Compose PostgreSQL reset.
- `internal/localdb/reset_test.go`: destructive-target validation with a fake command runner.
- `internal/cli/database.go`: `db-migrate`, `db-status`, and `db-reset` commands.
- `internal/cli/database_test.go`: bounded command output and failure tests.

### Modified manifests and composition

- `go.work`, `modules.txt`, root `go.mod`, and `go.sum`: register and consume the PostgreSQL adapter module.
- `Makefile`: remove Goose, add Stacks-owned database commands, and keep all-module verification.
- `db/init/010-create-application-role.sh`: bootstrap the application role only; migrations own schemas and grants.
- `compose.yaml`: retain PostgreSQL 18 and the single local data volume without installing vector.
- `internal/ingest/service.go`, `validate.go`, and tests: canonical completion only.
- `internal/analysis/service.go`, `signal.go`, and tests: on-demand canonical query without manager-specific durable cache.
- `internal/directory/service.go` and tests: preserve fail-soft optional behavior through the new store.
- `internal/config/config.go`, replacement for `internal/config/poc.go`, and tests: application/database/use-case configuration.
- `internal/doctor/providers.go`, `service.go`, and tests: structured read-only migration scope status.
- `internal/cli/storage.go`, `analyze.go`, `doctor.go`, and tests: canonical repository and status adapters.
- `cmd/stacks/main.go` and tests: lazy canonical composition with no provider construction in database commands.
- `.env.example`, `README.md`, and `.github/workflows/ci.yml`: destructive reset, scopes, commands, and verification truth.

### Deleted only after canonical cutover acceptance

- `db/migrations/00001_enable_vector.sql` through `db/migrations/00012_google_directory_identity.sql`.
- The complete `internal/storage/` directory:
  `analysis.go`, `analysis_test.go`, `completed_write_set.go`, `directory.go`,
  `directory_test.go`, `documents.go`, `documents_test.go`, `entities.go`,
  `entities_test.go`, `graph.go`, `graph_test.go`, `integration_test.go`,
  `migration_test.go`, `observation_codec.go`,
  `observation_codec_test.go`,
  `observation_compatibility_integration_test.go`,
  `observation_postgres.go`,
  `observation_postgres_integration_test.go`, and `postgres.go`.
- Legacy migration-upgrade, UUID-shape, digest-v1, dual-evidence, and compatibility tests.
- Core `LegacyUncited`, `LegacyUnversioned`, `ConfidenceUnspecifiedLegacy`, `NewLegacyConfidence`, and temporal unresolved-legacy state after no caller needs them.

---

### Task 1: Canonicalize Core Time and Version Every Durable Digest

**Files:**
- Create: `core/timepoint/timepoint.go`
- Create: `core/timepoint/timepoint_test.go`
- Create: `core/internal/canonicalhash/encoder.go`
- Create: `core/internal/canonicalhash/encoder_test.go`
- Create: `core/evidence/revision.go`
- Create: `core/evidence/revision_test.go`
- Create: `core/observation/digest.go`
- Modify: `core/evidence/document.go`
- Modify: `core/evidence/document_test.go`
- Modify: `core/evidence/evidence.go`
- Modify: `core/evidence/evidence_test.go`
- Modify: `core/observation/time.go`
- Modify: `core/observation/time_test.go`
- Modify: `core/observation/observation.go`
- Modify: `core/observation/observation_test.go`
- Modify: `core/temporal/plan.go`
- Modify: `core/temporal/plan_test.go`
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/ingest/validate_test.go`
- Modify: `internal/storage/documents.go`
- Modify: `internal/storage/documents_test.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/storage/observation_compatibility_integration_test.go`

**Interfaces:**
- Consumes: existing provider-neutral core evidence and observation constructors.
- Produces:

```go
package timepoint

const Precision = time.Microsecond

func Normalize(value time.Time) time.Time
func IsCanonical(value time.Time) bool
```

```go
const (
	DocumentDigestVersion     = "stacks.document.v3.utc-microsecond"
	SourceRevisionDigestVersion = "stacks.source-revision.v1"
	EvidenceIDVersion         = "stacks.evidence-id.v1.source-span"
	EvidenceSpanDigestVersion = "stacks.evidence-span.v1"
	ObservationDigestVersion  = "stacks.observation.v2.canonical"
)

type SourceRevisionObservationInput struct {
	Provider              string
	ProviderDocumentID    string
	DocumentDigestVersion string
	DocumentDigest        ContentDigest
	ProviderVersion       string
	ProviderRevision      string
	FirstRecordedAt       time.Time
}

func (DocumentVersion) DigestVersion() string
func NewSourceRevisionObservation(SourceRevisionObservationInput) (SourceRevisionObservation, error)
func (SourceRevisionObservation) ID() string
func (SourceRevisionObservation) Digest() ContentDigest
func (SourceRevisionObservation) DigestVersion() string
func SourceSpanID(DocumentVersion, string, int, int) EvidenceID
func (EvidenceSpan) ID() EvidenceID
func (EvidenceSpan) Locator() string
func (EvidenceSpan) RecordedAt() time.Time
func (EvidenceSpan) Digest() ContentDigest
func (EvidenceSpan) DigestVersion() string
func (Observation) Digest() evidence.ContentDigest
func (Observation) DigestVersion() string
```

Extend `EvidenceSpanInput` with `RecordedAt time.Time`. `SourceSpanID` derives
the opaque evidence ID from the named ID version, source provider/document
identity, document digest version/value, section ID, and UTF-8 byte offsets;
it contains no extraction run, prompt, model, citation-local ID, or recorded
time. `NewEvidenceSpan` uses that derivation after validating the source
range. The canonical hash encoder exposes typed `String`, `Bytes`, `Uint64`,
`Bool`, and `Time` methods and always prefixes the digest preimage with its
named version.

`SourceRevisionObservationInput` carries provider, provider document ID,
document digest version/value, provider version, optional provider revision,
and first recorded time. Its opaque ID excludes first recorded time; its
versioned digest covers every field including the normalized time. The
canonical PostgreSQL repository constructs it only after loading the stable
content version's first recorded time, so exact source-revision retries have
the same ID and payload. It is the canonical append-only provenance value used
by the new adapter. `DocumentVersion.ProviderRevision` and the matching input
field remain only as a transitional legacy caller bridge until Task 11 deletes
them.

- [ ] **Step 1: Write failing canonical-time tests**

Add:

```go
func TestNormalizeUsesUTCMicrosecondPrecision(t *testing.T)
func TestNormalizeRemovesMonotonicState(t *testing.T)
func TestIsCanonicalRejectsLocalAndSubMicrosecondValues(t *testing.T)
func TestEveryCoreConstructorNormalizesPersistedTimes(t *testing.T)
```

Use a non-UTC value with nanoseconds `123456789` and require `123456000` in
UTC from document modified/source/recorded time, source-revision first
recorded time, evidence recorded time, observation valid/recorded time, and
temporal plan cutoffs. Task 2 adds the
same boundary test for the identity and admission types introduced there.

- [ ] **Step 2: Run the time tests and verify red**

Run:

```bash
(cd core && GOWORK=off go test ./timepoint ./evidence ./observation ./temporal \
  -run 'Normalize|Canonical|Microsecond' -count=1)
```

Expected: FAIL because `core/timepoint` and canonical normalization do not exist.

- [ ] **Step 3: Implement the single timepoint policy and apply it at constructors**

`Normalize` returns zero unchanged; otherwise it calls `value.Round(0).UTC().Truncate(Precision)`. `IsCanonical` requires equality with `Normalize`, UTC location, and no monotonic representation. Every constructor normalizes before copying, digesting, comparing, or exposing the value.

- [ ] **Step 4: Write failing digest-version and mutation tests**

Add:

```go
func TestDocumentVersionDigestV3Golden(t *testing.T)
func TestDocumentDigestIgnoresRecordedAtAndProviderRevision(t *testing.T)
func TestEvidenceSpanDigestCoversExactSourceRangeAndRecordedTime(t *testing.T)
func TestSourceRevisionDigestSeparatesProviderRevisionFromContentVersion(t *testing.T)
func TestObservationDigestCoversCompleteSemanticPayload(t *testing.T)
func TestCanonicalHashLengthPrefixesPreventBoundaryCollisions(t *testing.T)
```

The observation mutation table changes each subject/object term component, predicate byte, temporal kind, bound-presence bit, normalized time, evidence ID/role, derivation field, epistemic status, confidence value, and confidence scale and requires a different digest. Changing only observation retry ID must not change the semantic digest.

- [ ] **Step 5: Run digest tests and verify red**

Run:

```bash
(cd core && GOWORK=off go test ./internal/canonicalhash ./evidence ./observation \
  -run 'Digest|CanonicalHash' -count=1)
```

Expected: FAIL because versioned digest APIs and exact evidence identity are missing.

- [ ] **Step 6: Implement minimal versioned digests**

Prefix each preimage with its version, use length-prefixed binary fields, normalize every included timestamp first, and sort observation evidence by `(EvidenceID, Role)`. Keep the old revision-inclusive document methods temporarily so the legacy application compiles; mark them for deletion in Task 11.

Update every existing `EvidenceSpanInput` caller in the files listed above so
the stricter constructor never needs an implicit legacy mode. Extend
`VersionState` with the source document version's first durable
`DocumentRecordedAt`, make the transitional repository return that stored
value, and use it for every evidence span. A retry or a new extraction
configuration over the same source coordinates must therefore produce the
same evidence ID, recorded time, and digest. Tests use canonical fixed times.
Do not generate wall-clock values inside core.

- [ ] **Step 7: Run focused and module checks**

Run:

```bash
(cd core && GOWORK=off go test ./... -count=1)
go test ./internal/ingest ./internal/storage -count=1
make fmt
make modules-check
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add core internal/ingest internal/storage
git commit -m "Canonicalize core time and digests"
```

---

### Task 2: Publish Identity Authority and Generic Admission

**Files:**
- Create: `core/identity/authority.go`
- Create: `core/identity/authority_test.go`
- Create: `core/admission/admission.go`
- Create: `core/admission/admission_test.go`
- Modify: `core/identity/identity.go`
- Modify: `core/identity/identity_test.go`
- Modify: `core/identity/resolver.go`
- Modify: `core/identity/resolver_test.go`
- Modify: `core/dependency_test.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/storage/integration_test.go`

**Interfaces:**
- Consumes: `core/evidence` identities and Task 1 time/digest helpers.
- Produces:

```go
type EntityID string
type MentionID string
type ProposalID string
type CandidateID string
type DecisionID string
type AliasAssertionID string

type EntityInput struct {
	ID          EntityID
	Kind        Kind
	DisplayName string
	RecordedAt  time.Time
}

type MentionInput struct {
	ID                      MentionID
	EvidenceID              evidence.EvidenceID
	DerivationRunID         string
	Surface                 string
	NormalizedName          string
	ProposedEmail           string
	ProposedEmailEvidenceID evidence.EvidenceID
	Role                    string
	RecordedAt              time.Time
}

type ResolutionProposalInput struct {
	ID          ProposalID
	MentionID   MentionID
	ReasonCode  string
	EvidenceIDs []evidence.EvidenceID
	RecordedAt  time.Time
}

type CandidateSource struct {
	Kind      string
	Reference string
}

type ResolutionCandidateInput struct {
	ID         CandidateID
	ProposalID ProposalID
	EntityID   EntityID
	Rank       int
	Confidence float64
	ReasonCode string
	Source     CandidateSource
	RecordedAt time.Time
}

type DecisionOutcome string
type DecisionAuthority string

const (
	DecisionAccepted DecisionOutcome = "accepted"
	DecisionRejected DecisionOutcome = "rejected"

	AuthorityAutomatic DecisionAuthority = "automatic"
	AuthorityReviewer  DecisionAuthority = "reviewer"
)

type ResolutionDecisionInput struct {
	ID           DecisionID
	ProposalID   ProposalID
	Outcome      DecisionOutcome
	EntityID     EntityID
	Authority    DecisionAuthority
	ReasonCode   string
	RecordedAt   time.Time
	SupersedesID DecisionID
}

type AliasAssertionInput struct {
	ID         AliasAssertionID
	DecisionID DecisionID
	EntityID   EntityID
	Alias      Alias
	RecordedAt time.Time
}

func NewEntity(EntityInput) (Entity, error)
func NewMention(MentionInput) (MentionRecord, error)
func NewResolutionProposal(ResolutionProposalInput) (ResolutionProposal, error)
func NewResolutionCandidate(ResolutionCandidateInput) (ResolutionCandidate, error)
func NewResolutionDecision(ResolutionDecisionInput) (ResolutionDecision, error)
func NewAliasAssertion(AliasAssertionInput) (AliasAssertion, error)
```

```go
package admission

type TargetKind string
type Outcome string
type Authority string

const (
	TargetExtractionRun    TargetKind = "extraction_run"
	TargetMention          TargetKind = "mention"
	TargetObservation      TargetKind = "observation"
	TargetIdentityDecision TargetKind = "identity_decision"

	Admitted    Outcome = "admitted"
	Quarantined Outcome = "quarantined"
	Retired     Outcome = "retired"

	AuthorityAutomatic Authority = "automatic"
	AuthorityReviewer  Authority = "reviewer"
	AuthorityPolicy    Authority = "policy"
)

type DecisionInput struct {
	ID           string
	TargetKind   TargetKind
	TargetID     string
	Outcome      Outcome
	ReasonCode   string
	Authority    Authority
	RecordedAt   time.Time
	SupersedesID string
}

func NewDecision(DecisionInput) (Decision, error)
```

Identity and admission decision digests include version, normalized recorded time, target/proposal, outcome, authority, bounded reason code, entity where present, and superseded decision ID.

- [ ] **Step 1: Write failing durable authority tests**

Add tests named:

```go
func TestIdentityAuthorityAcceptsOpaqueTrimmedIDs(t *testing.T)
func TestResolutionDecisionRequiresBoundedAuthorityAndOutcome(t *testing.T)
func TestResolutionDecisionDigestIncludesSupersessionAndAuthority(t *testing.T)
func TestAliasAssertionIsOwnedByAcceptedDecision(t *testing.T)
func TestAdmissionDecisionPreservesTargetOutcomeAuthorityAndSupersession(t *testing.T)
func TestAdmissionDecisionDigestChangesForEveryAuthorityField(t *testing.T)
func TestIdentityAndAdmissionAuthorityNormalizeRecordedTime(t *testing.T)
```

- [ ] **Step 2: Run authority tests and verify red**

Run:

```bash
(cd core && GOWORK=off go test ./identity ./admission \
  -run 'Authority|Supersession|AliasAssertion|Admission|Digest' -count=1)
```

Expected: FAIL because the durable authority and admission types do not exist.

- [ ] **Step 3: Implement immutable authority values**

Validate trimmed nonblank opaque IDs, finite unit-interval candidate confidence,
positive rank, bounded enumerations, canonical time, accepted decisions
requiring an entity, rejected decisions forbidding an entity, and alias
assertion shape. Constructors defensively copy slices and preserve exact
non-secret reason codes. Task 5's repository verifies that every alias
assertion is owned by the accepted decision being appended and names that
decision's entity, and that candidate rank is unique within a proposal.

- [ ] **Step 4: Write the email-only automatic-resolution regression**

Replace `TestResolverAutoResolvesUniqueAcceptedNameAlias` with:

```go
func TestResolverKeepsUniqueExactNameAsReviewCandidate(t *testing.T)
```

Keep `TestResolverAutoResolvesAcceptedExactEmail`, and add a duplicate-email case that remains pending.
Update `TestResolverComparesNameAndEmailOnlyToMatchingAliasTypes` so an exact
name is only a ranked candidate when the supplied email does not match an
accepted email alias.

- [ ] **Step 5: Run resolver test and verify red**

Run:

```bash
(cd core && GOWORK=off go test ./identity \
  -run 'Resolver.*(Name|Email)' -count=1)
```

Expected: FAIL because the current resolver auto-resolves a unique accepted name.

- [ ] **Step 6: Restrict automatic resolution to unique exact accepted email**

Name equality and token overlap produce deterministic candidates only. Exactly one accepted exact email may set `AutoResolved=true`; zero or multiple accepted email matches remain review candidates.

Update `TestSyncResolvesGroundedNameIndependentlyOfModelEmail` to assert that
the grounded exact name produces a review candidate with no `EntityID` or
automatic authority. Keep the proposed model email audit-only; it must not
resolve the mention to the different email owner.

Rename the legacy integration regression to
`TestDirectoryReviewLifecycleNameAliasesRemainReviewCandidates` and require
both the unique-name and duplicate-name states to remain unresolved while
preserving deterministic candidate ranks. This is an intermediate build gate,
not a legacy schema compatibility feature; Task 11 deletes the entire legacy
storage package.

- [ ] **Step 7: Run core verification**

Run:

```bash
(cd core && GOWORK=off go test ./... -count=1)
go test ./internal/ingest -count=1
direnv exec . go test ./internal/storage \
  -run 'TestDirectoryReviewLifecycleNameAliasesRemainReviewCandidates' -count=1
make fmt
make modules-check
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add core internal/ingest/service_test.go internal/storage/integration_test.go
git commit -m "Add canonical authority contracts"
```

---

### Task 3: Create the PostgreSQL Adapter and Scoped Migration Engine

**Files:**
- Create: `adapters/postgres/go.mod`
- Create: `adapters/postgres/doc.go`
- Create: `adapters/postgres/dependency_test.go`
- Create: `adapters/postgres/database.go`
- Create: `adapters/postgres/transaction.go`
- Create: `adapters/postgres/errors.go`
- Create: `adapters/postgres/migration/manifest.go`
- Create: `adapters/postgres/migration/manifest_test.go`
- Create: `adapters/postgres/migration/migrator.go`
- Create: `adapters/postgres/migration/migrator_test.go`
- Create: `adapters/postgres/migration/migrator_integration_test.go`
- Create: `adapters/postgres/postgrestest/database.go`
- Modify: `go.work`
- Modify: `modules.txt`
- Modify: root `go.mod`
- Modify: root `go.sum`
- Modify: `Makefile`

**Interfaces:**
- Consumes: core module plus `pgx/v5`; synthetic embedded SQL fixtures for this task.
- Produces:

```go
type Scope string

type File struct {
	Version int64
	Name    string
	Path    string
}

type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum [sha256.Size]byte
}

type Privilege string

const (
	PrivilegeUsage Privilege = "USAGE"
	PrivilegeSelect Privilege = "SELECT"
	PrivilegeInsert Privilege = "INSERT"
	PrivilegeUpdate Privilege = "UPDATE"
)

type SchemaGrant struct {
	Schema     string
	Privileges []Privilege
}

type TableGrant struct {
	Schema     string
	Table      string
	Privileges []Privilege
	UpdateColumns []string
}

type ObjectKind string

const (
	ObjectSchema   ObjectKind = "schema"
	ObjectTable    ObjectKind = "table"
	ObjectFunction ObjectKind = "function"
	ObjectTrigger  ObjectKind = "trigger"
)

type OwnedObject struct {
	Kind   ObjectKind
	Schema string
	Parent string
	Name   string
}

type Manifest struct {
	Scope                   Scope
	Ledger                  string
	Migrations              []Migration
	OwnedSchemaTrees        []string
	OwnedObjects            []OwnedObject
	ApplicationSchemaGrants []SchemaGrant
	ApplicationTableGrants  []TableGrant
}

func LoadManifest(scope Scope, ledger string, files fs.FS, entries []File, ownedSchemaTrees []string, ownedObjects []OwnedObject, schemaGrants []SchemaGrant, tableGrants []TableGrant) (Manifest, error)
func (manifest Manifest) Validate() error
func ValidateManifestSet([]Manifest) error
```

```go
type Migrator struct {
	DatabaseURL      string
	ApplicationRole string
	Manifests        []Manifest
}

type ScopeApplyResult struct {
	Scope          Scope
	Applied        []int64
	CurrentVersion int64
}

type ApplyResult struct {
	Scopes []ScopeApplyResult
}

func (migrator Migrator) Apply(context.Context) (ApplyResult, error)
```

```go
func Open(context.Context, string) (*Database, error)
func (database *Database) Close()
func (database *Database) InTransaction(
	context.Context,
	func(*Transaction) error,
) error
```

The fixed advisory-lock key is derived by SHA-256 from the named string `github.com/JakeFAU/stacks/postgres-migrations/v1`; it is not an unexplained numeric literal.

- [ ] **Step 1: Write failing manifest tests**

Cover exact embedded-byte SHA-256, unsafe ledger/schema/table/role identifiers,
blank SQL, duplicate or unordered versions, duplicate names, unsupported
privileges, duplicate schema-tree or exact-object ownership, unowned created
objects, and independent core/directory version `1`. Core owns the complete
`stacks_core` tree plus the exact `stacks_migrations` schema object and
`core_version` ledger table. Directory owns the complete `stacks_directory`
tree plus the exact `directory_version` ledger table; it does not claim the
shared schema.

- [ ] **Step 2: Run manifest tests and verify red**

Run:

```bash
(cd adapters/postgres && GOWORK=off go test ./migration \
  -run 'TestManifest|TestOwnership|TestGrant' -count=1)
```

Expected: FAIL because the adapter module and manifest contract do not exist.

- [ ] **Step 3: Add the module and implement manifest validation**

Set module path `github.com/JakeFAU/stacks/adapters/postgres`, require only
`github.com/JakeFAU/stacks/core v0.0.0` and pgx, and add the
repository-relative
`replace github.com/JakeFAU/stacks/core => ../../core` needed for isolated
`GOWORK=off` checks. Add the module to `go.work` and `modules.txt`; the root
application consumes it through the workspace. Add no provider, logging,
telemetry, configuration, or application imports.

- [ ] **Step 4: Write failing transactional migration tests**

Add live tests:

```go
func TestMigratorCreatesIndependentLedgers(t *testing.T)
func TestMigratorExactRetryPerformsNoWrites(t *testing.T)
func TestMigratorRejectsChangedAppliedChecksumBeforePendingWork(t *testing.T)
func TestMigratorRollsBackFailedVersionAndLedgerInsert(t *testing.T)
func TestMigratorSerializesConcurrentApplyWithoutSleep(t *testing.T)
func TestMigratorPreservesCancellationAndReleasesSessionLock(t *testing.T)
func TestApplicationRoleCanInspectButCannotMigrate(t *testing.T)
```

The concurrency test holds the named advisory lock on connection A, starts
apply on connection B, observes B waiting through `pg_locks`, releases A, and
asserts one application. The test-only `postgrestest` package generates
temporary database names internally, quotes identifiers, limits connection
termination to the exact database, and drops only that database in cleanup.

- [ ] **Step 5: Run live migration tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./migration \
  -run "TestMigrator|TestApplicationRole" -count=1'
```

Expected: FAIL because `Migrator.Apply` and scoped ledgers are missing.

- [ ] **Step 6: Implement the minimal migrator**

Use one dedicated connection and a session advisory lock across scopes. Create `stacks_migrations`, then each requested ledger with `version`, `name`, `checksum`, and `applied_at`. Validate every applied record before any pending SQL. Apply one migration plus its ledger insert and scope-owned grants in one transaction. A failed command leaves the version pending; exact current apply performs no writes. Explicitly unlock, and close the connection if cancellation prevents cleanup.

- [ ] **Step 7: Run module and repository checks**

Run:

```bash
(cd adapters/postgres && GOWORK=off go test ./... -count=1)
make modules-check
make fmt
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add adapters/postgres go.work modules.txt go.mod go.sum Makefile
git commit -m "Add scoped PostgreSQL migrations"
```

---

### Task 4: Install and Persist Canonical Documents and Evidence

**Files:**
- Create: `adapters/postgres/coremigrations/manifest.go`
- Create: `adapters/postgres/coremigrations/manifest_test.go`
- Create: `adapters/postgres/coremigrations/migrations/00001_documents_evidence.sql`
- Create: `adapters/postgres/documents.go`
- Create: `adapters/postgres/documents_test.go`
- Create: `adapters/postgres/documents_integration_test.go`

**Interfaces:**
- Consumes: Task 1 canonical evidence and Task 3 migration/database boundary.
- Produces:

```go
package coremigrations

func Manifest() (migration.Manifest, error)
```

```go
type DocumentVersionRef struct {
	SourceDocumentID string
	VersionID        string
	RecordedAt       time.Time
}

type DocumentVersionRecord struct {
	Ref       DocumentVersionRef
	Version   evidence.DocumentVersion
	Revisions []evidence.SourceRevisionObservation
}

type PutDocumentVersionResult struct {
	Ref            DocumentVersionRef
	ContentCreated bool
}

func (db *Database) PutDocumentVersion(context.Context, evidence.DocumentVersion) (PutDocumentVersionResult, error)
func (tx *Transaction) PutDocumentVersion(context.Context, evidence.DocumentVersion) (PutDocumentVersionResult, error)
func (tx *Transaction) PutSourceRevisionObservation(context.Context, evidence.SourceRevisionObservation) (bool, error)
func (db *Database) LoadDocumentVersion(context.Context, string) (DocumentVersionRecord, error)
func (tx *Transaction) PutEvidenceSpan(context.Context, evidence.EvidenceSpan) (bool, error)
func (db *Database) LoadEvidenceSpan(context.Context, evidence.EvidenceID) (evidence.EvidenceSpan, error)
func (tx *Transaction) SetCurrentDocumentVersion(context.Context, string, string) error
```

The first core migration creates only:

```text
stacks_core.source_documents
stacks_core.document_versions
stacks_core.source_revision_observations
stacks_core.document_sections
stacks_core.evidence_spans
```

Required constraints:

- unique `(provider, provider_document_id)` source identity;
- unique `(source_document_id, digest_version, content_digest)` immutable version identity;
- unique revision-observation identity over source, provider version/revision, and content version;
- `content_digest` and evidence digest are exactly 32 bytes;
- provider version is required; provider revision is optional append-only provenance and is not a mutable column on the stable content version;
- all timestamps are `timestamptz(6)`;
- section primary key is `(document_version_id, section_id)`;
- section order is nonnegative and unique per version;
- evidence offsets satisfy `start_offset >= 0` and `end_offset > start_offset`;
- current version is a composite foreign key `(source_document_id, current_version_id)` into the same source's versions;
- no provider, manager-confidence, directory, vector, or model configuration object exists.

- [ ] **Step 1: Write failing schema ownership tests**

Add:

```go
func TestCoreManifestStartsWithDocumentsAndEvidence(t *testing.T)
func TestDocumentsMigrationContainsNoVerticalOrProviderObjects(t *testing.T)
func TestCleanCoreDocumentsInstallUsesTextIDsAndTimestampSix(t *testing.T)
```

Inspect `information_schema` and `pg_catalog`, not SQL source text alone.

- [ ] **Step 2: Run schema tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./coremigrations \
  -run "Test(CoreManifest|DocumentsMigration|CleanCoreDocuments)" -count=1'
```

Expected: FAIL because the core manifest and first canonical migration do not exist.

- [ ] **Step 3: Write the first core migration and least-privilege grants**

Do not change the current init script yet: Goose still needs its bootstrap
schemas until Task 10 atomically replaces the database commands. The core
manifest grants the configured application role:

- `USAGE` on `stacks_core`;
- `SELECT, INSERT` on immutable versions, revision observations, sections, and evidence;
- `SELECT, INSERT, UPDATE (current_version_id)` on source documents; and
- read-only access to the core ledger and catalog inspection required by status.

- [ ] **Step 4: Write failing repository round-trip tests**

Cover:

```go
func TestDocumentVersionRoundTripsCanonicalSourceAndSectionState(t *testing.T)
func TestDocumentVersionRevisionChurnReusesStableContentVersion(t *testing.T)
func TestProviderRevisionChurnAppendsProvenanceWithoutRewritingContent(t *testing.T)
func TestDocumentVersionExactRetryIsReadOnly(t *testing.T)
func TestDocumentVersionStableIdentityConflictIsBounded(t *testing.T)
func TestEvidenceSpanRoundTripsExactUTF8RangeAndDigest(t *testing.T)
func TestCurrentVersionPointerRejectsVersionFromDifferentSource(t *testing.T)
func TestApplicationRoleCannotRewriteImmutableEvidence(t *testing.T)
func TestDocumentRepositoryPreservesCancellation(t *testing.T)
```

- [ ] **Step 5: Run repository tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./... \
  -run "Test(DocumentVersion|EvidenceSpan|DocumentRepository|ApplicationRoleCannotRewrite)" -count=1'
```

Expected: FAIL because the document/evidence repository methods are missing.

- [ ] **Step 6: Implement immutable persistence**

Derive internal row IDs deterministically from logical source identity and the
canonical digest without exposing that algorithm as a core ID requirement.
The stable content row excludes `ProviderRevision`. After
`PutDocumentVersion` returns the stored content version's first `RecordedAt`,
the owning transaction constructs the core revision observation from that
stable time and appends it with `PutSourceRevisionObservation`. Each distinct
provider version/revision/content tuple therefore creates one immutable row
whose `first_recorded_at` is never updated, while an exact repeat compares the
complete canonical payload and performs no write. Same identity plus different
payload returns `ErrConflict`. Load returns the canonical content value plus
all revision observations and verifies stored digest/version before returning.
Never discard or overwrite a later distinct revision.

- [ ] **Step 7: Run task verification**

Run:

```bash
(cd adapters/postgres && GOWORK=off go test ./... -count=1)
make fmt
make modules-check
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add adapters/postgres
git commit -m "Persist canonical documents and evidence"
```

---

### Task 5: Persist Identity Authority and Generic Admission History

**Files:**
- Create: `adapters/postgres/coremigrations/migrations/00002_identity_admission.sql`
- Modify: `adapters/postgres/coremigrations/manifest.go`
- Modify: `adapters/postgres/coremigrations/manifest_test.go`
- Create: `adapters/postgres/identity.go`
- Create: `adapters/postgres/identity_test.go`
- Create: `adapters/postgres/identity_integration_test.go`
- Create: `adapters/postgres/admission.go`
- Create: `adapters/postgres/admission_test.go`
- Create: `adapters/postgres/admission_integration_test.go`

**Interfaces:**
- Consumes: Task 2 identity/admission values and Task 4 evidence IDs.
- Produces:

```go
func (tx *Transaction) PutEntity(context.Context, identity.Entity) (bool, error)
func (tx *Transaction) PutMention(context.Context, identity.MentionRecord) (bool, error)
func (tx *Transaction) PutResolutionProposal(context.Context, identity.ResolutionProposal) (bool, error)
func (tx *Transaction) PutResolutionCandidate(context.Context, identity.ResolutionCandidate) (bool, error)
func (tx *Transaction) AppendResolutionDecision(context.Context, identity.ResolutionDecision, []identity.AliasAssertion) error
func (db *Database) EffectiveResolutionDecision(context.Context, identity.ProposalID) (identity.ResolutionDecision, error)
func (db *Database) LoadResolutionDecision(context.Context, identity.DecisionID) (identity.ResolutionDecision, error)
func (db *Database) EntitySnapshots(context.Context) ([]identity.EntitySnapshot, error)
func (db *Database) ListEntities(context.Context) ([]EntityRecord, error)
func (db *Database) LoadEntity(context.Context, identity.EntityID) (EntityRecord, error)
func (db *Database) ListPendingResolutionProposals(context.Context) ([]ResolutionProposalRecord, error)
func (db *Database) LoadResolutionProposal(context.Context, identity.ProposalID) (ResolutionProposalRecord, error)
func (tx *Transaction) AppendAdmissionDecision(context.Context, admission.Decision) error
func (db *Database) EffectiveAdmissionDecision(context.Context, admission.TargetKind, string) (admission.Decision, error)
```

`EntityRecord` is a generic canonical entity plus effective alias assertions,
grounding mention IDs, and evidence IDs. `ResolutionProposalRecord` is a
canonical proposal plus candidates and its optional effective decision. These
are adapter-neutral read models used by root application review composition;
they contain no rendered CLI context, masked presentation strings, or
directory foreign-key DTOs.

Migration `00002` creates:

```text
stacks_core.entities
stacks_core.mentions
stacks_core.resolution_proposals
stacks_core.resolution_candidates
stacks_core.resolution_decisions
stacks_core.entity_alias_assertions
stacks_core.admission_targets
stacks_core.admission_decisions
```

Canonical candidates store `source_kind` and opaque `source_reference`; no core column or foreign key mentions directory state. Decision rows are immutable. Unique initial-decision and unique superseded-predecessor constraints prevent roots and corrections from branching. Repository transactions lock the proposal or admission target before proving the claimed predecessor is effective.

- [ ] **Step 1: Write failing identity/admission schema tests**

Add live tests proving text IDs, decision immutability, generic candidate provenance, one initial decision, one successor per predecessor, and no `stacks_core` foreign key targeting `stacks_directory`.

- [ ] **Step 2: Run schema tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./coremigrations \
  -run "Test.*(Identity|Admission|DirectoryDependency)" -count=1'
```

Expected: FAIL because migration `00002` is missing.

- [ ] **Step 3: Add the identity/admission migration**

Use `text CHECK (btrim(id) <> '')` for domain IDs, `timestamptz(6)` for authority time, fixed check constraints for outcomes/authority/target kinds, exact digest length, and foreign keys to immutable evidence where proposals or mentions claim evidence.
Extend the core manifest's grants with `SELECT, INSERT` on immutable identity,
proposal, candidate, alias, target, and decision rows. There is no application
`UPDATE` or `DELETE` grant for authority history.

- [ ] **Step 4: Write failing authority repository tests**

Add:

```go
func TestIdentityRepositoryAcceptsOpaqueTextIDs(t *testing.T)
func TestIdentityRetryIsIdempotentAndPayloadConflictFails(t *testing.T)
func TestNameOnlyCandidateNeverCreatesAutomaticAuthority(t *testing.T)
func TestUniqueExactWorkEmailCanCreateAutomaticAuthority(t *testing.T)
func TestReviewerCorrectionAppendsAndSupersedes(t *testing.T)
func TestConcurrentCorrectionsCannotBranch(t *testing.T)
func TestEffectiveAliasesFollowOnlyCurrentAcceptedAuthority(t *testing.T)
func TestReviewReadModelsContainOnlyCanonicalIdentityState(t *testing.T)
func TestAdmissionQuarantineThenAdmissionPreservesHistory(t *testing.T)
func TestConcurrentAdmissionCorrectionsCannotBranch(t *testing.T)
```

- [ ] **Step 5: Run repository tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./... \
  -run "Test(Identity|NameOnly|UniqueExact|Reviewer|Concurrent|EffectiveAliases|Admission)" -count=1'
```

Expected: FAIL because identity and admission repositories are missing.

- [ ] **Step 6: Implement append-only authority**

Exact retries compare canonical digests. Corrections insert a successor and never update the predecessor. Rejected decisions create no aliases. Entity snapshots expose aliases only from the effective accepted decision chain. Directory source references remain opaque strings.

- [ ] **Step 7: Run task verification**

Run:

```bash
(cd adapters/postgres && GOWORK=off go test ./... -count=1)
make fmt
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add adapters/postgres
git commit -m "Persist canonical identity authority"
```

---

### Task 6: Persist Extraction Attempts and Complete Canonical Observations

**Files:**
- Create: `adapters/postgres/coremigrations/migrations/00003_extraction_observations.sql`
- Modify: `adapters/postgres/coremigrations/manifest.go`
- Modify: `adapters/postgres/coremigrations/manifest_test.go`
- Create: `adapters/postgres/extraction.go`
- Create: `adapters/postgres/extraction_test.go`
- Create: `adapters/postgres/extraction_integration_test.go`
- Create: `adapters/postgres/observation.go`
- Create: `adapters/postgres/observation_test.go`
- Create: `adapters/postgres/observation_integration_test.go`
- Create: `adapters/postgres/query.go`
- Create: `adapters/postgres/query_test.go`
- Create: `adapters/postgres/longitudinal_integration_test.go`

**Interfaces:**
- Consumes: Task 1 observation contract, Task 4 document versions, and Task 5 identities/admission.
- Produces:

```go
type ExtractionRunInput struct {
	ID                      string
	DocumentVersionID       string
	DerivationDigestVersion string
	DerivationDigest        evidence.ContentDigest
	Method                  string
	Version                 string
	Provider                string
	DataMode                string
	Model                   string
	PromptVersion           string
	SchemaDigest            evidence.ContentDigest
	MaxOutputTokens         int
	RecordedAt              time.Time
}

type LeaseRequest struct {
	AttemptID    string
	Owner        string
	ClaimedAt    time.Time
	LeaseDuration time.Duration
}

type ExtractionFailureInput struct {
	RunID       string
	AttemptID   string
	Owner       string
	FailedAt    time.Time
	FailureCode string
}

type ExtractionCompletionInput struct {
	RunID                string
	AttemptID            string
	Owner                string
	CompletedAt          time.Time
	WriteSetDigestVersion string
	WriteSetDigest       evidence.ContentDigest
}

func (tx *Transaction) PrepareExtraction(context.Context, ExtractionRunInput, LeaseRequest) (ExtractionState, error)
func (tx *Transaction) RecordExtractionFailure(context.Context, ExtractionFailureInput) error
func (tx *Transaction) CompleteExtraction(context.Context, ExtractionCompletionInput) error
func (tx *Transaction) PutObservation(context.Context, observation.Observation) (bool, error)
func (db *Database) LoadObservation(context.Context, observation.ObservationID) (observation.Observation, error)
func (db *Database) ListAdmittedRelationshipObservations(context.Context, identity.EntityID, identity.EntityID) ([]ObservationRecord, error)
```

Migration `00003` creates:

```text
stacks_core.extraction_runs
stacks_core.extraction_attempts
stacks_core.observations
stacks_core.observation_evidence
```

Observation columns store explicit subject/object term tags and kind-specific values, temporal kind and bound-presence booleans, independent derivation fields, epistemic status, confidence value/scale pairing, digest version/value, and recorded time. A deferred transaction-final check proves every observation is cited.
Extraction completion stores the versioned canonical write-set digest on the
run and changes the owning attempt and run to completed in the caller's
existing transaction.

- [ ] **Step 1: Write failing extraction lifecycle tests**

Add:

```go
func TestExtractionFirstClaimCreatesAttemptOne(t *testing.T)
func TestExtractionLiveLeaseReturnsBusyWithoutNewAttempt(t *testing.T)
func TestExtractionExpiredLeaseAppendsReclaimedAttempt(t *testing.T)
func TestExtractionFailureRetainsAttemptAndPermitsRetry(t *testing.T)
func TestExtractionCompletedResumeCreatesNoAttempt(t *testing.T)
func TestExtractionCompletionExactRetryMatchesWriteSetDigest(t *testing.T)
func TestExtractionCompletionRejectsDifferentWriteSetDigest(t *testing.T)
func TestExtractionWrongOwnerCannotFailOrComplete(t *testing.T)
func TestExtractionCancellationPreservesErrorsIs(t *testing.T)
func TestExtractionRejectsNonCanonicalLifecycleTimesBeforeSQL(t *testing.T)
```

All IDs and times are caller supplied. Test non-UTC, monotonic, and
sub-microsecond recorded/claimed/expiry/failure/completion values and require
rejection through `timepoint.IsCanonical` before any SQL call. Adapter code
contains no `time.Now`, random ID, provider type, or disclosure-policy type.

- [ ] **Step 2: Run extraction tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./... \
  -run "TestExtraction" -count=1'
```

Expected: FAIL because the lifecycle tables and repository are missing.

- [ ] **Step 3: Implement append-preserving attempt lifecycle**

Run identity/provenance is immutable. Each claim creates an attempt; active
attempts may transition once to completed, failed, canceled, or expired while
prior attempts remain. Claim and reclaim lock the run. `CompleteExtraction`
requires the current run/attempt/owner, stores the versioned full-write-set
digest, and transitions both records in the surrounding transaction. Its exact
retry is read-only; a different digest, owner, or attempt is a bounded
conflict. Stable completed derivation returns completed without another
attempt.
Extend application grants with `SELECT, INSERT` on runs, attempts,
observations, and evidence roles, plus column-scoped `UPDATE` only for the
active attempt's lease and terminal-state columns. No application grant may
rewrite extraction-run provenance or canonical observation payloads.

- [ ] **Step 4: Write failing canonical observation matrix**

Cover every subject and object term independently, unknown/instant/during/since/until/window, all epistemic states, absent or unit-interval confidence, independent derivation fields, exact text/predicate bytes, same evidence ID in both roles, canonical evidence ordering, retry equality, stable-ID conflict, corrupt-row rejection, and uncited transaction rollback. Generic relationship projection resolves mention terms through current effective identity authority while preserving direct entity terms as the original assertion.

Add a wholly non-manager live scenario:

```go
func TestProjectCommitmentChronologyPreservesChangeUncertaintyAndCounterevidence(t *testing.T)
```

Three dated synthetic project notes first hypothesize a Project Atlas delivery
commitment, then replace it with a later date while retaining the first note as
contradicting evidence, then confirm the revised commitment. Load the canonical
observations and run the deterministic core temporal comparison/chronology
operators. Assert valid-time order, independent recorded-time cutoffs,
hypothesized versus observed status, the changed commitment, supporting
evidence, and preserved counterevidence. The fixture and test contain no person
pair, interaction category, manager-confidence predicate, analysis package
import, or manager-specific repository.

- [ ] **Step 5: Run observation tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./... \
  -run "Test(CanonicalObservation|ObservationQuery|ProjectCommitment)" -count=1'
```

Expected: FAIL because the canonical observation codec and query projection are missing.

- [ ] **Step 6: Implement strict canonical observation storage**

The adapter reconstructs the public value and verifies its digest before return. It never infers temporal kind, confidence scale, evidence role, or derivation. `ObservationRecord` contains canonical observation, exact evidence spans, source/version/section provenance, and effective admission; it contains no manager vocabulary.

- [ ] **Step 7: Run task verification**

Run:

```bash
(cd adapters/postgres && GOWORK=off go test ./... -count=1)
make fmt
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add adapters/postgres
git commit -m "Persist canonical extraction observations"
```

---

### Task 7: Build the Canonical Ingestion Write Set and Atomic Completion

**Files:**
- Create: `internal/ingest/canonical.go`
- Create: `internal/ingest/canonical_test.go`
- Create: `internal/ingest/postgres.go`
- Create: `internal/ingest/postgres_test.go`
- Create: `internal/ingest/postgres_integration_test.go`
- Create: `internal/analysis/observation.go`
- Create: `internal/analysis/observation_test.go`
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/ingest/validate.go`
- Modify: `internal/ingest/validate_test.go`
- Modify: root `go.mod`
- Modify: root `go.sum`

**Interfaces:**
- Consumes: Tasks 4–6 transaction methods and current extraction/provider boundaries.
- Produces:

```go
type DraftTerm struct {
	Kind                 observation.TermKind
	Text                 string
	MentionKey           string
	EntityID             string
	GroundingMentionKey  string
}

type DraftEvidenceLink struct {
	EvidenceKey string
	Role        observation.EvidenceRole
}

type CanonicalObservationDraft struct {
	ID         observation.ObservationID
	Subject    DraftTerm
	Predicate  observation.Predicate
	Object     DraftTerm
	ValidTime  observation.TemporalExtent
	RecordedAt time.Time
	Evidence   []DraftEvidenceLink
	Derivation observation.Derivation
	Status     observation.EpistemicStatus
	Confidence *observation.Confidence
}

type Completion struct {
	VersionID         string
	RunID             string
	AttemptID         string
	LeaseOwner        string
	CompletedAt       time.Time
	Evidence          []EvidenceRecord
	Mentions          []MentionRecord
	Proposals         []identity.ResolutionProposal
	Candidates        []identity.ResolutionCandidate
	Decisions         []identity.ResolutionDecision
	AliasAssertions   []identity.AliasAssertion
	Observations       []CanonicalObservationDraft
	AdmissionDecisions []admission.Decision
}

type VersionState struct {
	VersionID         string
	RunID             string
	AttemptID         string
	LeaseOwner        string
	DocumentRecordedAt time.Time
	LeaseExpiresAt    time.Time
	Status            VersionStatus
	RetryCount        int
	FailureCode       FailureCode
}

type Failure struct {
	RunID       string
	AttemptID   string
	LeaseOwner  string
	Status      VersionStatus
	Code        FailureCode
	FailedAt    time.Time
}

type SourceRevisionMetadata struct {
	ProviderVersion  string
	ProviderRevision string
}

type Repository interface {
	PrepareVersion(
		context.Context,
		evidence.DocumentVersion,
		SourceRevisionMetadata,
		DerivationIdentity,
		modelpolicy.DataMode,
		time.Duration,
	) (VersionState, error)
	CompleteVersion(context.Context, Completion) error
	RecordFailure(context.Context, Failure) error
	EntitySnapshots(context.Context) ([]entity.EntitySnapshot, error)
}
```

`PostgresRepository` implements the consumer-owned `ingest.Repository`; the external adapter module never imports `stacks/internal/ingest`.
`ingest.Service` passes bounded source-revision metadata independently of the
content version. `PostgresRepository.PrepareVersion` persists or reuses the
content version, uses the returned stable `DocumentVersionRef.RecordedAt` to
construct the canonical core source-revision observation, and appends that
observation in the same transaction. It rejects metadata whose provider
version differs from the content version. It then maps
`PrepareExtraction` outcomes to pending, busy, failed-retry, or completed
resume without invoking the model for completed work. Immutable run provenance
or stable-ID conflicts are semantic mismatches, not resumable work.
`RecordFailure` maps the typed root failure into
`ExtractionFailureInput`. `CompleteVersion` resolves all local keys, computes a
versioned digest over the complete resolved canonical write set, writes it
atomically, and calls `CompleteExtraction` last in the same transaction.

- [ ] **Step 1: Write failing manager-interaction predicate and draft tests**

In `internal/analysis/observation.go`, define one versioned namespace that encodes category and direction into an ordinary predicate while subject and object remain person terms. Add round-trip rejection for unknown namespace/version/category/direction. Map signal supporting/contradicting citations directly to canonical evidence roles and keep unit-interval confidence.

Add tests:

```go
func TestInteractionObservationPredicateRoundTrips(t *testing.T)
func TestInteractionObservationPredicateRejectsUnknownVocabulary(t *testing.T)
func TestCanonicalCompletionPersistsInteractionObservationsWithoutSignalDTO(t *testing.T)
func TestInteractionDraftPersistsGroundedMentionTerms(t *testing.T)
func TestCanonicalDraftKeepsSupportingAndContradictingRoles(t *testing.T)
func TestCanonicalDraftUsesInstantOrUnknownSourceTime(t *testing.T)
func TestCanonicalCompletionRecordsBoundedLeaseAndTransactionOutcome(t *testing.T)
```

- [ ] **Step 2: Run mapper tests and verify red**

Run:

```bash
go test ./internal/ingest ./internal/analysis \
  -run 'Test(InteractionObservation|Canonical)' -count=1
```

Expected: FAIL because the canonical draft and predicate codec do not exist.

- [ ] **Step 3: Implement the canonical application mapping**

Interaction observations use the versioned application predicate namespace.
Their source-grounded people are persisted as `TermMention`, never frozen as
the entity selected during that extraction run. Current effective identity
authority resolves those mentions at query time, so reviewer correction
changes pair selection without rewriting or reingesting the observation.
Attributed statements remain validated intermediates for selecting exact
supporting evidence; Plan C does not coerce their speaker and semantic-subject
fields into the narrower observation term contract. Source meeting time
becomes an instant; absence remains unknown. Do not use `Since` merely to fit
legacy nullable columns. Rationale is deterministic presentation derived from
category/direction, not durable vertical state.

- [ ] **Step 4: Write failing atomic completion tests**

Inject failure after:

1. evidence;
2. mentions/proposals/candidates;
3. automatic identity decisions/aliases;
4. observations/evidence roles;
5. initial admission decisions;
6. current document-version pointer update; and
7. extraction-attempt completion.

For each injection, assert the entire transaction is invisible. Add exact completed retry, semantic mismatch, different owner, additive later identity state, and additive directory state cases.

- [ ] **Step 5: Run completion tests and verify red**

Run:

```bash
direnv exec . go test ./internal/ingest \
  -run 'TestCanonical(Completion|Rollback|CompletedRetry)' -count=1
```

Expected: FAIL because `PostgresRepository` and canonical completion do not exist.

- [ ] **Step 6: Implement one transaction-owned completion**

Resolve local evidence and mention keys inside the transaction, construct
canonical core values, compute and compare the full versioned canonical
write-set digest on an exact completion retry, and call only adapter
transaction methods. Completion advances the source current-version pointer
only after every required durable write succeeds, then invokes
`CompleteExtraction` as the transaction's final state transition.

- [ ] **Step 7: Switch `ingest.Service` to canonical completion**

Delete `SignalRecord`, `SignalEvidenceRecord`, the legacy `ObservationDraft`, `interactionPredicate`, confidence downgrade, and direct `analysis.ExplainSignal` dependency from ingestion. Preserve prompt/schema/provider calls, lease deadlines, failure classification, and fail-soft post-completion directory enrichment.

- [ ] **Step 8: Run task verification**

Run:

```bash
go test ./internal/ingest ./internal/analysis -count=1
go test ./cmd/stacks -run 'Test.*Sync' -count=1
make fmt
git diff --check
```

Expected: all commands pass.

- [ ] **Step 9: Commit**

```bash
git add internal/ingest internal/analysis go.mod go.sum
git commit -m "Cut ingestion to canonical observations"
```

---

### Task 8: Add the Optional Directory Scope Without Core Dependency

**Files:**
- Create: `adapters/postgres/directorymigrations/manifest.go`
- Create: `adapters/postgres/directorymigrations/manifest_test.go`
- Create: `adapters/postgres/directorymigrations/migrations/00001_directory.sql`
- Create: `adapters/postgres/directory.go`
- Create: `adapters/postgres/directory_test.go`
- Create: `adapters/postgres/directory_integration_test.go`
- Create: `internal/directory/postgres.go`
- Create: `internal/directory/postgres_test.go`
- Modify: `internal/directory/service.go`
- Modify: `internal/directory/service_test.go`
- Modify: `internal/ingest/postgres.go`
- Modify: `internal/ingest/postgres_integration_test.go`

**Interfaces:**
- Consumes: canonical core identity candidates/decisions, optional directory service inputs, and Task 3 migration scopes.
- Produces:

```go
package directorymigrations

func Manifest() (migration.Manifest, error)
```

```go
type DirectoryStore struct {
	Database *Database
}

func (store DirectoryStore) LoadWork(context.Context, DirectoryWorkRequest) (DirectoryWorkset, error)
func (store DirectoryStore) LoadIdentityState(context.Context) (DirectoryIdentityState, error)
func (store DirectoryStore) Persist(context.Context, DirectoryPersistInput) (DirectoryPersistResult, error)
```

The root `internal/directory/postgres.go` maps these adapter-neutral values to the existing consumer-owned `directory.Repository` interface. The external adapter module does not import root internal packages.

Migration `00001_directory.sql` creates only:

```text
stacks_directory.snapshots
stacks_directory.profiles
stacks_directory.profile_emails
stacks_directory.lookup_attempts
stacks_directory.entity_links
```

Directory-owned rows may reference core entity/proposal/candidate IDs. No core table, constraint, trigger, function, or query references `stacks_directory`.

- [ ] **Step 1: Write failing optional-scope schema tests**

Add:

```go
func TestDirectoryManifestIsIndependentFromCoreVersion(t *testing.T)
func TestCoreOnlyInstallContainsNoDirectoryObjects(t *testing.T)
func TestDirectoryInstallReferencesCoreOnlyFromDirectory(t *testing.T)
func TestDirectoryAbsenceLeavesCoreMigrationCurrent(t *testing.T)
```

- [ ] **Step 2: Run schema tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./directorymigrations \
  -run "TestDirectory|TestCoreOnly" -count=1'
```

Expected: FAIL because the directory manifest and schema are missing.

- [ ] **Step 3: Add the optional directory migration**

Store normalized provider subject, bounded snapshots, changed-profile history, exact-email indexes, attempts/outcomes/retry times, and optional directory-to-core links. Application grants belong only to this scope.

- [ ] **Step 4: Write failing directory policy/persistence tests**

Port the current behavioral matrix to canonical schemas:

- exact email with one eligible profile may propose automatic authority;
- name-only match creates review candidate only;
- provider conflict downgrades to review;
- reviewer acceptance/correction remains authoritative;
- missing/no-match/unavailable lookup preserves explicit decisions;
- retry windows and changed snapshots remain idempotent;
- two concurrent exact-email attempts create one authority;
- injected core write failure rolls back directory and core writes;
- directory cancellation preserves canonical cancellation;
- non-UTC, monotonic, or sub-microsecond snapshot, lookup, retry, and link times fail before SQL;
- disabled directory constructs neither store nor client.

- [ ] **Step 5: Run directory tests and verify red**

Run:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./... \
  -run "TestCanonicalDirectory" -count=1'
direnv exec . go test ./internal/directory \
  -run 'Test(DirectoryPostgres|Directory.*Concurrent)' -count=1
```

Expected: FAIL because canonical directory persistence and mapping are missing.

- [ ] **Step 6: Implement additive directory persistence**

Use one core transaction for any authority change plus its directory link. Store core candidate provenance as `source_kind=directory` and an opaque source reference. A provider failure records only bounded adapter attempt state and never rolls back already completed ingestion.
Validate every raw adapter time with `timepoint.IsCanonical`; never rely on
PostgreSQL `timestamptz(6)` truncation.

- [ ] **Step 7: Run focused race and package verification**

Run:

```bash
direnv exec . go test -race ./internal/directory -count=1
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test -race ./... \
  -run "TestDirectory.*Concurrent" -count=1'
make fmt
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add adapters/postgres internal/directory internal/ingest
git commit -m "Add optional directory persistence"
```

---

### Task 9: Make Manager Confidence a Canonical Read-Only Query Use Case

**Files:**
- Create: `internal/analysis/postgres.go`
- Create: `internal/analysis/postgres_test.go`
- Create: `internal/analysis/postgres_integration_test.go`
- Modify: `internal/analysis/service.go`
- Modify: `internal/analysis/service_test.go`
- Modify: `internal/analysis/signal.go`
- Modify: `internal/analysis/signal_test.go`
- Modify: `internal/cli/analyze.go`
- Modify: `internal/cli/analyze_test.go`

**Interfaces:**
- Consumes: Task 6 generic admitted relationship observations and Task 7 manager-interaction predicate codec.
- Produces:

```go
type Repository interface {
	LoadPairInputs(context.Context, string, string) (PairSnapshot, error)
}
```

The temporary repository reads canonical effective identity authority, admitted canonical observations, exact evidence roles, source/version/section provenance, and independent valid/recorded time. It never reads or writes `interaction_signals`, `signal_evidence`, `analysis_runs`, or `analysis_inputs`.

- [ ] **Step 1: Write failing canonical query translation tests**

Add:

```go
func TestCanonicalManagerQueryRequiresAcceptedPair(t *testing.T)
func TestCanonicalManagerQueryMapsOnlyVersionedInteractionPredicates(t *testing.T)
func TestCanonicalManagerQueryPreservesSupportingAndCounterevidence(t *testing.T)
func TestCanonicalManagerQuerySeparatesUnknownAndDatedSourceTime(t *testing.T)
func TestCanonicalManagerQueryUsesCurrentAdmissionAndIdentityAuthority(t *testing.T)
func TestCanonicalManagerQueryCorrectionChangesResultWithoutReingest(t *testing.T)
func TestCanonicalManagerQueryAcceptsOpaqueEntityIDs(t *testing.T)
func TestCanonicalManagerQueryResolvesMentionTermsThroughCurrentAuthority(t *testing.T)
```

- [ ] **Step 2: Run query tests and verify red**

Run:

```bash
direnv exec . go test ./internal/analysis \
  -run 'TestCanonicalManagerQuery' -count=1
```

Expected: FAIL because the canonical query repository is missing.

- [ ] **Step 3: Implement canonical observation-to-signal translation**

Compute deterministic rationale from the versioned category/direction
predicate. Build citations from exact evidence and their canonical role. Use
source-document identity for meeting identity. Resolve each stored mention
term through its current effective accepted identity decision, then filter the
requested opaque entity pair and effective admission without rewriting
observations. Remove UUID parsing and UUID-shaped validation from every
surviving analysis/configuration path.

- [ ] **Step 4: Write failing on-demand service tests**

Remove caching expectations and add:

```go
func TestServiceUsesOnlyReadOnlyCanonicalRepository(t *testing.T)
func TestServiceDoesNotPersistManagerReport(t *testing.T)
func TestServiceRepeatedQueryReevaluatesCurrentAuthority(t *testing.T)
func TestServicePreservesBoundedAdmissionAndCounterevidence(t *testing.T)
```

- [ ] **Step 5: Run service tests and verify red**

Run:

```bash
go test ./internal/analysis ./internal/cli \
  -run 'TestService|TestAnalyze' -count=1
```

Expected: FAIL because `Repository` still requires `FindCompleted` and `CompleteAnalysis`.

- [ ] **Step 6: Remove manager-specific durable cache behavior**

Delete `FindCompleted`, `CompleteAnalysis`, `Completion`, durable manager-report identity, and signal/input kinds that exist only for manager tables. The report remains a bounded on-demand CLI result. Do not invent a generic derived-result cache; Plan D may introduce one only after a generic contract is approved.

- [ ] **Step 7: Run task verification**

Run:

```bash
go test ./internal/analysis ./internal/cli -count=1
make fmt
git diff --check
```

Expected: all commands pass.

- [ ] **Step 8: Commit**

```bash
git add internal/analysis internal/cli
git commit -m "Query manager confidence from canonical evidence"
```

---

### Task 10: Add Fingerprints, Structured Status, Doctor, and Database Commands

**Files:**
- Create: `adapters/postgres/migration/fingerprint.go`
- Create: `adapters/postgres/migration/fingerprint_test.go`
- Create: `adapters/postgres/migration/status.go`
- Create: `adapters/postgres/migration/status_test.go`
- Create: `adapters/postgres/migration/status_integration_test.go`
- Create: `adapters/postgres/cmd/schema-fingerprint/main.go`
- Modify: `adapters/postgres/migration/manifest.go`
- Modify: `adapters/postgres/migration/manifest_test.go`
- Modify: `adapters/postgres/migration/migrator_test.go`
- Modify: `adapters/postgres/migration/migrator_integration_test.go`
- Modify: `adapters/postgres/coremigrations/manifest.go`
- Modify: `adapters/postgres/directorymigrations/manifest.go`
- Create: `internal/cli/database.go`
- Create: `internal/cli/database_test.go`
- Create: `internal/localdb/reset.go`
- Create: `internal/localdb/reset_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/app/execute.go`
- Modify: `internal/app/execute_test.go`
- Modify: `internal/doctor/providers.go`
- Modify: `internal/doctor/providers_test.go`
- Modify: `internal/doctor/postgres_integration_test.go`
- Modify: `internal/doctor/service.go`
- Modify: `internal/doctor/service_test.go`
- Modify: `internal/cli/doctor_test.go`
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`
- Modify: `db/init/010-create-application-role.sh`
- Modify: `.env.example`
- Modify: `Makefile`

**Interfaces:**
- Consumes: complete core/directory manifests and current command router.
- Produces:

```go
type CatalogObject struct {
	Kind       string
	Schema     string
	Parent     string
	Name       string
	Definition string
}

func Fingerprint([]CatalogObject) [sha256.Size]byte
```

Task 10 extends `migration.Manifest` with:

```go
ExpectedFingerprint [sha256.Size]byte
```

From this task onward manifest validation requires a nonzero expected
fingerprint, and `Inspector.Status` compares it with the live fingerprint of
the manifest's owned schema trees plus exact owned objects, including its
ledger.
Extend `LoadManifest` with the expected fingerprint argument and update every
synthetic manifest fixture; no caller may assign the field after validation.

```go
type State string

const (
	StateAbsent           State = "absent"
	StatePending          State = "pending"
	StateCurrent          State = "current"
	StateChecksumMismatch State = "checksum_mismatch"
	StateSchemaDrift      State = "schema_drift"
)

type ScopeStatus struct {
	Scope           Scope
	State           State
	AppliedVersion  int64
	ExpectedVersion int64
	Configured      bool
}

type Inspector struct {
	DatabaseURL string
	Manifests   []Manifest
	Configured  []Scope
}

func (inspector Inspector) Status(context.Context) ([]ScopeStatus, error)
```

Configuration adds safe, typed fields for
`STACKS_MIGRATION_DATABASE_URL`, `STACKS_DATABASE_SCOPES`, and
`STACKS_DATABASE_APP_ROLE`. Core must appear exactly once in scopes; directory
is optional; unknown or duplicate scopes fail before connections. Enabling
directory enrichment without selecting the directory database scope also
fails before any database or provider construction. Disabled directory remains
healthy with a core-only scope.

- [ ] **Step 1: Write failing fingerprint and status tests**

Fingerprint schemas, tables, columns/type/nullability/default, constraints,
indexes, functions, and triggers. Sort on every `CatalogObject` field and
length-prefix serialization. Exclude OIDs, owners, and ACL order. Tests prove
schema-tree ownership includes descendants, exact-object ownership includes
only the selected object, and changing either scope's ledger shape changes
only that scope's fingerprint. Status precedence is absent, checksum mismatch,
pending, schema drift, current.

- [ ] **Step 2: Run fingerprint/status tests and verify red**

Run:

```bash
(cd adapters/postgres && GOWORK=off go test ./migration \
  -run 'TestFingerprint|TestInspector|TestStatus' -count=1)
```

Expected: FAIL because catalog fingerprinting and complete status do not exist.

- [ ] **Step 3: Implement PG18 semantic fingerprints**

The safe generator reads the migration URL from environment and prints only
`scope=<scope> sha256=<hex>`. Before making the expected fingerprint mandatory,
use the Task 3 migrator to install each scope in a newly created PostgreSQL 18
temporary database and run the generator against it. Then add the captured
values, extend `LoadManifest`, enable nonzero validation, update every fixture,
and prove another clean install matches. Do not add a provisional zero or
"skip fingerprint" runtime mode. Mutating one owned column, default,
constraint, index, function, or trigger must report `schema_drift`.

- [ ] **Step 4: Write failing database command and doctor tests**

Add top-level commands matching the existing router:

```go
const (
	CommandDBMigrate Command = "db-migrate"
	CommandDBStatus  Command = "db-status"
	CommandDBReset   Command = "db-reset"
)
```

`db-status` prints only:

```text
scope=core state=current applied=3 expected=3 configured=true
scope=directory state=absent applied=0 expected=1 configured=false
```

Doctor exposes stable checks `database.migrations.core` and `database.migrations.directory`. It remains read-only and distinguishes pending, checksum mismatch, and drift. Directory absence is healthy when not selected and actionable when selected.

Add:

```go
func TestDirectoryProviderRequiresDirectoryDatabaseScope(t *testing.T)
func TestDisabledDirectoryAcceptsCoreOnlyDatabaseScope(t *testing.T)
func TestInvalidDirectoryScopeCombinationConstructsNoDependencies(t *testing.T)
```

Add span tests proving `db-migrate`, `db-status`, and `db-reset` record only scope, version, state, duration, and bounded outcome. They must not record database URLs, role names, SQL text, volume paths containing user input, document IDs, or provider configuration.

- [ ] **Step 5: Run commands/doctor tests and verify red**

Run:

```bash
go test ./internal/config ./internal/app ./internal/cli ./internal/doctor ./cmd/stacks \
  -run 'Test.*(DBMigrate|DBStatus|MigrationStatus|Scope|Drift|Checksum)' -count=1
```

Expected: FAIL because the commands and structured doctor contract are missing.

- [ ] **Step 6: Implement commands without provider construction**

`db-migrate` uses the admin migration URL and selected manifests. `db-status` uses the application URL and always inspects both known manifests. Composition tests record zero Drive, directory-provider, Bedrock, Anthropic, OpenAI, source, and model construction.

- [ ] **Step 7: Write failing guarded-reset tests**

`internal/localdb.Resetter` validates:

```text
service: postgres
volume key: postgres_data
mount: /var/lib/postgresql
confirmation: delete-local-stacks-postgres
```

Reject wrong confirmation, non-loopback database URL, absent/ambiguous service, absent/ambiguous volume, wrong mount, or mismatched Compose labels before any remove call. Output includes the exact service and resolved volume plus an unrecoverable-data warning, but never a URL or password.

- [ ] **Step 8: Run reset tests and verify red**

Run:

```bash
go test ./internal/localdb ./internal/cli \
  -run 'Test.*Reset' -count=1
```

Expected: FAIL because `Resetter` is missing.

- [ ] **Step 9: Implement exact local reset**

Use a small injected command-runner interface. Resolve the actual Compose volume through service/container inspection and verify project/volume labels and mount destination. Stop/remove only `postgres`, remove only that resolved volume, recreate/wait only for `postgres`, then apply selected embedded migrations. Never use `docker compose down --volumes`.

Make targets become:

```text
make db-migrate
make db-status
make db-reset CONFIRM=delete-local-stacks-postgres
```

Remove `GOOSE_VERSION` and Goose invocation.
Now that every database target uses the embedded migrator, reduce
`db/init/010-create-application-role.sh` to role/database bootstrap:
retain role creation, database connect, public-schema revocation, and
`search_path=stacks_core`; remove creation of `stacks`, `extensions`, and their
broad default privileges.
Update `make test-integration` to run the adapter module's gated migration/repository packages plus the root ingestion, directory, analysis, and doctor integration packages with `STACKS_TEST_DATABASE_URL` and `STACKS_TEST_MIGRATION_DATABASE_URL`.
The target must require `ENV_FILE`, source it with `set -a` exactly once
without echoing values, validate both test URLs after sourcing, and pass them
to each root or nested-module test command. This keeps local
`direnv exec . make test-integration` and CI
`make test-integration ENV_FILE=.env.ci` on the same path.

- [ ] **Step 10: Run task verification**

Run:

```bash
(cd adapters/postgres && GOWORK=off go test ./... -count=1)
go test ./internal/config ./internal/app ./internal/cli ./internal/doctor ./internal/localdb ./cmd/stacks -count=1
make fmt
git diff --check
```

Expected: all commands pass.

- [ ] **Step 11: Commit**

```bash
git add adapters/postgres internal/config internal/app internal/cli internal/doctor internal/localdb cmd/stacks db/init/010-create-application-role.sh .env.example Makefile
git commit -m "Operate scoped PostgreSQL migrations"
```

---

### Task 11: Switch Composition Once and Retire the Legacy Path

**Files:**
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`
- Modify: `internal/config/config.go`
- Move: `internal/config/poc.go` to `internal/config/application.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/cli/storage.go`
- Modify: `internal/cli/storage_test.go`
- Modify: `internal/ingest/postgres.go`
- Modify: `internal/directory/postgres.go`
- Modify: `internal/analysis/postgres.go`
- Modify: `core/observation/confidence.go`
- Modify: `core/observation/confidence_test.go`
- Modify: `core/observation/observation.go`
- Modify: `core/observation/observation_test.go`
- Modify: `core/temporal/aggregation.go`
- Modify: `core/temporal/aggregation_test.go`
- Modify: `core/temporal/comparison.go`
- Modify: `core/temporal/comparison_test.go`
- Delete: the complete `internal/storage/` directory listed in the File Map only after every canonical consumer and replacement test passes.
- Delete: `db/migrations/00001_enable_vector.sql` through `db/migrations/00012_google_directory_identity.sql`.
- Delete: obsolete legacy migration and compatibility tests.

**Interfaces:**
- Consumes: all canonical repositories and commands from Tasks 3–10.
- Produces: one runtime composition using only canonical core/directory scopes.

Rename:

```go
PoCSettings       -> ApplicationSettings
Settings.PoC      -> Settings.Application
pocCommandRuntime -> commandRuntime
pocCommandProvider -> commandProvider
```

Replace `StorageReviewStore`'s concrete `*storage.EntityRepository` dependency
with a small consumer-owned `canonicalReviewRepository` interface in
`internal/cli`. Its methods use adapter-neutral entity, proposal, decision,
and append-command values; the implementation built by composition delegates
to the PostgreSQL adapter. Generate decision and entity IDs plus canonical
recorded times at the application boundary through injected `IDGenerator` and
`Clock` capabilities. The adapter and core contain no `uuid.NewString` or
`time.Now`.

Keep temporary manager-confidence values in:

```go
type ManagerConfidenceSettings struct {
	PromptVersion   string
	EmployeeEntityID string
	ManagerEntityID  string
}
```

This makes the use case visibly temporary without changing its existing environment names before Plan D removes the command.

- [ ] **Step 1: Write failing canonical composition tests**

Add:

```go
func TestCoreCommandsOpenOnlyCanonicalPostgresRepositories(t *testing.T)
func TestCoreOnlyCompositionConstructsNoDirectoryBoundary(t *testing.T)
func TestEnabledDirectoryUsesOptionalCanonicalScope(t *testing.T)
func TestEnabledDirectoryWithoutScopeFailsBeforeConstruction(t *testing.T)
func TestAnalyzeUsesReadOnlyCanonicalQueryRepository(t *testing.T)
func TestDatabaseCommandsConstructNoProviders(t *testing.T)
```

- [ ] **Step 2: Run composition tests and verify red**

Run:

```bash
go test ./cmd/stacks ./internal/config ./internal/cli \
  -run 'Test(Core|EnabledDirectory|AnalyzeUses|DatabaseCommands)' -count=1
```

Expected: FAIL because composition still constructs legacy `internal/storage` repositories.

- [ ] **Step 3: Switch composition to the adapter module**

Open one canonical `postgres.Database` owner per command. Build root consumer adapters for ingestion, review, optional directory, and temporary analysis. Preserve restricted-disclosure checks before source/storage/model construction and preserve cancellation identity.

- [ ] **Step 4: Remove legacy-only core states**

Delete:

```text
DocumentVersionInput.ProviderRevision
DocumentVersion.ProviderRevision()
DocumentVersion.LegacyRevisionInclusiveDigest()
DocumentVersion.LegacyRevisionInclusiveDigestFor()
ConfidenceUnspecifiedLegacy
NewLegacyConfidence
Derivation.LegacyUnversioned
ObservationInput.LegacyUncited
Observation.LegacyUncited()
temporal.UnresolvedLegacyUncited
legacy-uncited aggregation/comparison fields
```

Update tests so every observation is cited, every derivation is versioned, and every stored confidence declares its active scale.

- [ ] **Step 5: Run core and application tests before deletion**

Run:

```bash
(cd core && GOWORK=off go test ./... -count=1)
go test ./internal/ingest ./internal/directory ./internal/analysis ./internal/cli ./cmd/stacks -count=1
```

Expected: all commands pass through canonical composition.

- [ ] **Step 6: Delete the retired writers, managers, migrations, and tests**

Remove the complete `internal/storage/` directory after its ingestion,
directory, analysis, entity, and review consumers pass through canonical root
adapters. Also remove Goose migration SQL and upgrade/UUID/digest-v1
compatibility fixtures. Preserve historical design documents. The task is not
complete if even a seemingly generic legacy repository remains under
`internal/storage`.

- [ ] **Step 7: Run the deletion audit**

Run:

```bash
rg -n 'goose_db_version|pressly/goose|GOOSE_VERSION|interaction_signals|signal_evidence|analysis_runs|currently_admissible|LegacyUncited|LegacyUnversioned|unspecified_legacy|putLegacyObservation|(FROM|INTO|UPDATE|JOIN|TABLE|SCHEMA)[[:space:]]+stacks\\.' \
  Makefile README.md db internal cmd adapters core .github
```

Expected: no runtime, migration, test, README, Makefile, or current-plan matches. Historical Plan B documentation is the only exemption.

- [ ] **Step 8: Run task verification**

Run:

```bash
make fmt
make test
make modules-check
make staticcheck
make build
git diff --check
```

Expected: all commands pass.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "Retire the legacy PostgreSQL path"
```

---

### Task 12: Synchronize Documentation, CI, and Full Local Acceptance

**Files:**
- Modify: `README.md`
- Modify: `.env.example`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`
- Modify: `compose.yaml` only if command comments or health wiring are stale.
- Modify: `docs/superpowers/specs/2026-07-25-canonical-postgres-reset-design.md` status only after every completion criterion passes.

**Interfaces:**
- Consumes: the final canonical application and exact repository commands.
- Produces: truthful operator instructions and reproducible local/CI verification.

- [ ] **Step 1: Write the documentation/command acceptance checklist**

The README must state:

- migrations `00001`–`00012` have no upgrade path;
- the reset irrecoverably deletes only local Compose PostgreSQL data;
- the exact confirmation string;
- core is required and directory optional;
- manager confidence is not a migration or installation scope;
- Drive and model providers are runtime adapters;
- pgvector is available in the image but not installed by core;
- status meanings and doctor remediation;
- every required environment variable with a safe placeholder; and
- local PostgreSQL success does not validate live Drive, Workspace Directory, Bedrock, Anthropic, OpenAI, or private-corpus acceptance.
- Plan C does not enable cloud logging; existing local observability privacy rules remain in force.

- [ ] **Step 2: Update CI to use repository commands**

CI uses synthetic passwords and a temporary ignored `.env.ci`, then runs
`make db-up ENV_FILE=.env.ci`, `make db-migrate ENV_FILE=.env.ci`,
`make db-status ENV_FILE=.env.ci`, and
`make test-integration ENV_FILE=.env.ci`. The integration target sources the
safe test URLs from that file. CI invokes no provider and stores no real
credential.

- [ ] **Step 3: Run deterministic gates**

Run:

```bash
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
git diff --check
```

Expected: every command exits zero.

- [ ] **Step 4: Run the explicit clean local reset**

Run:

```bash
direnv exec . make db-reset CONFIRM=delete-local-stacks-postgres
direnv exec . make db-status
```

Expected core-only status:

```text
scope=core state=current applied=3 expected=3 configured=true
scope=directory state=absent applied=0 expected=1 configured=false
```

If local `.env` explicitly selects directory, expected directory status is `current`, not `absent`.

- [ ] **Step 5: Prove repeat migration is a no-op**

Record ledger `applied_at` values through a bounded test helper, run:

```bash
direnv exec . make db-migrate
direnv exec . make db-status
```

Then assert the ledger timestamps are unchanged and status remains current.

- [ ] **Step 6: Run every PostgreSQL integration and race gate**

Run:

```bash
direnv exec . make test-integration
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test -race ./... \
  -run "TestMigrator|TestExtraction|TestIdentity|TestAdmission|TestDirectory" -count=1'
```

Expected: every gated test passes with PostgreSQL 18.

- [ ] **Step 7: Exercise doctor in both scope modes**

Exercise the database-only doctor integration boundary without invoking the
full provider doctor:

```bash
direnv exec . go test ./internal/doctor \
  -run 'TestPostgresProbeReports(CoreOnly|ConfiguredDirectory)MigrationStatus' \
  -count=1
```

The test creates one core-only temporary database and one core-plus-directory
temporary database through the migration test helper. Confirm core
non-current fails, optional directory absence is healthy when disabled,
directory absence is actionable when selected, and the doctor path performs
no writes and constructs or invokes no source, directory, or model provider.

- [ ] **Step 8: Run both synthetic longitudinal acceptance scenarios**

Use only committed synthetic fixtures and fake model responses. Prove the temporary command reads canonical observations, preserves chronology/support/counterevidence, and performs no manager-specific SQL write.

Explicitly rerun the non-manager scenario committed in Task 6:

```bash
direnv exec . sh -c \
  'cd adapters/postgres && GOWORK=off go test ./... \
  -run "TestProjectCommitmentChronologyPreservesChangeUncertaintyAndCounterevidence" \
  -count=1'
```

- [ ] **Step 9: Request independent whole-branch review**

The reviewer checks the complete diff against the approved spec, runs the deletion audit, verifies core has no adapter imports, verifies the adapter has no provider/manager imports, and reviews reset target safety and database permissions.

- [ ] **Step 10: Fix review findings test-first and rerun all gates**

Every concrete defect receives a failing regression test, minimal fix, focused
green run, and the complete deterministic/PostgreSQL gate set above. Commit
each coherent review fix before changing the spec status; do not hide code
fixes in the final documentation commit.

- [ ] **Step 11: Mark the Plan C spec implemented only after evidence exists**

Change the spec status from `Approved design` to `Implemented` and append the
already-known Task 11 canonical-cutover commit plus the verification record.
Do not try to predict or amend the pending documentation commit. Keep live
provider/private-corpus acceptance explicitly unvalidated.

- [ ] **Step 12: Commit**

```bash
git add README.md .env.example .github/workflows/ci.yml Makefile compose.yaml docs/superpowers/specs/2026-07-25-canonical-postgres-reset-design.md
git commit -m "Document canonical PostgreSQL operations"
```

---

## Final Review Checklist

- [ ] Core-only install contains no directory, manager-confidence, provider, OAuth, or vector objects.
- [ ] Directory has an independent ledger and only directory-to-core dependencies.
- [ ] Every core ID is opaque text and every persisted timestamp is canonical UTC microseconds.
- [ ] Every immutable payload and versioned digest is stable under exact retry.
- [ ] Every observation term, temporal shape, evidence role, derivation field, epistemic state, and supported confidence scale round-trips.
- [ ] Identity and admission corrections append and supersede without rewriting history or branching authority.
- [ ] Extraction leases, failure, reclaim, retry, completed resume, and full completion rollback pass live PostgreSQL tests.
- [ ] Name-only resolution remains review-only; unique exact accepted work email is the only automatic identity path.
- [ ] Directory failure is additive and reviewer decisions remain authoritative.
- [ ] Manager confidence has no database object or migration scope and reads canonical state only.
- [ ] A second non-manager project-commitment scenario proves chronology, recorded cutoffs, uncertainty, change, and counterevidence over canonical PostgreSQL.
- [ ] `db-migrate`, `db-status`, `db-reset`, and the doctor database-check boundary are bounded, secret-safe, and provider-free; the explicitly invoked full doctor may retain its separate provider readiness checks.
- [ ] The destructive reset resolves and validates the exact local Compose service and volume before removal.
- [ ] No legacy writable path, Goose reference, UUID-bound core schema, mutable admission flag, manager table, or compatibility flag remains outside historical docs.
- [ ] Formatting, full tests, integration tests, race tests, Staticcheck, build, module checks, migration status, repeat migration, reset/install, doctor, and independent whole-branch review all pass.
- [ ] Final report distinguishes passing deterministic/local PostgreSQL acceptance from unvalidated live Drive, Workspace Directory, Bedrock, Anthropic, OpenAI, and private-corpus acceptance.
