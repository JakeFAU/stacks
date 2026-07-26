# Canonical Observation over Legacy PostgreSQL Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `core/observation.Observation` the sole active observation contract at the PostgreSQL boundary while preserving every characterized behavior and byte of the frozen `00001`-through-`00012` schema.

**Architecture:** Add one package-local legacy codec that converts between canonical observations and the existing observation, evidence, extraction-run, and manager-signal rows. Route direct graph writes and ingestion completion through that codec, preserve digest v1 with private relational-origin metadata, carry the extraction run's once-recorded time into canonical construction, and replace completed-run early success with a read-only full write-set comparison.

**Tech Stack:** Go 1.26.0, `github.com/JakeFAU/stacks/core/observation`, PostgreSQL 17 with pgvector, pgx v5, the standard Go testing package, repository-pinned Staticcheck 2026.1, Goose v3.27.1, and OrbStack for local integration.

## Global Constraints

- Work only in `/Users/jacob/dev/personal/stacks` on `codex/canonical-observation-storage`; preserve unrelated work.
- Use a fresh implementation subagent for each task, an independent specification reviewer and code-quality reviewer for each task, and an independent whole-branch reviewer after final verification.
- Follow the approved design in `docs/superpowers/specs/2026-07-25-canonical-observation-legacy-postgres-design.md`.
- Do not edit, rename, reorder, or re-checksum `db/migrations/00001_enable_vector.sql` through `db/migrations/00012_google_directory_identity.sql`.
- Do not add migration `00013`, a feature flag, a dual writer, a second observation domain type, or a new transaction boundary.
- `core/observation` owns domain validity; storage owns only SQL representation, relational compatibility, idempotency, and conflict checks.
- Preserve digest v1 bytes exactly; its evidence input is the exact sorted `observation_evidence` origin, never the canonical evidence union.
- Preserve the current active dual evidence storage: `observation_evidence` receives the statement/support/contradiction union and `signal_evidence` retains exact roles.
- Preserve manager-signal category, direction, rationale, model, prompt, confidence, transcript-evidence enforcement, and notes-only rejection.
- Source manager confidence remains validated as `unit_interval`; the generic durable canonical observation deliberately stores the same number as `unspecified_legacy`.
- Never infer a generic confidence scale from signal presence or numeric range.
- `PrepareVersion` stores and returns one UTC microsecond `RecordedAt`; completion and retry paths must not choose a new observation time.
- `core/observation.NewObservation` normalizes the supplied instant to UTC; storage enforces microsecond precision on that canonical instant and does not claim to recover the caller's original timezone.
- Historical uncited rows remain readable with `LegacyUncited`; active uncited writes remain rejected.
- Error messages and telemetry must not contain predicates, evidence text, rationale, prompts, model output, document contents, raw SQL values, secrets, or private source payloads.
- Use only synthetic fixtures and reserved domains in tests.
- Do not invoke Google Drive, Workspace Directory, Bedrock, Anthropic, or OpenAI.
- Do not read or print `.env`, OAuth files, credentials, tokens, or private document contents.
- Do not deploy, enable cloud logging, push, publish, open a pull request, merge, or tag without explicit user approval.

---

## File map

### New files

- `internal/storage/observation_codec.go`: package-local legacy row carriers, canonical encode/decode, bounded error types, exact evidence-origin metadata, timestamp representability, and digest v1.
- `internal/storage/observation_codec_test.go`: pure codec, representation, confidence, evidence-role, exact-string, timestamp, and digest-golden tests.
- `internal/storage/observation_postgres.go`: SQL loading, canonical direct writes, full stable-ID retry comparison, and unique-digest conflict translation.
- `internal/storage/observation_postgres_integration_test.go`: PostgreSQL read/write, corrupt-row, idempotency, and conflict matrix for canonical observations.
- `internal/storage/completed_write_set.go`: read-only reconstruction and comparison for same-owner retries of completed extraction runs.

### Modified files

- `internal/storage/graph.go`: retain only the manager-signal vertical and shared graph transaction helper; remove the parallel durable observation DTO, observation validator, observation digest, and old writer.
- `internal/storage/graph_test.go`: retain signal digest tests and move observation digest claims to codec tests.
- `internal/storage/observation_compatibility_integration_test.go`: make the existing legacy characterization assert decoded canonical values and private compatibility state.
- `internal/storage/documents.go`: return persisted run time, construct canonical observations after UUID resolution, call the codec writer, and invoke completed-write-set comparison.
- `internal/storage/documents_test.go`: add bounded error and pre-transaction validation coverage.
- `internal/storage/integration_test.go`: update direct graph callers and add ingestion time, confidence, evidence-origin, rollback, and completed-retry integration cases.
- `internal/ingest/service.go`: add retry-stable run time to `VersionState` and carry canonical-compatible observation draft values.
- `internal/ingest/service_test.go`: prove source confidence validation, the deliberate generic scale downgrade boundary, and retry-stable recorded time; update repository fakes.
- `internal/ingest/validate.go` and `internal/ingest/validate_test.go`: validate only draft references and source-score bounds without duplicating canonical observation invariants.
- `internal/source/drive/chronology_test.go`, `cmd/stacks/main_test.go`, and any compile-time `ingest.Repository` fakes: return or tolerate `VersionState.RecordedAt`.
- `README.md`: state that the application PostgreSQL boundary now consumes canonical observations through the frozen-schema compatibility codec, without claiming full canonical storage.

### Read but never modify

- `db/migrations/00001_enable_vector.sql` through `db/migrations/00012_google_directory_identity.sql`.
- `.env`, `.envrc`, `.secrets/**`, Google OAuth material, and private source data.

---

### Task 1: Build the pure legacy observation codec and pin digest v1

**Files:**
- Create: `internal/storage/observation_codec.go`
- Create: `internal/storage/observation_codec_test.go`
- Read without modifying: `internal/storage/graph.go`
- Read without modifying: `internal/storage/graph_test.go`
- Read without modifying: `db/migrations/00002_manager_confidence_poc.sql`

**Interfaces:**
- Consumes: `observation.Observation`, `observation.Term`, `observation.TemporalExtent`, `observation.Confidence`, existing `SignalInput`, and `SignalEvidenceInput`.
- Produces:

```go
var (
	ErrObservationNotRepresentable = errors.New("observation is not representable by legacy PostgreSQL")
	ErrObservationCompatibility    = errors.New("legacy observation is incompatible")
	ErrObservationConflict         = errors.New("observation conflicts with stored state")
)

const legacyPostgresTimestampPrecision = time.Microsecond

type legacyObservationCompatibility struct {
	observationEvidenceOrigin []evidence.EvidenceID
	storedDigest              [sha256.Size]byte
}

type owningExtractionRun struct {
	ID            string
	ModelID       string
	PromptVersion string
	RecordedAt    time.Time
}

type legacyObservationRow struct {
	ID, ExtractionRunID                         string
	SubjectEntityID, ObjectEntityID             string
	SubjectMentionID, ObjectMentionID           string
	Predicate, Derivation, EpistemicStatus       string
	ValidStart, ValidEnd                         *time.Time
	RecordedAt                                  time.Time
	Confidence                                  *float64
	Digest                                      [sha256.Size]byte
}

type legacySignalState struct {
	Input    SignalInput
	Evidence []SignalEvidenceInput
	Digest   [sha256.Size]byte
}

type decodedLegacyObservation struct {
	Observation  observation.Observation
	Signal       *legacySignalState
	Compatibility legacyObservationCompatibility
}

type legacyObservationWrite struct {
	Row           legacyObservationRow
	Origin        []evidence.EvidenceID
	Signal        *legacySignalState
}

func decodeLegacyObservation(legacyObservationRow, []evidence.EvidenceID, *legacySignalState, *owningExtractionRun) (decodedLegacyObservation, error)
func encodeLegacyObservation(observation.Observation, legacyObservationCompatibility, *owningExtractionRun, *legacySignalState) (legacyObservationWrite, error)
func computeObservationDigestV1(legacyObservationWrite) ([sha256.Size]byte, error)
```

- Later tasks may add private comparison helpers, but they must not add another observation DTO or export compatibility metadata.

- [ ] **Step 1: Write failing codec error and timestamp tests**

Add tests with these exact names:

```go
func TestObservationCompatibilityErrorSupportsErrorsIs(t *testing.T)
func TestEncodeLegacyObservationRejectsUnrepresentableRecordedAt(t *testing.T)
func TestEncodeLegacyObservationRejectsUnrepresentableValidTime(t *testing.T)
```

The error test must wrap each sentinel with operation context and prove `errors.Is` still succeeds. The timestamp test must use:

```go
notMicrosecond := time.Date(2026, time.July, 25, 12, 0, 0, 1, time.UTC)
```

and assert the fixed reason code without including the predicate:

```go
if !errors.Is(err, ErrObservationNotRepresentable) ||
	!strings.Contains(err.Error(), "recorded_at_not_representable") ||
	strings.Contains(err.Error(), privatePredicate) {
	t.Fatalf("encode error = %v", err)
}
```

The valid-time precision test must apply the same one-nanosecond value to an
`Instant`, a `Since` start, a `During` start, and a `During` end. Every case
must return `ErrObservationNotRepresentable` before SQL rather than accepting
PostgreSQL truncation.

- [ ] **Step 2: Run the focused tests and verify the missing codec fails**

Run:

```bash
go test ./internal/storage -run 'TestObservationCompatibilityError|TestEncodeLegacyObservationRejectsUnrepresentable(RecordedAt|ValidTime)' -count=1
```

Expected: FAIL because the codec sentinels and encoder do not exist.

- [ ] **Step 3: Implement bounded errors and exact timestamp checks**

Use an unexported error carrying only kind, fixed reason, and optional stable ID:

```go
type observationBoundaryError struct {
	kind          error
	reason        string
	observationID string
	runID         string
}

func (err *observationBoundaryError) Error() string {
	switch {
	case err.observationID != "":
		return fmt.Sprintf("observation boundary %q: %s", err.observationID, err.reason)
	case err.runID != "":
		return fmt.Sprintf("observation boundary run %q: %s", err.runID, err.reason)
	default:
		return fmt.Sprintf("observation boundary: %s", err.reason)
	}
}

func (err *observationBoundaryError) Unwrap() error { return err.kind }

func newObservationBoundaryError(kind error, reason, observationID string) error {
	return &observationBoundaryError{
		kind: kind, reason: reason, observationID: observationID,
	}
}

func newCompletionBoundaryError(kind error, reason, runID string) error {
	return &observationBoundaryError{
		kind: kind, reason: reason, runID: runID,
	}
}

func legacyTimestampRepresentable(value time.Time) bool {
	return !value.IsZero() &&
		value.Equal(value.Truncate(legacyPostgresTimestampPrecision))
}
```

`observation.NewObservation` has already normalized `RecordedAt` to UTC. Do
not add a branch that pretends storage can recover the caller's original
location.

Use fixed reason constants for at least:

```go
const (
	reasonObservationOriginMismatch          = "observation_origin_mismatch"
	reasonObservationDigestMismatch          = "observation_digest_mismatch"
	reasonConfidenceScaleNotRepresentable    = "confidence_scale_not_representable"
	reasonRecordedAtNotRepresentable         = "recorded_at_not_representable"
	reasonCompletionOwnerMismatch            = "completion_owner_mismatch"
	reasonCompletionWriteSetMismatch         = "completion_write_set_mismatch"
)
```

- [ ] **Step 4: Write failing term, time, confidence, derivation, and evidence representation tests**

Add table tests:

```go
func TestLegacyObservationCodecMapsAllTermShapes(t *testing.T)
func TestLegacyObservationCodecMapsLegacyValidTime(t *testing.T)
func TestEncodeLegacyObservationRejectsUnsupportedCanonicalShapes(t *testing.T)
func TestLegacyObservationCodecPreservesConfidenceWithoutInventingScale(t *testing.T)
func TestLegacyObservationCodecPreservesEvidencePairsAndPrivateOrigin(t *testing.T)
func TestLegacyObservationCodecPreservesExactPredicateAndDerivationBytes(t *testing.T)
func TestLegacyObservationCodecRecoversDerivationFromOwningRun(t *testing.T)
func TestLegacyObservationCodecAllowsHistoricalUncitedDecodeOnly(t *testing.T)
```

The term table must cover `Absent`, `Mention`, `Entity` without grounding, and
`Entity` with grounding independently for subject and object. The valid-time
table must cover null/null, start/null, start/start, and start/later.
Unsupported encoding cases must include `Text`, `Until`, `Window`, direct
`unit_interval`, active uncited, active `LegacyUnversioned`, missing owning
run, model without prompt, prompt without model, owning-run ID mismatch, model
mismatch, prompt/version mismatch, and evidence-role ownership mismatch.

The confidence table must assert:

```go
func decodeFixtureWithObservationConfidence(
	t *testing.T,
	value float64,
) decodedLegacyObservation

for _, value := range []float64{-2.5, 0, 0.75, 1, 4.25} {
	decoded := decodeFixtureWithObservationConfidence(t, value)
	confidence, ok := decoded.Observation.Confidence()
	if !ok || confidence.Value() != value ||
		confidence.Scale() != observation.ConfidenceUnspecifiedLegacy {
		t.Fatalf("decoded confidence = (%v, %v)", confidence, ok)
	}
}
```

It must separately prove null observation confidence plus non-null signal confidence and unequal generic/signal confidence values remain valid separate data.

- [ ] **Step 5: Run the codec matrix and verify it fails**

Run:

```bash
go test ./internal/storage -run 'TestLegacyObservationCodec|TestEncodeLegacyObservationRejectsUnsupported' -count=1
```

Expected: FAIL because legacy mapping and representability are not implemented.

- [ ] **Step 6: Implement canonical encode/decode mapping**

Implement small private helpers with these signatures:

```go
func decodeLegacyTerm(entityID, mentionID string) (observation.Term, error)
func encodeLegacyTerm(term observation.Term) (entityID, mentionID string, err error)
func decodeLegacyValidTime(start, end *time.Time) (observation.TemporalExtent, error)
func encodeLegacyValidTime(value observation.TemporalExtent) (start, end *time.Time, err error)
func decodeLegacyConfidence(value *float64) (*observation.Confidence, error)
func canonicalEvidenceLinks(origin []evidence.EvidenceID, signal *legacySignalState) ([]observation.EvidenceLink, error)
```

`canonicalEvidenceLinks` must append supporting links for the exact origin, append every signal role link, deduplicate only exact `(EvidenceID, Role)` pairs, and rely on `observation.NewObservation` for final stable sorting and domain validation.

`encodeLegacyObservation` must reject unsupported forms before returning a write. It may inspect canonical terms, time, confidence, derivation, and evidence, but must not repeat `core/observation` validation already guaranteed by construction.

- [ ] **Step 7: Write failing digest v1 golden tests**

Add:

```go
func TestComputeObservationDigestV1GoldenBytes(t *testing.T)
func TestComputeObservationDigestV1IgnoresCanonicalFieldsAbsentFromV1(t *testing.T)
func TestComputeObservationDigestV1ChangesOnlyWithLegacyOrigin(t *testing.T)
```

Pin lowercase hexadecimal SHA-256 constants for at least:

1. all nullable IDs empty, unknown valid time, absent confidence, one origin;
2. entity plus grounding mention, instant, confidence `0.75`, two unsorted duplicate origins;
3. extraction-run ID, bounded interval, confidence `-2.5`, and no origin.

The invariance test must change `RecordedAt`, signal-only evidence, signal roles, and canonical evidence pairs without changing the private origin, then assert v1 is unchanged. The origin test must add one UUID to the private origin and assert v1 changes.

- [ ] **Step 8: Run the new digest tests and verify they fail**

Run:

```bash
go test ./internal/storage -run 'TestComputeObservationDigestCanonicalizesSemanticIdentity|TestComputeObservationDigestV1' -count=1
```

Expected: FAIL because `computeObservationDigestV1` is not implemented.

- [ ] **Step 9: Implement digest v1 in the codec**

Implement v1 as the exact NUL-separated field order from the design. Use:

```go
func appendLegacyTime(fields []string, value *time.Time) []string
func appendLegacyConfidence(fields []string, value *float64) []string
```

with `UTC().Format(time.RFC3339Nano)` and
`fmt.Sprintf("%.17g", *value)`. Canonicalize UUID spelling and
sort/deduplicate only the supplied origin IDs. Do not include stable
observation ID, `RecordedAt`, signal-only evidence, or roles.

While the old writer still exists, add one bridge test that constructs
equivalent old and new fixtures and proves exported `ComputeObservationDigest`
and `computeObservationDigestV1` return identical bytes. Do not remove or edit
the old digest, writer, canonicalization, or validation helpers in Task 1;
Task 3 removes them only after production callers switch atomically.

- [ ] **Step 10: Run Task 1 tests and commit**

Run:

```bash
gofmt -w internal/storage/observation_codec.go internal/storage/observation_codec_test.go
go test ./internal/storage -run 'TestObservationCompatibilityError|TestLegacyObservationCodec|TestEncodeLegacyObservation|TestComputeObservationDigestV1|TestComputeSignalDigest' -count=1
git diff --check
```

Expected: PASS.

Commit:

```bash
git add internal/storage/observation_codec.go internal/storage/observation_codec_test.go
git commit -m "Add legacy observation codec"
```

---

### Task 2: Decode and characterize legacy PostgreSQL observations canonically

**Files:**
- Create: `internal/storage/observation_postgres.go`
- Create: `internal/storage/observation_postgres_integration_test.go`
- Modify: `internal/storage/observation_compatibility_integration_test.go`

**Interfaces:**
- Consumes: Task 1 codec and the frozen `stacks.observations`, `stacks.observation_evidence`, `stacks.interaction_signals`, `stacks.signal_evidence`, and `stacks.extraction_runs` relations.
- Produces:

```go
type legacyObservationQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadLegacyObservation(
	ctx context.Context,
	query legacyObservationQuerier,
	observationID string,
) (decodedLegacyObservation, error)
```

- The loader returns canonical observation plus private compatibility and unchanged optional signal state only inside `internal/storage`.

- [ ] **Step 1: Write failing PostgreSQL decode tests**

Add:

```go
func TestLoadLegacyObservationDecodesCanonicalReferenceAndTimeShapes(t *testing.T)
func TestLoadLegacyObservationPreservesEvidenceOriginAndRoles(t *testing.T)
func TestLoadLegacyObservationPreservesSignalVertical(t *testing.T)
func TestLoadLegacyObservationPreservesHistoricalSignalDerivationMismatch(t *testing.T)
func TestLoadLegacyObservationMarksHistoricalUncited(t *testing.T)
func TestLoadLegacyObservationRejectsDigestMismatchPrivately(t *testing.T)
func TestLoadLegacyObservationRejectsInvalidStoredPairingsPrivately(t *testing.T)
```

Reuse `createLegacyObservationFixture`, `insertLegacyObservation`, and the 16
subject/object combinations from
`observation_compatibility_integration_test.go`. Seed synthetic rows only.
Update the fixture helper to calculate its stored digest through
`computeObservationDigestV1`; its former arbitrary 32-byte digest is no longer
valid once the loader verifies compatibility. For each decoded value assert
canonical term kind and IDs, time kind/bounds, all evidence pairs, the exact
private origin, stored digest, derivation, status, confidence, and signal
metadata.

For persisted corruption that the schema permits, seed a wrong 32-byte digest
and exercise the loader. A historical signal model/prompt mismatch with its
owning run is valid vertical data: add a positive test proving the canonical
derivation comes from the run while the unchanged mismatch remains in the
signal state. Keep end-only, decreasing-time, non-finite confidence, and other
shapes blocked by PostgreSQL in pure codec tests rather than weakening an
isolated schema. Assert `errors.Is(err, ErrObservationCompatibility)` for real
corruption, a fixed reason code, and absence of
predicate/rationale/evidence text in `err.Error()`.

- [ ] **Step 2: Run the loader tests and verify they fail**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestLoadLegacyObservation' -count=1
```

Expected: FAIL because `loadLegacyObservation` does not exist.

- [ ] **Step 3: Implement exact row and relationship loading**

Load the observation row and optional owning run in one query. Load `observation_evidence` ordered by evidence UUID and the optional signal plus `signal_evidence` ordered by evidence UUID then role. Copy `bytea` digest values into fixed arrays only after enforcing 32 bytes.

Use these private loader signatures:

```go
func scanLegacyObservationState(
	context.Context,
	legacyObservationQuerier,
	string,
) (legacyObservationRow, *owningExtractionRun, *legacySignalState, error)

func loadObservationEvidenceOrigin(
	context.Context,
	legacyObservationQuerier,
	string,
) ([]evidence.EvidenceID, error)
```

Use this order:

```go
row, run, signal, err := scanLegacyObservationState(ctx, query, observationID)
if err != nil {
	return decodedLegacyObservation{}, err
}
origin, err := loadObservationEvidenceOrigin(ctx, query, observationID)
if err != nil {
	return decodedLegacyObservation{}, err
}
decoded, err := decodeLegacyObservation(row, origin, signal, run)
if err != nil {
	return decodedLegacyObservation{}, err
}
expected, err := computeObservationDigestV1(legacyObservationWrite{
	Row: row, Origin: origin, Signal: signal,
})
if err != nil || expected != row.Digest {
	return decodedLegacyObservation{}, newObservationBoundaryError(
		ErrObservationCompatibility, reasonObservationDigestMismatch, observationID,
	)
}
return decoded, nil
```

Do not select evidence quote or document content for observation compatibility.
The signal loader must select rationale to preserve the vertical row, but no
error may include rationale, predicate, prompt content, evidence text, or
document content.

- [ ] **Step 4: Upgrade the existing characterization from raw SQL to canonical assertions**

Keep `TestLegacyObservationCompatibilityShapes` as the black-box migration contract. After each accepted raw insert, call `loadLegacyObservation` and assert the canonical mapping rather than merely re-reading nullable columns. Extend its provenance subtest to assert:

```go
want := []observation.EvidenceLink{
	{EvidenceID: evidence.EvidenceID(fixture.evidenceSpanID), Role: observation.EvidenceSupporting},
	{EvidenceID: evidence.EvidenceID(fixture.evidenceSpanID), Role: observation.EvidenceContradicting},
}
```

and separately assert the private origin contains the evidence ID exactly once.

- [ ] **Step 5: Run all compatibility decode tests**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestLegacyObservationCompatibilityShapes|TestLoadLegacyObservation' -count=1
```

Expected: PASS against migrations through `00012`.

- [ ] **Step 6: Commit canonical legacy decoding**

```bash
git add internal/storage/observation_postgres.go internal/storage/observation_postgres_integration_test.go internal/storage/observation_compatibility_integration_test.go
git commit -m "Decode legacy observations canonically"
```

---

### Task 3: Replace the direct graph observation API and enforce full retry equality

**Files:**
- Modify: `internal/storage/observation_postgres.go`
- Modify: `internal/storage/observation_postgres_integration_test.go`
- Modify: `internal/storage/graph.go`
- Modify: `internal/storage/integration_test.go`

**Interfaces:**
- Consumes: Task 2 loader and Task 1 encoder.
- Produces:

```go
func (repository *GraphRepository) CompleteObservation(
	ctx context.Context,
	value observation.Observation,
	observationEvidenceOrigin []evidence.EvidenceID,
	signal *SignalInput,
	signalEvidence []SignalEvidenceInput,
) (observation.Observation, *InteractionSignal, error)

func putLegacyObservation(
	ctx context.Context,
	transaction pgx.Tx,
	write legacyObservationWrite,
) (observation.Observation, *InteractionSignal, error)
```

- Replaces the public direct observation method with the canonical boundary.
  The old observation DTO and helpers remain temporarily for the active
  ingestion caller and are removed in Task 5 immediately after that caller
  switches.

- [ ] **Step 1: Convert direct callers to atomic canonical observation completions and verify compile failure**

In `internal/storage/integration_test.go`, replace old `ObservationInput` fixtures with a helper:

```go
func testCanonicalObservation(
	t *testing.T,
	run owningExtractionRun,
	id, subjectEntityID, subjectMentionID, objectEntityID, objectMentionID string,
	predicate string,
	validTime observation.TemporalExtent,
	evidenceIDs ...string,
) observation.Observation
```

The helper creates the correct term shape, supporting evidence links, an exact
predicate, `RecordedAt: run.RecordedAt`, derivation method `"synthetic"`,
`Version: run.PromptVersion`, `RunID: run.ID`, `Model: run.ModelID`,
`PromptVersion: run.PromptVersion`, status `inferred`, and no confidence unless
explicitly requested by the test. It never sets `LegacyUnversioned` for a new
write.

Create each direct-write run through `IngestionRepository.PrepareVersion` with
a synthetic document version and derivation. In Task 3's fixture, query that
run's exact stored `recorded_at`, model, and prompt metadata into
`owningExtractionRun`; Task 4 later exposes the same stored instant through
`VersionState`. Callers that create a manager signal pass the exact observation
origin, existing `SignalInput`, and role links in the same
`CompleteObservation` call.

Run:

```bash
go test ./internal/storage -run 'TestStorageRetriesDoNotDuplicateGraphRecords|TestCompleteObservationRejectsNotesOnlySignalEvidence' -count=1
```

Expected: FAIL because `CompleteObservation` still accepts the removed DTO
signature and signals are completed separately.

- [ ] **Step 2: Implement canonical direct writes**

For direct writes:

1. canonicalize and retain the explicitly supplied observation origin;
2. require a non-empty canonical `Derivation.RunID` and load that owning run;
3. reject `LegacyUnversioned` active writes;
4. for a signal-free write, require every canonical link to be supporting and every supporting ID to equal the supplied origin;
5. for a signaled write, apply the exact origin-relative rules from the design: every origin ID has canonical support, support outside the origin is backed by signal support, every contradiction is backed by signal contradiction, and every signal role appears in canonical evidence;
6. require the signal observation ID to equal `value.ID()`;
7. encode using Task 1;
8. insert observation, origin, optional signal, and signal-role rows in the existing graph transaction; and
9. return the canonical observation and optional stored signal after successful equality.

The SQL insert must use `value.RecordedAt()` from the encoded row; it must contain no `time.Now`.

- [ ] **Step 3: Write failing stable-ID and unique-digest conflict tests**

Add:

```go
func TestCompleteObservationAcceptsExactCanonicalRetry(t *testing.T)
func TestCompleteObservationRejectsEveryDigestExcludedDifference(t *testing.T)
func TestCompleteObservationRejectsDifferentIDWithSameDigest(t *testing.T)
func TestCompleteObservationRejectsUnrepresentableCanonicalValueBeforeSQL(t *testing.T)
func TestCompleteObservationPreservesOriginRelativeSignalEvidence(t *testing.T)
```

The digest-excluded difference table must independently change:

- `RecordedAt`;
- private origin while retaining the same canonical union through signal state in a seeded row;
- a signal-only evidence ID;
- one signal evidence role;
- generic confidence presence/value;
- vertical signal confidence; and
- stored digest bytes.

The origin-relative table must cover observation-only supporting evidence,
signal-only supporting evidence, supporting evidence present in both
relations, and contradicting signal evidence whose ID is also in the
observation origin. Assert the exact canonical pairs, private origin, and
vertical roles after loading each write.

Every case must assert `errors.Is(err, ErrObservationConflict)` for retry differences or `ErrObservationCompatibility` for pre-existing stored corruption. Ensure errors contain only the stable UUID and fixed reason.

- [ ] **Step 4: Run retry tests and verify digest-only behavior fails**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestCompleteObservationAccepts|TestCompleteObservationRejects' -count=1
```

Expected: FAIL because the current writer compares only digest bytes.

- [ ] **Step 5: Implement complete stored-state retry comparison**

On stable-ID conflict, call `loadLegacyObservation` and compare the complete
atomic completion, including the optional signal:

```go
func equalLegacyObservationRetry(
	expected legacyObservationWrite,
	stored decodedLegacyObservation,
) bool
```

Compare stable ID, all term columns including grounding mentions, exact predicate and derivation bytes, temporal kind/bounds, exact UTC microsecond `RecordedAt`, evidence `(ID, role)` pairs, exact private origin, stored and recomputed v1 digest, complete derivation, status, confidence presence/value/scale, `LegacyUncited`, and the complete optional signal state.

Do not use `reflect.DeepEqual` on `time.Time`; compare instants with `Equal` after representability has been established. Compare sorted private slices and canonical evidence slices explicitly.

Map a PostgreSQL unique-digest violation under a different stable ID to `ErrObservationConflict`. Preserve context cancellation and database causes through wrapping.

- [ ] **Step 6: Preserve signal behavior through the atomic boundary**

Keep `SignalInput`, `InteractionSignal`, `SignalEvidenceInput`,
`ComputeSignalDigest`, category/direction validation, and deferred transcript
constraint behavior unchanged. Move direct callers from the separate
`CompleteSignal` lifecycle into atomic `CompleteObservation`, then remove the
public `CompleteSignal` method after no caller remains. Retain package-local
`putSignal` for the shared transaction used by ingestion.

Do not remove the old observation DTO, `putObservation`, or its helper chain
in this task: `persistIngestionGraph` still calls them. Task 5 removes that
temporary path in the same commit that switches ingestion. Keep all
signal-specific validation and digest code.

- [ ] **Step 7: Run the direct graph regression set and commit**

Run:

```bash
direnv exec . go test ./internal/storage -run 'Observation|Graph|Signal|Digest|NotesOnly' -count=1
git diff --check
```

Expected: PASS.

Commit:

```bash
git add internal/storage/observation_postgres.go internal/storage/observation_postgres_integration_test.go internal/storage/graph.go internal/storage/integration_test.go
git commit -m "Use canonical observations in graph storage"
```

---

### Task 4: Carry one persisted extraction-run time through ingestion

**Files:**
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/storage/documents.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/source/drive/chronology_test.go`
- Modify: `cmd/stacks/main_test.go`
- Modify: every test fake returned by:

```bash
rg -l 'PrepareVersion\\(' --glob '*_test.go'
```

**Interfaces:**
- Consumes: existing `PrepareVersion` transaction and extraction-run `recorded_at`.
- Produces:

```go
type VersionState struct {
	ID               string
	DerivationID     string
	DerivationDigest [sha256.Size]byte
	RecordedAt       time.Time
	LeaseOwner       string
	LeaseExpiresAt   time.Time
	Status           VersionStatus
	RetryCount       int
	FailureCode      FailureCode
}
```

- `RecordedAt` is the exact persisted UTC microsecond extraction-run time for new, resumed, busy, and complete states.

- [ ] **Step 1: Write failing repository tests for persisted run time**

Add:

```go
func TestPrepareVersionReturnsPersistedRecordedAtForEveryState(t *testing.T)
func TestPrepareVersionTruncatesRecordedAtOnceToPostgresPrecision(t *testing.T)
```

Exercise new pending, busy, failed/resumed, and complete paths. Query `extraction_runs.recorded_at` after the first prepare and assert every later state returns the same instant:

```go
if !state.RecordedAt.Equal(storedRecordedAt) ||
	state.RecordedAt.Location() != time.UTC ||
	!state.RecordedAt.Equal(state.RecordedAt.Truncate(time.Microsecond)) {
	t.Fatalf("RecordedAt = %v, want persisted UTC microsecond %v", state.RecordedAt, storedRecordedAt)
}
```

- [ ] **Step 2: Run the focused preparation tests and verify failure**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestPrepareVersion.*RecordedAt' -count=1
```

Expected: FAIL because `VersionState` does not expose `RecordedAt`.

- [ ] **Step 3: Persist and return the exact run time**

Set:

```go
claimedAt := time.Now().UTC().Truncate(legacyPostgresTimestampPrecision)
state := ingest.VersionState{
	ID:               stored.ID,
	DerivationDigest: derivation.Digest,
	RecordedAt:       claimedAt,
}
```

On `ON CONFLICT`, select `recorded_at` in the existing locked row query, scan
it into a local `persistedRecordedAt`, and assign
`state.RecordedAt = persistedRecordedAt.UTC()`. pgx does not configure a fixed
timestamptz scan location in this repository, so this explicit location
conversion preserves the exact instant while guaranteeing the canonical UTC
form. Never update the database value on resume. Ensure complete and busy
states also return it.

- [ ] **Step 4: Write the failing service test for observation-time propagation**

Add:

```go
func TestCompletionCarriesRetryStableRecordedAt(t *testing.T)
```

Use a `VersionState.RecordedAt` that differs from the document version and
service clocks. Assert every existing `ObservationRecord` carries the run time
exactly.

- [ ] **Step 5: Add time to the existing record without cutting over its type**

Add one field while leaving every existing field and `Completion.Observations`
type unchanged:

```go
type ObservationRecord struct {
	// existing fields remain unchanged
	RecordedAt time.Time
}
```

Set it from `state.RecordedAt` in `Service.completion` and require it to be
non-zero in the existing `validateObservationIdentities`. Do not rename
`ObservationRecord`, change its predicate/time/confidence field types, remove
its semantic hash, or change `persistIngestionGraph` in Task 4. Task 5 performs
that atomic type and storage cutover.

- [ ] **Step 6: Update every repository fake**

Give deterministic synthetic UTC microsecond `RecordedAt` values to memory, chronology, snapshot-coherence, and runtime fakes. Do not use `time.Now` for observation time in these fakes. Keep lease clocks separate because lease expiration is operational rather than observation time.

- [ ] **Step 7: Run ingestion and preparation tests and commit**

Run:

```bash
go test ./internal/ingest ./internal/source/drive ./cmd/stacks -count=1
direnv exec . go test ./internal/storage -run 'TestPrepareVersion.*RecordedAt|TestIngestionRepositoryResumesVersion' -count=1
git diff --check
```

Expected: PASS.

Commit:

```bash
git add internal/ingest/service.go internal/ingest/service_test.go internal/ingest/validate.go internal/ingest/validate_test.go internal/storage/documents.go internal/storage/integration_test.go internal/source/drive/chronology_test.go cmd/stacks/main_test.go
git commit -m "Carry extraction recorded time through ingestion"
```

---

### Task 5: Route active ingestion through canonical observation storage

**Files:**
- Modify: `internal/storage/documents.go`
- Modify: `internal/storage/observation_postgres.go`
- Modify: `internal/storage/graph.go`
- Modify: `internal/storage/graph_test.go`
- Modify: `internal/storage/observation_codec_test.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/storage/observation_compatibility_integration_test.go`
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/ingest/validate.go`
- Modify: `internal/ingest/validate_test.go`

**Interfaces:**
- Consumes: Task 4 `ObservationRecord` with retry-stable `RecordedAt`, durable evidence/mention maps, persisted owning run, Task 1 codec, and the unchanged signal vertical.
- Produces:

```go
type ObservationDraft struct {
	ID                observation.ObservationID
	SubjectEntityID   string
	ObjectEntityID    string
	SubjectMentionKey string
	ObjectMentionKey  string
	Predicate         observation.Predicate
	ValidTime         observation.TemporalExtent
	RecordedAt        time.Time
	EvidenceKeys      []string
	SourceConfidence  observation.Confidence
}

func buildCanonicalIngestionObservation(
	draft ingest.ObservationDraft,
	signal *ingest.SignalRecord,
	run owningExtractionRun,
	evidenceIDs map[string]string,
	mentionIDs map[string]string,
) (observation.Observation, legacyObservationCompatibility, *legacySignalState, error)

func persistIngestionGraph(
	ctx context.Context,
	transaction pgx.Tx,
	run owningExtractionRun,
	observations []ingest.ObservationDraft,
	signals []ingest.SignalRecord,
	evidenceIDs, mentionIDs map[string]string,
) error
```

- Canonical construction happens after durable UUID resolution and before any observation or signal SQL insert.

- [ ] **Step 1: Write failing draft-boundary tests**

Add:

```go
func TestCompletionBuildsCanonicalCompatibleObservationDraft(t *testing.T)
func TestValidateForPersistenceRejectsInvalidSourceConfidence(t *testing.T)
func TestValidateForPersistenceUsesReferencesNotObservationSemanticHash(t *testing.T)
```

The completion test must assert an exact predicate, `Since` for the current
start-only meeting date, `state.RecordedAt`, and a
`ConfidenceUnitInterval` source score. The persistence test must pass
`observation.Confidence{}` and `NewLegacyConfidence(0.5)` in otherwise valid
drafts and assert `ErrPersistenceReference`.

The reference test must prove duplicate local IDs and unknown evidence or
mention keys fail, while two different stable IDs with equal pre-resolution
semantic fields pass draft validation; the staged codec detects the resulting
duplicate durable digest later in this task.

- [ ] **Step 2: Cut over the draft type and reference validation atomically**

Rename `ObservationRecord` to `ObservationDraft` and change
`Completion.Observations` in the same edit that updates all ingestion and
storage compile-time consumers. In `Service.completion`, construct the
predicate with `observation.NewPredicate`, map a non-null meeting date with
`observation.Since`, preserve unknown time with `observation.UnknownTime`, and
construct the source score with `observation.NewUnitIntervalConfidence`.

Validate the exported confidence value:

```go
func validSourceConfidence(value observation.Confidence) bool {
	score := value.Value()
	return value.Scale() == observation.ConfidenceUnitInterval &&
		!math.IsNaN(score) && !math.IsInf(score, 0) &&
		score >= 0 && score <= 1
}
```

Replace `validateObservationIdentities` and its JSON semantic hash with:

```go
func validateObservationReferences(
	records []ObservationDraft,
	evidence map[string][sha256.Size]byte,
	mentions map[string]struct{},
) (map[string]struct{}, error)
```

This helper checks unique canonical local observation IDs, mention-key
ownership, evidence-key ownership, non-zero recorded time, and valid source
confidence. Change `validateSignalIdentities` to consume the returned
observation-ID set instead of an observation semantic digest.

- [ ] **Step 3: Run the draft tests**

Run:

```bash
go test ./internal/ingest -run 'TestCompletionBuildsCanonicalCompatibleObservationDraft|TestValidateForPersistence' -count=1
```

Expected: PASS. Do not commit at this checkpoint:
`persistIngestionGraph` switches to `[]ingest.ObservationDraft` in the
remaining steps of this same task before any build or commit gate.

- [ ] **Step 4: Write failing ingestion integration tests**

Add:

```go
func TestIngestionPersistsCanonicalObservationWithLegacyConfidenceDowngrade(t *testing.T)
func TestIngestionPreservesObservationOriginAndSignalRoles(t *testing.T)
func TestIngestionUsesOwningRunDerivationAndRecordedAt(t *testing.T)
func TestIngestionCanonicalConstructionFailureRollsBackWholeCompletion(t *testing.T)
```

The confidence test must assert the loaded canonical observation has `unspecified_legacy` with the exact source number while the signal row retains the same numeric score and application-level bounded source fixture.

The evidence test must use distinct statement, supporting, and contradicting citations plus one citation repeated across roles. Assert:

- private origin is the distinct statement/support/contradiction union;
- every origin ID has a canonical supporting link;
- signal evidence retains exact supporting and contradicting pairs;
- one ID may appear as both supporting and contradicting; and
- v1 uses the origin, not the canonical union.

The rollback test must make canonical construction fail after evidence and mention persistence would have begun, then assert no evidence, mention, proposal, observation, signal, completion-status, or current-pointer mutation committed.

- [ ] **Step 5: Run active-ingestion tests and verify old DTO behavior fails**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestIngestionPersistsCanonical|TestIngestionPreservesObservation|TestIngestionUsesOwningRun|TestIngestionCanonicalConstructionFailure' -count=1
```

Expected: FAIL because persistence still builds storage `ObservationInput` and chooses observation time in `putObservation`.

- [ ] **Step 6: Load the owning run once inside the completion transaction**

Extend the locked extraction-run query in `CompleteVersion` to load:

```go
run := owningExtractionRun{
	ID:            completion.DerivationID,
	ModelID:       storedModelID,
	PromptVersion: storedPromptVersion,
	RecordedAt:    storedRecordedAt,
}
```

Select `model_id`, `prompt_version`, and `recorded_at` alongside existing state. Verify `storedRecordedAt` equals the preparation state carried in every draft; mismatch is `ErrObservationConflict` with a fixed reason, not normalization.

- [ ] **Step 7: Resolve references and construct the canonical observation**

Build terms from durable values:

```go
func ingestionTerm(entityID, mentionID string) (observation.Term, error) {
	switch {
	case entityID != "":
		return observation.NewEntityTerm(entityID, mentionID)
	case mentionID != "":
		return observation.NewMentionTerm(mentionID)
	default:
		return observation.AbsentTerm(), nil
	}
}
```

Resolve the exact origin with the current distinct union in `draft.EvidenceKeys`. Build supporting canonical links for every origin, append signal role links, and deduplicate exact pairs only.

Perform the explicit confidence conversion:

```go
legacyConfidence, err := observation.NewLegacyConfidence(draft.SourceConfidence.Value())
if err != nil {
	return observation.Observation{}, legacyObservationCompatibility{}, nil, err
}
```

Construct with:

```go
observation.NewObservation(observation.ObservationInput{
	ID:         draft.ID,
	Statement:  observation.Statement{Subject: subject, Predicate: draft.Predicate, Object: object},
	ValidTime:  draft.ValidTime,
	RecordedAt: run.RecordedAt,
	Evidence:   links,
	Derivation: observation.Derivation{
		Method:        "model_extraction",
		Version:       run.PromptVersion,
		RunID:         run.ID,
		Model:         run.ModelID,
		PromptVersion: run.PromptVersion,
	},
	Status:     observation.StatusInferred,
	Confidence: &legacyConfidence,
})
```

- [ ] **Step 8: Encode and persist observation plus signal as one write**

Index signals by observation ID and reject missing, duplicate, or dangling
signal ownership with bounded compatibility errors. Construct and encode every
legacy observation write into an in-memory slice first. Only after every
canonical observation succeeds may a second loop insert observations,
`observation_evidence`, signals, and `signal_evidence` inside the existing
completion transaction.

Before the insert loop, reject duplicate stable observation IDs, duplicate
digest v1 values under different IDs, duplicate signal IDs, and more than one
signal per observation by comparing the staged canonical writes. This replaces
the removed pre-persistence observation semantic hash with the actual durable
compatibility identity.

Do not call public `CompleteObservation`, because it starts a separate
transaction. Share only package-local `putLegacyObservation` and `putSignal`
primitives that accept the caller's transaction.

After the staged canonical ingestion path compiles, remove storage
`ObservationInput`, storage `Observation`, `putObservation`,
`ComputeObservationDigest`, `canonicalizeObservationIdentity`,
`validateObservationInput`, and `validEpistemicStatus`. At this point no
production caller may remain on the old observation path. Keep all
signal-specific validation and digest code.

Delete the temporary Task 1 bridge test that calls exported
`ComputeObservationDigest` in the same edit. Its fixed digest-v1 goldens remain
as the permanent compatibility proof.

- [ ] **Step 9: Prove legacy behavior and notes-only enforcement remain intact**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestLegacyObservationCompatibilityShapes|TestCompleteObservationRejectsNotesOnlySignalEvidence|TestIngestion.*Canonical|TestIngestion.*Origin|TestIngestion.*RecordedAt|TestIngestion.*RollsBack' -count=1
```

Expected: PASS with deferred transcript enforcement unchanged.

- [ ] **Step 10: Commit the active canonical path**

```bash
git add internal/ingest/service.go internal/ingest/service_test.go internal/ingest/validate.go internal/ingest/validate_test.go internal/storage/documents.go internal/storage/observation_postgres.go internal/storage/observation_codec_test.go internal/storage/graph.go internal/storage/graph_test.go internal/storage/integration_test.go internal/storage/observation_compatibility_integration_test.go
git commit -m "Persist ingestion observations canonically"
```

---

### Task 6: Compare completed-run retries without writing

**Files:**
- Create: `internal/storage/completed_write_set.go`
- Modify: `internal/storage/documents.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/storage/documents_test.go`

**Interfaces:**
- Consumes: a same-owner `ingest.Completion`, the locked complete extraction run, Task 2 canonical loader, and existing immutable document/evidence/identity tables.
- Produces:

```go
func compareCompletedWriteSet(
	ctx context.Context,
	transaction pgx.Tx,
	completion ingest.Completion,
	run owningExtractionRun,
) error
```

- Exact equality returns nil without inserts, updates, pointer repair, or state changes. Any difference returns `ErrObservationConflict` with `completion_write_set_mismatch`.

- [ ] **Step 1: Write the failing exact-retry no-mutation test**

Add:

```go
func TestCompleteVersionExactCompletedRetryIsReadOnly(t *testing.T)
func TestCompleteVersionExactRetryToleratesAdditiveIdentityEnrichment(t *testing.T)
```

Complete one synthetic ingestion, capture:

- row counts for evidence, mentions, proposals, candidates, decisions, observations, observation evidence, signals, and signal evidence;
- digests and immutable columns for those rows;
- extraction-run status, owner, data mode, admissibility, and completion time; and
- the source document's current-version pointer.

Call `CompleteVersion` again with the original owner and exact completion. Assert success and byte-for-byte equality of the snapshot before and after.

For the additive case, first append a synthetic directory candidate and then
exercise a reviewer decision or supersession through existing repository
methods. Capture the enriched snapshot, retry the original completion, and
assert success plus no mutation. The original completion-owned candidate and
automatic decision payloads must still exist; later candidates, decisions,
proposal status, supersession links, aliases, and directory assertions are
allowed additive state.

- [ ] **Step 2: Write the completed-retry mismatch matrix**

Add:

```go
func TestCompleteVersionRejectsCompletedWriteSetMismatch(t *testing.T)
func TestCompleteVersionRejectsCompletedRetryFromDifferentOwner(t *testing.T)
```

Use one fresh completed fixture per case. Independently mutate the supplied
completion for cases 1 through 10, and mutate the stored compatibility state
for cases 11 and 12:

1. evidence span identity;
2. evidence immutable quote digest or offsets;
3. mention identity key;
4. mention evidence binding;
5. removal or payload mutation of a completion-owned resolution candidate or automatic decision;
6. observation subject/object/predicate/time/confidence;
7. observation origin;
8. signal category/direction/rationale/confidence;
9. signal evidence role or ID;
10. data mode;
11. extraction-run admissibility; and
12. version/current-pointer association.

For write-set cases, assert `errors.Is(err, ErrObservationConflict)` and reason
`completion_write_set_mismatch`. For the different-owner test, assert the same
sentinel and reason `completion_owner_mismatch`. Every case must exclude
private fixture text from the error and perform no durable mutation.

- [ ] **Step 3: Run completed-retry tests and verify current early success fails**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestCompleteVersionExact|TestCompleteVersionRejectsCompleted' -count=1
```

Expected: FAIL because the current complete-state branch returns after optional current-pointer repair without comparing the supplied write-set.

- [ ] **Step 4: Implement read-only expected identity reconstruction**

Use existing deterministic identities and unique keys without calling insert helpers:

- evidence: locate by version/tab identity plus exact start/end and compare immutable quote/content digests;
- mentions: locate by extraction run, evidence ID, surface, and role; compare
  normalized name and proposed-email evidence, then require every
  completion-owned candidate, automatic decision, and authorized alias payload
  to remain present;
- observations: resolve supplied local keys through the located evidence and mention maps, construct canonical observations with `run.RecordedAt`, then compare through Task 3 codec state;
- signals: construct exact vertical signal state and compare all fields and evidence roles; and
- run/version: compare data mode, owner, admissibility, status, version association, and current pointer.

Private helper signatures:

```go
func loadCompletedEvidenceMap(context.Context, pgx.Tx, ingest.Completion) (map[string]string, error)
func loadCompletedMentionMap(context.Context, pgx.Tx, string, []ingest.MentionRecord, map[string]string) (map[string]string, error)
func compareCompletedObservations(context.Context, pgx.Tx, owningExtractionRun, ingest.Completion, map[string]string, map[string]string) error
func compareCompletedRunState(context.Context, pgx.Tx, ingest.Completion, owningExtractionRun) error
```

Every query is `SELECT` only. Do not call `PutDocumentVersion`, `PutEvidenceSpan`, `persistIngestionMentions`, `persistIngestionGraph`, `setCurrentDocumentVersion`, or any `INSERT`, `UPDATE`, or `DELETE` helper.

Compare completion-owned identity rows by their deterministic keys and
immutable payload/digest, not by exact equality of the whole proposal identity
subgraph. Ignore extra directory candidates, later reviewer decisions,
directory assertions, and aliases not supplied by the original completion.
Permit proposal status and supersession linkage to reflect those later
append-only decisions.

- [ ] **Step 5: Replace the complete-state early return**

Use:

```go
if status == string(ingest.VersionStatusComplete) {
	if completedByOwner == nil || *completedByOwner != completion.LeaseOwner {
		return newCompletionBoundaryError(
			ErrObservationConflict,
			reasonCompletionOwnerMismatch,
			completion.DerivationID,
		)
	}
	if err := compareCompletedWriteSet(ctx, transaction, completion, run); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
```

Do not repair the current pointer in this branch. A pointer mismatch is a conflict because exact completed retry is observationally read-only.

- [ ] **Step 6: Run completed retry, race, and rollback subsets**

Run:

```bash
direnv exec . go test ./internal/storage -run 'CompleteVersion|Retry|Rollback|CurrentDocumentVersion' -count=1
go test -race ./internal/ingest ./internal/storage -run 'CompleteVersion|Retry' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 7: Commit completed-run equality**

```bash
git add internal/storage/completed_write_set.go internal/storage/documents.go internal/storage/integration_test.go internal/storage/documents_test.go
git commit -m "Compare completed ingestion retries"
```

---

### Task 7: Remove the parallel DTO, document the boundary, and verify Plan B

**Files:**
- Modify: `README.md`
- Verify without modifying: `internal/storage/graph.go`
- Verify without modifying: `internal/storage/graph_test.go`
- Verify without modifying: `internal/storage/documents.go`
- Verify without modifying: `db/migrations/00001_enable_vector.sql` through `db/migrations/00012_google_directory_identity.sql`

**Interfaces:**
- Consumes: completed Tasks 1 through 6.
- Produces: one active canonical observation/PostgreSQL path, no duplicate durable observation DTO, unchanged legacy migrations, and accurate local-verification claims.

- [ ] **Step 1: Prove the old observation DTO and duplicate validator are unused**

Run:

```bash
if rg -n 'type ObservationInput struct|type Observation struct|validateObservationInput|validEpistemicStatus|func ComputeObservationDigest\\(|canonicalizeObservationIdentity|time\\.Now\\(\\).*observation' internal/storage; then
	echo "obsolete observation storage path remains" >&2
	exit 1
fi
```

Expected: no matches for the removed observation symbols or a fresh observation clock. Signal-specific types and validation remain.

- [ ] **Step 2: Run compile-focused tests after the completed cleanup**

Task 5 already removed the observation DTO, old digest adapter, duplicate
status/confidence/time validation, and old writer. If Step 1 finds one of those
symbols, stop and return to Task 5 rather than creating an unreviewed cleanup
change in this final verification task. Do not delete signal vertical code or
shared transaction handling.

Run:

```bash
go test ./internal/storage ./internal/ingest ./internal/source/drive ./cmd/stacks -run '^$'
```

Expected: PASS compilation.

- [ ] **Step 3: Update README claims narrowly**

In `Public core (experimental)`, state:

```markdown
The application PostgreSQL boundary consumes canonical `core/observation`
values through a compatibility codec for the frozen legacy schema. That codec
does not make the legacy schema complete canonical storage: unsupported terms,
time windows, and explicit generic confidence scales remain deferred to the
scoped-migration phase.
```

Do not claim scoped migrations, an independently released adapter, complete canonical storage, provider acceptance, or a production query API.

- [ ] **Step 4: Run formatting and deterministic tests**

Run:

```bash
make fmt
make modules-check
make test
```

Expected: PASS.

- [ ] **Step 5: Run race tests, Staticcheck, and build**

Run:

```bash
make test-race
make staticcheck
make build
(cd core && GOWORK=off go mod tidy -diff)
(cd core && GOWORK=off go test ./... -count=1)
(cd core && GOWORK=off go test -race ./... -count=1)
(cd core && GOWORK=off go build ./...)
```

Expected: PASS with no module-file rewrite.

- [ ] **Step 6: Run local PostgreSQL migration and integration checks**

Run:

```bash
make db-up
make db-migrate
make db-status
direnv exec . make test-integration
```

Expected: migrations `00001` through `00012` applied, none pending, and every PostgreSQL-gated storage and doctor test PASS. No model or Google provider is invoked.

- [ ] **Step 7: Run focused compatibility and retry regressions**

Run:

```bash
direnv exec . go test ./internal/storage -run 'Compatibility|Observation|Graph|Signal|Retry|Digest|Admission|CompleteVersion|CurrentDocumentVersion' -count=1
```

Expected: PASS.

- [ ] **Step 8: Prove migration immutability and repository cleanliness**

Run:

```bash
git diff --exit-code 29e7dcd -- db/migrations
git diff --check
git status --short
```

Expected: no migration diff, no whitespace errors, and only intentional Plan B files before the final commit.

- [ ] **Step 9: Commit the documentation**

```bash
git add README.md
git commit -m "Document canonical observation storage"
```

- [ ] **Step 10: Perform independent review gates**

Dispatch independent reviewers for:

1. codec losslessness, term/time/confidence mapping, digest v1, and privacy-safe errors;
2. canonical direct writes and full stable-ID retry equality;
3. retry-stable extraction time, active evidence dual storage, signal preservation, and atomic rollback;
4. completed-run full write-set comparison and proof that the success path is read-only; and
5. whole-branch architecture, migration immutability, tests, documentation claims, and absence of provider or private-data access.

For each actionable finding, first add or strengthen a regression test, implement the smallest correction, rerun the reviewer's focused commands, then rerun all final checks affected by the correction.

- [ ] **Step 11: Record the exact verification boundary**

The final report must distinguish:

- passing deterministic, race, Staticcheck, build, migration-status, and local PostgreSQL/pgvector integration;
- no schema change beyond frozen migrations `00001` through `00012`;
- no Google Drive or Workspace Directory acceptance in Plan B;
- no Bedrock, Anthropic, or OpenAI invocation or acceptance in Plan B; and
- no private-corpus acceptance in Plan B.

Do not push, open a pull request, merge, deploy, or publish until the user explicitly requests the next delivery action.
