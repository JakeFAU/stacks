# Core Domain Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract Stacks' existing evidence, identity, observation, and temporal primitives into a provider-neutral public Go module without changing current application, PostgreSQL, manager-confidence, or provider behavior.

**Architecture:** Add `github.com/JakeFAU/stacks/core` as the first tracked workspace module and move one implementation of each earned domain contract into it. Keep the current application buildable through explicit source-to-evidence conversion and one temporary identity type-alias bridge; PostgreSQL remains on its existing DTO and immutable `00001`-`00012` migration history until the next implementation plan.

**Tech Stack:** Go 1.26.0, `golang.org/x/text` for compatibility-preserving Unicode NFKC normalization, PostgreSQL 17 with pgvector for characterization tests, the standard Go testing package, repository-pinned Staticcheck 2026.1, and Goose v3.27.1 for unchanged legacy migrations.

## Global Constraints

- Work only in `/Users/jacob/dev/personal/stacks` on `codex/open-source-core-boundary`; preserve unrelated work.
- Use fresh implementation subagents and independent task reviewers; finish with an independent whole-plan review.
- Move existing implementations; do not create a second competing evidence, identity, observation, or temporal implementation.
- Do not edit, rename, reorder, or re-checksum `db/migrations/00001_enable_vector.sql` through `db/migrations/00012_google_directory_identity.sql`.
- Do not change current PostgreSQL write behavior in this plan; characterize its complete compatibility surface first.
- Core may depend only on the Go standard library and the reviewed `golang.org/x/text` Unicode normalization dependency.
- Core must not import provider SDKs, SQL drivers, Zap, OpenTelemetry, application configuration, manager-confidence, directory policy, or packages under the root module's `internal` tree.
- Preserve the current stable and legacy revision-inclusive document digest bytes exactly.
- Preserve exact UTF-8 evidence offsets, immutable source provenance, unknown source time, valid time distinct from recorded time, and append-only identity authority.
- Canonical terms must represent absent values, text, unresolved mentions, and resolved entities with an optional grounding mention.
- Canonical observations must represent all six durable epistemic states and every finite legacy confidence without treating an unscaled score as a probability.
- Model confidence must never select temporal truth; temporal precedence must never imply causality.
- Keep manager-confidence, strict meeting-title chronology, transcript admission, Drive tab classification, provider disclosure policy, and directory matching downstream.
- Keep the current root application working and retain its existing CLI output.
- Use synthetic test values and reserved domains only.
- Do not invoke Google Drive, Workspace Directory, Bedrock, Anthropic, or OpenAI.
- Do not read or print private document contents, `.env`, OAuth files, credentials, or tokens.
- Do not deploy, enable cloud logging, change GitHub settings, push, tag, publish modules, open a PR, or merge without explicit user approval.

---

## File map

### New files

- `go.work`: tracked local workspace containing the root application module and `./core`.
- `modules.txt`: explicit module inventory used by local and CI verification.
- `scripts/check-modules.sh`: verifies that tracked `go.mod` files, `modules.txt`, and `go.work` agree.
- `core/go.mod` and `core/go.sum`: provider-neutral public module and its single approved foundational dependency once identity is moved.
- `core/doc.go`: public module purpose and privacy/provenance contract.
- `core/dependency_test.go`: import-boundary regression test.
- `core/evidence/evidence.go`: immutable evidence identity and source reference.
- `core/evidence/section.go`: generic immutable ordered content sections.
- `core/evidence/document.go`: immutable document versions, digests, and exact spans.
- `core/evidence/*_test.go`: black-box evidence, digest, source-time, copying, and UTF-8 tests.
- `core/identity/identity.go`: entity, alias, mention, and normalization contracts.
- `core/identity/resolver.go`: conservative accepted-alias resolution and review ranking.
- `core/identity/*_test.go`: black-box normalization, ambiguity, and ranking tests.
- `core/observation/term.go`: closed canonical term representation.
- `core/observation/time.go`: valid-time extent representation.
- `core/observation/confidence.go`: explicit confidence scale and finite-value validation.
- `core/observation/observation.go`: immutable observation, derivation, evidence roles, and epistemic status.
- `core/observation/*_test.go`: black-box canonical compatibility tests.
- `core/temporal/plan.go`: deterministic temporal query plans.
- `core/temporal/aggregation.go`: valid/recorded-time selection and conflict-preserving aggregation.
- `core/temporal/comparison.go`: deterministic before/after comparison.
- `core/temporal/*_test.go`: moved and extended temporal tests.
- `internal/knowledge/evidence_alias.go`: temporary `EvidenceID` alias used only
  by the old observation/query code until Task 5.
- `internal/entity/identity_alias.go`: temporary logic-free aliases from downstream directory/application code to public identity values.
- `internal/storage/observation_compatibility_integration_test.go`: PostgreSQL characterization matrix for the next plan.

### Modified files

- `.gitignore`: track `go.work`; continue ignoring `go.work.sum`.
- `Makefile`: explicitly run formatting, tests, race tests, Staticcheck, and builds for every module in `modules.txt`.
- `.github/workflows/ci.yml`: call the explicit multi-module Make targets.
- `internal/source/source.go`: remain the current Drive-facing transport contract; no provider behavior change.
- `internal/ingest/service.go` and tests: convert `source.Tab` and `MeetingTime` into generic evidence inputs.
- `internal/storage/documents.go` and tests: consume public evidence types without changing SQL names or behavior.
- root application tests and interfaces that currently mention `internal/knowledge` or base `internal/entity` types.
- `README.md`: describe only the public packages actually implemented by this plan.

### Removed files

- `internal/knowledge/evidence.go`
- `internal/knowledge/evidence_test.go`
- `internal/knowledge/document.go`
- `internal/knowledge/document_test.go`
- `internal/knowledge/observation.go`
- `internal/knowledge/observation_test.go`
- `internal/entity/entity.go`
- `internal/entity/entity_test.go`
- `internal/entity/resolver.go`
- `internal/entity/resolver_test.go`
- `internal/query/plan.go`
- `internal/query/plan_test.go`
- `internal/query/aggregation.go`
- `internal/query/aggregation_test.go`
- `internal/query/comparison.go`
- `internal/query/comparison_test.go`

---

### Task 1: Characterize the complete legacy observation contract

**Files:**
- Create: `internal/storage/observation_compatibility_integration_test.go`
- Read without modifying: `db/migrations/00002_manager_confidence_poc.sql`
- Read without modifying: `internal/storage/graph.go`
- Test: `internal/storage/observation_compatibility_integration_test.go`

**Interfaces:**
- Consumes: `openIntegrationDatabase(t)`, current `stacks.observations`, `stacks.observation_evidence`, `stacks.interaction_signals`, and `stacks.signal_evidence`.
- Produces: `TestLegacyObservationCompatibilityShapes`, an executable matrix that the canonical PostgreSQL plan must preserve.

- [ ] **Step 1: Add the accepted row-shape table**

Create a black-box matrix whose four independent reference columns exercise all
16 combinations:

```go
type legacyTermShape struct {
	name      string
	entityID  bool
	mentionID bool
}

var legacyTermShapes = []legacyTermShape{
	{name: "absent"},
	{name: "entity", entityID: true},
	{name: "mention", mentionID: true},
	{name: "entity_with_grounding_mention", entityID: true, mentionID: true},
}
```

Nest subject and object shapes and insert synthetic UUIDs for enabled columns.
Assert that every accepted row reads back the exact four nullable references
without collapsing an entity-plus-mention pair.

- [ ] **Step 2: Add epistemic, confidence, and temporal cases**

Use these exact case sets:

```go
statuses := []string{
	"observed",
	"inferred",
	"hypothesized",
	"validated_structurally",
	"validated_empirically",
	"rejected",
}

confidences := []*float64{
	nil,
	float64Pointer(-2.5),
	float64Pointer(0),
	float64Pointer(0.75),
	float64Pointer(1),
	float64Pointer(4.25),
}
```

Cover no bounds, start only, equal start/end, and increasing start/end. Execute
end-only, decreasing bounds, NaN, positive infinity, and negative infinity in
transactions and assert PostgreSQL rejects them.

- [ ] **Step 3: Characterize provenance and evidence roles**

Create one synthetic extraction run, evidence span, observation, and interaction
signal. Assert:

```go
type legacyObservationProjection struct {
	extractionRunID string
	recordedAt      time.Time
	derivation      string
	modelID         string
	promptVersion   string
	supportingIDs   []string
	contradictingIDs []string
}
```

The test must prove that existing `observation_evidence` implies supporting
evidence, while `signal_evidence` independently preserves both `supporting` and
`contradicting`. It must also preserve signal category, direction, rationale,
model, prompt, and confidence keyed by observation ID.

- [ ] **Step 4: Prove the active writer retains entity and grounding mention**

Use the existing auto-resolution fixture path and assert the durable row has
both `subject_entity_id` and `subject_mention_id` populated for the resolved
subject. Do the same for the object when the fixture resolves both parties.

- [ ] **Step 5: Run the focused PostgreSQL characterization**

Run:

```bash
direnv exec . go test ./internal/storage -run 'TestLegacyObservationCompatibilityShapes' -count=1
```

Expected: PASS with every subtest using synthetic data. If
`STACKS_TEST_DATABASE_URL` is unavailable, stop this task and report that Plan
A cannot safely define compatibility from schema inspection alone.

- [ ] **Step 6: Run the existing storage regression subset**

Run:

```bash
direnv exec . go test ./internal/storage -run 'Observation|Graph|Alias|Decision|Retry|Digest|Admission' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit the characterization**

```bash
git add internal/storage/observation_compatibility_integration_test.go
git commit -m "Characterize legacy observation compatibility"
```

---

### Task 2: Establish the tracked workspace and module-verification boundary

**Files:**
- Modify: `.gitignore`
- Create: `go.work`
- Create: `modules.txt`
- Create: `scripts/check-modules.sh`
- Create: `core/go.mod`
- Create: `core/doc.go`
- Create: `core/dependency_test.go`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: root module `stacks`, Go 1.26.0, pinned Staticcheck 2026.1.
- Produces: tracked workspace membership, explicit module inventory, `make test-race`, `make modules-check`, and independently testable `github.com/JakeFAU/stacks/core`.

- [ ] **Step 1: Write the failing module-inventory check**

Create `scripts/check-modules.sh` with this contract:

```sh
#!/bin/sh
set -eu

manifest_modules=$(sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$/d' modules.txt | sort)
filesystem_modules=$(
  find . -type f -name go.mod \
    -not -path './.git/*' \
    -not -path './vendor/*' \
    -not -path './.worktrees/*' \
    -not -path './worktrees/*' |
    sed -e 's#/go.mod$##' -e 's#^\./$#.#' |
    sort
)
workspace_modules=$(go work edit -json | sed -n 's/.*"DiskPath": "\(.*\)".*/\1/p' | sort)

test "$manifest_modules" = "$filesystem_modules"
test "$manifest_modules" = "$workspace_modules"
```

The implementation may use a more robust standard tool already available on
the machine to parse `go work edit -json`, but it must compare exact normalized
sets and print only module paths on failure.

- [ ] **Step 2: Run the check and verify RED**

Run:

```bash
sh scripts/check-modules.sh
```

Expected: FAIL because `modules.txt`, tracked `go.work`, and `core/go.mod` do not
exist yet.

- [ ] **Step 3: Track the workspace and add the core module**

Remove only the `go.work` ignore entry; keep `go.work.sum` ignored. Create:

```text
.
./core
```

```go
// go.work
go 1.26.0

use (
	.
	./core
)
```

```go
// core/go.mod
module github.com/JakeFAU/stacks/core

go 1.26.0
```

Create `core/doc.go`:

```go
// Package core documents the dependency boundary shared by Stacks' public
// evidence, identity, observation, and temporal packages.
//
// Core performs no provider I/O, persistence, configuration loading, logging,
// telemetry initialization, model invocation, or application-specific policy.
package core
```

- [ ] **Step 4: Add the core dependency test**

Implement `TestCoreDependencyBoundary` in package `core_test`. It runs:

```go
command := exec.Command("go", "list", "-deps", "./...")
command.Env = append(os.Environ(), "GOWORK=off")
```

Reject exact dependency prefixes for:

```go
var forbidden = []string{
	"stacks/internal/",
	"github.com/JakeFAU/stacks/adapters/",
	"github.com/JakeFAU/stacks/examples/",
	"github.com/JakeFAU/stacks/app/",
	"github.com/jackc/pgx/",
	"go.uber.org/zap",
	"go.opentelemetry.io/",
	"google.golang.org/api/",
	"github.com/aws/",
	"github.com/anthropics/",
	"github.com/openai/",
}
```

The test must compare complete import paths line by line; substring matches are
not sufficient.

- [ ] **Step 5: Make repository commands module-aware**

Add `modules-check` and `test-race` to `.PHONY`. Implement the commands by
reading `modules.txt` explicitly:

```make
modules-check:
	sh scripts/check-modules.sh

test:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go test ./...) || exit; \
	done

test-race:
	@sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$$/d' modules.txt | while IFS= read -r module; do \
		(cd "$$module" && go test -race ./...) || exit; \
	done
```

Apply the same explicit loop to Staticcheck and package builds. Preserve
`make build` producing `bin/stacks` from the current root application.

- [ ] **Step 6: Update CI to use the repository boundary**

Add `make modules-check` and `make test-race`; replace the raw application
build command with `make build`. Keep formatting, Staticcheck, and ordinary
tests. Do not add provider calls or secrets.

- [ ] **Step 7: Verify the workspace and isolated core**

Run:

```bash
make modules-check
make test
make test-race
make staticcheck
make build
(cd core && GOWORK=off go test ./... && GOWORK=off go build ./...)
```

Expected: PASS.

- [ ] **Step 8: Commit the workspace boundary**

```bash
git add .gitignore go.work modules.txt scripts/check-modules.sh core/go.mod core/doc.go core/dependency_test.go Makefile .github/workflows/ci.yml
git commit -m "Establish public core module boundary"
```

---

### Task 3: Extract provider-neutral immutable evidence

**Files:**
- Create: `core/evidence/evidence.go`
- Create: `core/evidence/section.go`
- Create: `core/evidence/document.go`
- Create: `core/evidence/evidence_test.go`
- Create: `core/evidence/document_test.go`
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/ingest/validate_test.go`
- Modify: `internal/storage/documents.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/source/drive/chronology_test.go`
- Modify: `cmd/stacks/main_test.go`
- Create temporarily: `internal/knowledge/evidence_alias.go`
- Remove: `internal/knowledge/evidence.go`
- Remove: `internal/knowledge/evidence_test.go`
- Remove: `internal/knowledge/document.go`
- Remove: `internal/knowledge/document_test.go`

**Interfaces:**
- Consumes: current immutable evidence and digest algorithms plus the downstream `source.Document`/`source.Tab` transport.
- Produces:

```go
type SectionInput struct {
	ID, Title, ParentID string
	Path                []string
	Order               int
	Role                 string
	Text                 string
}

func NewSection(SectionInput) (Section, error)
func (Section) ID() string
func (Section) Title() string
func (Section) ParentID() string
func (Section) Path() []string
func (Section) Order() int
func (Section) Role() string
func (Section) Text() string

type DocumentVersionInput struct {
	Provider, ProviderDocumentID, Title, Locator string
	ProviderVersion, ProviderRevision             string
	ModifiedAt, RecordedAt                        time.Time
	SourceTime                                    *time.Time
	Sections                                      []Section
}

func NewDocumentVersion(DocumentVersionInput) (DocumentVersion, error)
func (DocumentVersion) Sections() []Section
func (DocumentVersion) SourceTime() *time.Time

type EvidenceSpanInput struct {
	Document DocumentVersion
	SectionID string
	StartOffset int
	EndOffset int
	Quote string
}

func NewEvidenceSpan(EvidenceSpanInput) (EvidenceSpan, error)
func (EvidenceSpan) Provider() string
func (EvidenceSpan) ProviderDocumentID() string
func (EvidenceSpan) DocumentDigest() ContentDigest
func (EvidenceSpan) SectionID() string
func (EvidenceSpan) StartOffset() int
func (EvidenceSpan) EndOffset() int
func (EvidenceSpan) Text() string
```

- [ ] **Step 1: Move the existing evidence identity tests and verify RED**

Move the current `Evidence`, `ContentDigest`, parsing, source-reference, and
copying test cases into external package `evidence_test`. Change imports to:

```go
import "github.com/JakeFAU/stacks/core/evidence"
```

Run:

```bash
(cd core && go test ./evidence)
```

Expected: FAIL because the package does not exist.

- [ ] **Step 2: Move the immutable evidence implementation**

Move `EvidenceID`, `SourceReference`, `ContentDigest`, `DigestContent`,
`ParseContentDigest`, `EvidenceInput`, `Evidence`, and `NewEvidence` into
`core/evidence/evidence.go`. Preserve validation and error wording unless a
public package name makes the old wording inaccurate.

- [ ] **Step 3: Write generic section tests**

Add tests proving:

```go
func TestSectionRejectsEmptyIDTitleRoleInvalidOrderAndInvalidUTF8(t *testing.T)
func TestSectionDefensivelyCopiesPath(t *testing.T)
func TestDocumentVersionDefensivelyCopiesSections(t *testing.T)
```

Use role values `"transcript"`, `"gemini-notes"`, and `"other"` only as opaque
compatibility strings. Core must not define those as semantic constants.

- [ ] **Step 4: Implement immutable sections**

Store fields privately. `NewSection` trims ID, title, parent ID, path titles,
and role; rejects empty ID/title/role, negative order, empty path elements, and
invalid UTF-8 text; and copies the path. Accessors return copies where needed.

- [ ] **Step 5: Move document and exact-span tests**

Move current digest, provider-revision compatibility, source-time, hierarchy,
exact-span, invalid-boundary, and defensive-copy tests. Add fixed golden
assertions:

```go
const stableDigestGolden = "636138cffa70a8a1b034669d3cfb423c258cae3fc9a943842a01daf31cd795ec"
const legacyRevisionDigestGolden = "3cfec92c9511780154a2a0517ec094652350c23cb1fa588648b033ffee1d3f46"
```

These values are for the existing synthetic `documentVersionInput` fixture with
one transcript section containing `Alex: synthetic words`.

- [ ] **Step 6: Move and adapt the document implementation**

Replace `source.Tab` with private `Section` values, `Tabs()` with `Sections()`,
`SourceMeetingTime` with `SourceTime`, and `TabID` with `SectionID`. Preserve
the digest byte stream exactly:

```go
writeString(digest, section.ID())
writeString(digest, section.Title())
writeString(digest, section.ParentID())
writeLength(digest, uint64(len(section.Path())))
for _, pathTitle := range section.Path() {
	writeString(digest, pathTitle)
}
writeLength(digest, uint64(section.Order()))
writeString(digest, section.Role())
contentDigest := DigestContent([]byte(section.Text()))
writeBytes(digest, contentDigest[:])
```

Do not add new field labels, separators, or provider revision bytes to the
stable digest.

- [ ] **Step 7: Convert source documents at the ingestion boundary**

Add one focused function in `internal/ingest/service.go`:

```go
func evidenceSections(tabs []source.Tab) ([]evidence.Section, error)
```

For each tab, validate the existing application role with the current policy,
then call `evidence.NewSection`. Construct `evidence.DocumentVersionInput` with
`SourceTime: document.MeetingTime`. Do not rename source or SQL fields in this
task.

- [ ] **Step 8: Update storage and application imports**

Change consumers from `stacks/internal/knowledge` to
`github.com/JakeFAU/stacks/core/evidence`. Use section accessors in storage.
Update test fixtures mechanically and delete the old evidence/document files
only after `rg 'stacks/internal/knowledge'` shows observation/query references
alone. Keep those old packages compiling with this logic-free bridge:

```go
package knowledge

import "github.com/JakeFAU/stacks/core/evidence"

type EvidenceID = evidence.EvidenceID
```

- [ ] **Step 9: Run evidence and application regressions**

Run:

```bash
(cd core && go test ./evidence -count=1)
go test ./internal/ingest ./internal/storage ./cmd/stacks -count=1
direnv exec . go test ./internal/storage -run 'Digest|Document|Evidence|Compatibility' -count=1
```

Expected: PASS with both digest goldens unchanged.

- [ ] **Step 10: Commit the evidence boundary**

```bash
git add core/evidence internal/ingest internal/storage cmd/stacks internal/knowledge internal/source/drive/chronology_test.go
git commit -m "Extract immutable evidence core"
```

---

### Task 4: Extract conservative identity without directory policy

**Files:**
- Create: `core/identity/identity.go`
- Create: `core/identity/resolver.go`
- Create: `core/identity/identity_test.go`
- Create: `core/identity/resolver_test.go`
- Modify: `core/go.mod`
- Create: `core/go.sum`
- Create: `internal/entity/identity_alias.go`
- Remove: `internal/entity/entity.go`
- Remove: `internal/entity/entity_test.go`
- Remove: `internal/entity/resolver.go`
- Remove: `internal/entity/resolver_test.go`

**Interfaces:**
- Consumes: current NFKC normalization, exact accepted-alias authority, and deterministic candidate ranking.
- Produces the same public names under `github.com/JakeFAU/stacks/core/identity`; directory-specific contracts remain in `internal/entity`.

- [ ] **Step 1: Move identity tests into an external package**

Move normalization, mailbox, exact alias, duplicate ambiguity, name-only
ranking, and deterministic-order tests to `identity_test` and import:

```go
import "github.com/JakeFAU/stacks/core/identity"
```

Run:

```bash
(cd core && go test ./identity)
```

Expected: FAIL because the package does not exist.

- [ ] **Step 2: Move the identity values and normalization**

Move these exact contracts without adding directory data:

```go
type Kind string
const KindPerson Kind = "person"

type AliasType string
const (
	AliasTypeName  AliasType = "name"
	AliasTypeEmail AliasType = "email"
)

type Alias struct { Type AliasType; Value string }
type EntitySnapshot struct {
	ID string
	Kind Kind
	DisplayName string
	RecordedAt time.Time
	Aliases []Alias
}
type Mention struct { Name, Email string }
type Candidate struct { EntityID string; Confidence float64; Reason string }
type Resolution struct { EntityID string; AutoResolved bool; Candidates []Candidate }
```

Preserve `NormalizeName`, `NormalizeEmail`, and `ValidEmail`, including
`norm.NFKC.String`. Add `golang.org/x/text v0.40.0` to `core/go.mod` and run
`GOWORK=off go mod tidy` from `core/` to create the matching `core/go.sum`.

- [ ] **Step 3: Move the deterministic resolver**

Preserve the exact rule:

```go
// Auto-resolution occurs only when exactly one person owns an accepted exact
// email or accepted exact name alias. Similarity produces review candidates.
func (Resolver) Resolve(Mention, []EntitySnapshot) Resolution
```

Candidate ordering remains descending confidence then ascending entity ID.

- [ ] **Step 4: Add the temporary logic-free compatibility bridge**

Create aliases and function variables only:

```go
package entity

import coreidentity "github.com/JakeFAU/stacks/core/identity"

type Kind = coreidentity.Kind
type AliasType = coreidentity.AliasType
type Alias = coreidentity.Alias
type EntitySnapshot = coreidentity.EntitySnapshot
type Mention = coreidentity.Mention
type Candidate = coreidentity.Candidate
type Resolution = coreidentity.Resolution
type Resolver = coreidentity.Resolver

const (
	KindPerson = coreidentity.KindPerson
	AliasTypeName = coreidentity.AliasTypeName
	AliasTypeEmail = coreidentity.AliasTypeEmail
)

func NormalizeName(value string) string {
	return coreidentity.NormalizeName(value)
}

func NormalizeEmail(value string) string {
	return coreidentity.NormalizeEmail(value)
}

func ValidEmail(value string) bool {
	return coreidentity.ValidEmail(value)
}
```

Do not add wrappers containing policy or copy the implementation.

- [ ] **Step 5: Prove directory policy remains downstream**

Run:

```bash
(cd core && go test ./identity -count=1)
go test ./internal/entity ./internal/directory ./internal/storage -count=1
if (cd core && go list -deps ./... | rg 'google|directory|pgx|otel|zap'); then
	exit 1
fi
```

Expected: PASS; the final dependency command prints nothing.

- [ ] **Step 6: Commit the identity boundary**

```bash
git add core/identity core/go.mod core/go.sum internal/entity
git commit -m "Extract conservative identity core"
```

---

### Task 5: Publish canonical observations and deterministic temporal reading

**Files:**
- Create: `core/observation/term.go`
- Create: `core/observation/time.go`
- Create: `core/observation/confidence.go`
- Create: `core/observation/observation.go`
- Create: `core/observation/term_test.go`
- Create: `core/observation/time_test.go`
- Create: `core/observation/confidence_test.go`
- Create: `core/observation/observation_test.go`
- Create: `core/temporal/plan.go`
- Create: `core/temporal/aggregation.go`
- Create: `core/temporal/comparison.go`
- Create: `core/temporal/plan_test.go`
- Create: `core/temporal/aggregation_test.go`
- Create: `core/temporal/comparison_test.go`
- Remove: `internal/query/plan.go`
- Remove: `internal/query/plan_test.go`
- Remove: `internal/query/aggregation.go`
- Remove: `internal/query/aggregation_test.go`
- Remove: `internal/query/comparison.go`
- Remove: `internal/query/comparison_test.go`
- Remove: `internal/knowledge/observation.go`
- Remove: `internal/knowledge/observation_test.go`
- Remove: `internal/knowledge/evidence_alias.go`

**Interfaces:**
- Consumes: `core/evidence.EvidenceID` and the complete legacy shape matrix from Task 1.
- Produces canonical observations plus unchanged query-plan semantics and
  provenance-separated temporal `Fact` values:

```go
type TermKind uint8
const (
	TermAbsent TermKind = iota
	TermText
	TermMention
	TermEntity
)
func AbsentTerm() Term
func NewTextTerm(string) (Term, error)
func NewMentionTerm(mentionID string) (Term, error)
func NewEntityTerm(entityID, groundingMentionID string) (Term, error)

type ConfidenceScale string
const (
	ConfidenceUnitInterval ConfidenceScale = "unit_interval"
	ConfidenceUnspecifiedLegacy ConfidenceScale = "unspecified_legacy"
)
func NewUnitIntervalConfidence(float64) (Confidence, error)
func NewLegacyConfidence(float64) (Confidence, error)

type EvidenceRole string
const (
	EvidenceSupporting EvidenceRole = "supporting"
	EvidenceContradicting EvidenceRole = "contradicting"
)

type ObservationID string
```

- [ ] **Step 1: Write failing term tests**

Add:

```go
func TestEntityTermPreservesGroundingMention(t *testing.T)
func TestTermSupportsAllLegacyReferenceShapes(t *testing.T)
func TestTermRejectsBlankTextMentionAndEntity(t *testing.T)
func TestTermAccessorsDoNotConfuseKinds(t *testing.T)
```

The entity test must assert both IDs round-trip. `AbsentTerm` is valid only as
a representation; strict new-write policy belongs to the consuming validator.

- [ ] **Step 2: Implement closed immutable terms**

Use private fields and these accessors:

```go
func (Term) Kind() TermKind
func (Term) Text() (string, bool)
func (Term) MentionID() (string, bool)
func (Term) Entity() (entityID, groundingMentionID string, ok bool)
```

`NewEntityTerm` permits an empty grounding mention but not an empty entity ID.

- [ ] **Step 3: Move valid-time tests and implementation**

Move `TemporalKind`, `TemporalExtent`, `UnknownTime`, `AtTime`, `During`,
`Since`, `Until`, and `Within` from `internal/knowledge`. Preserve UTC
normalization, open-bound semantics, half-open bounded intervals/windows, and
the rejection of zero-duration `During`/`Within`.

- [ ] **Step 4: Write confidence-scale tests**

Use:

```go
func TestUnitIntervalConfidenceAcceptsClosedUnitInterval(t *testing.T)
func TestUnitIntervalConfidenceRejectsOutsideRangeAndNonFinite(t *testing.T)
func TestLegacyConfidencePreservesEveryFiniteValue(t *testing.T)
func TestLegacyConfidenceRejectsNonFinite(t *testing.T)
```

The legacy cases include `-2.5`, `0.75`, and `4.25`; all retain
`ConfidenceUnspecifiedLegacy`.

- [ ] **Step 5: Implement immutable confidence**

Expose:

```go
func (Confidence) Value() float64
func (Confidence) Scale() ConfidenceScale
```

Both constructors reject NaN and infinities. Only unit-interval confidence
enforces `[0,1]`.

- [ ] **Step 6: Write canonical observation tests**

Cover:

```go
func TestObservationPreservesEntityAndGroundingMentionTerms(t *testing.T)
func TestObservationAllowsAbsentLegacyTerms(t *testing.T)
func TestObservationPreservesSupportingAndContradictingEvidence(t *testing.T)
func TestObservationAcceptsAllDurableEpistemicStatuses(t *testing.T)
func TestObservationRequiresCallerRecordedTime(t *testing.T)
func TestObservationPreservesStructuredDerivationAndRunID(t *testing.T)
func TestObservationDefensivelyCopiesEvidenceLinks(t *testing.T)
func TestObservationRejectsDuplicateEvidenceRolePairs(t *testing.T)
```

- [ ] **Step 7: Implement the canonical observation contract**

Define:

```go
type EpistemicStatus string

const (
	StatusObserved EpistemicStatus = "observed"
	StatusInferred EpistemicStatus = "inferred"
	StatusHypothesized EpistemicStatus = "hypothesized"
	StatusValidatedStructurally EpistemicStatus = "validated_structurally"
	StatusValidatedEmpirically EpistemicStatus = "validated_empirically"
	StatusRejected EpistemicStatus = "rejected"
)

type Predicate string
func NewPredicate(string) (Predicate, error)

type Statement struct {
	Subject Term
	Predicate Predicate
	Object Term
}

type EvidenceLink struct {
	EvidenceID evidence.EvidenceID
	Role EvidenceRole
}

type Derivation struct {
	Method string
	Version string
	RunID string
	Model string
	PromptVersion string
	LegacyUnversioned bool
}

type ObservationInput struct {
	ID ObservationID
	Statement Statement
	ValidTime TemporalExtent
	RecordedAt time.Time
	Evidence []EvidenceLink
	Derivation Derivation
	Status EpistemicStatus
	Confidence *Confidence
}

func NewObservation(ObservationInput) (Observation, error)
func (Observation) ID() ObservationID
func (Observation) Statement() Statement
func (Observation) ValidTime() TemporalExtent
func (Observation) RecordedAt() time.Time
func (Observation) EvidenceLinks() []EvidenceLink
func (Observation) Derivation() Derivation
func (Observation) Status() EpistemicStatus
func (Observation) Confidence() (Confidence, bool)
```

Require ID, predicate, recorded time, at least one unique evidence-role pair,
derivation method, and valid status. Require a non-empty derivation version for
ordinary observations. Permit an empty version only when
`LegacyUnversioned=true`, and reject that flag when a version is present.
Require model and prompt together. Copy evidence and confidence values.

- [ ] **Step 8: Run observation tests**

Run:

```bash
(cd core && go test ./observation -count=1)
```

Expected: PASS.

The task is not committed yet. Continue directly into the temporal move so the
repository never contains two observation implementations or an incompatible
query bridge.

The temporal result uses:

```go
type Fact struct {
	Key string
	Value string
	ObservationIDs []observation.ObservationID
	SupportingEvidenceIDs []evidence.EvidenceID
	ContradictingEvidenceIDs []evidence.EvidenceID
}
```

- [ ] **Step 9: Move temporal planning unchanged**

Move intent, temporal selection, knowledge scope, retrieval operation, plan
validation, and accessors. Keep:

```go
func At(string, time.Time) (TemporalSelection, error)
func Between(string, time.Time, time.Time) (TemporalSelection, error)
func KnownAsOf(time.Time) (KnowledgeScope, error)
func NewPlan(PlanInput) (Plan, error)
```

Run:

```bash
(cd core && go test ./temporal -run 'Plan|Selection|Knowledge' -count=1)
```

Expected: PASS after moving the tests.

- [ ] **Step 10: Adapt temporal test observation builders**

Build observations with `NewTextTerm`, `NewPredicate`, caller-recorded time,
supporting evidence links, and unit-interval confidence only where a case
requires confidence metadata.

- [ ] **Step 11: Preserve valid-time and recorded-time selection**

Move `AggregateWindow`, `recordedTimeEligible`, and
`validTimeEligibility`. Update status names so rejected observations remain
excluded and hypothesized observations remain tentative. Structurally and
empirically validated observations follow the supported path; confidence is
never a filter or tie-breaker.

- [ ] **Step 12: Preserve evidence roles in summaries**

Update accumulation:

```go
for _, link := range candidate.Observation.EvidenceLinks() {
	switch link.Role {
	case observation.EvidenceSupporting:
		accumulator.supporting[link.EvidenceID] = struct{}{}
	case observation.EvidenceContradicting:
		accumulator.contradicting[link.EvidenceID] = struct{}{}
	}
}
```

Add tests proving:

- the same evidence ID may appear once in each distinct role;
- input evidence order does not affect aggregation output;
- comparison clones both role slices;
- an observation with only contradicting evidence is not promoted to a fact.

Add `UnresolvedCounterevidenceOnly` to the closed unresolved-reason vocabulary.
When an eligible observation has no supporting links, preserve it as an
unresolved candidate with that reason and its contradicting evidence. Do not
silently discard it and do not treat counterevidence as support. Exact duplicate
evidence-role pairs remain constructor errors from Step 7 rather than temporal
deduplication inputs.

- [ ] **Step 13: Move comparison unchanged except provenance fields**

Preserve deterministic ordering, unresolved conflicts, transition detection,
added/removed/changed facts, and exclusion of unresolved keys from changes.
Clone both evidence-role slices and observation IDs defensively. Replace the old
support-only provenance validation with:

```go
if len(fact.SupportingEvidenceIDs)+len(fact.ContradictingEvidenceIDs) == 0 {
	return Fact{}, fmt.Errorf("%s summary fact evidence is required", name)
}
```

This permits `UnresolvedCounterevidenceOnly` candidates to survive comparison
validation while still rejecting facts with no provenance.

- [ ] **Step 14: Delete the old implementations**

Run:

```bash
rg 'stacks/internal/(knowledge|query)' --glob '*.go'
```

Update any remaining test imports to public packages, then delete old
observation/query files. Do not leave compatibility logic or duplicate
implementations.

- [ ] **Step 15: Run core temporal and isolated-module checks**

Run:

```bash
(cd core && go test ./... -count=1)
(cd core && go test -race ./... -count=1)
(cd core && GOWORK=off go test ./... -count=1)
(cd core && GOWORK=off go build ./...)
```

Expected: PASS.

- [ ] **Step 16: Commit canonical observations and temporal reading together**

```bash
git add core/observation core/temporal internal/knowledge internal/query
git commit -m "Publish canonical temporal observations"
```

---

### Task 6: Document, verify, and independently review Plan A

**Files:**
- Modify: `README.md`
- Modify if needed: `core/doc.go`
- Test: complete repository and PostgreSQL integration suites

**Interfaces:**
- Consumes: Tasks 1-6.
- Produces: a verified public core-domain boundary with no release, provider, migration-cutover, or vertical claims.

- [ ] **Step 1: Document implemented reality**

Add a concise README section:

```text
Public core (experimental)

- evidence: immutable versioned sources and exact spans
- identity: conservative accepted-alias resolution
- observation: provenance-bearing valid/recorded-time claims
- temporal: deterministic aggregation and comparison

PostgreSQL, model/source providers, operator configuration, and the
manager-confidence workflow remain downstream in the application during the
next extraction phases.
```

Do not say adapters, scoped migrations, the synthetic commitment example, a
license, or independent module releases exist yet.

- [ ] **Step 2: Run formatting and module inventory**

Run:

```bash
make fmt
make modules-check
git diff --check
```

Expected: PASS and no unrelated formatting changes.

- [ ] **Step 3: Run deterministic tests, race tests, Staticcheck, and build**

Run:

```bash
make test
make test-race
make staticcheck
make build
```

Expected: PASS.

- [ ] **Step 4: Run isolated core verification**

Run:

```bash
(cd core && GOWORK=off go mod tidy)
git diff --exit-code HEAD -- core/go.mod core/go.sum
test -z "$(git status --porcelain -- core/go.mod core/go.sum)"
(cd core && GOWORK=off go test ./... -count=1)
(cd core && GOWORK=off go test -race ./... -count=1)
(cd core && GOWORK=off go build ./...)
```

Expected: PASS with no uncommitted module-file rewrite.

- [ ] **Step 5: Run local PostgreSQL migration and integration checks**

Run:

```bash
make db-up
make db-migrate
make db-status
direnv exec . make test-integration
```

Expected: migrations through `00012`, none pending, and all PostgreSQL-gated
tests PASS. No model or Google provider is invoked.

- [ ] **Step 6: Run focused compatibility regressions**

Run:

```bash
direnv exec . go test ./internal/storage -run 'Compatibility|Observation|Graph|Alias|Decision|Retry|Digest|Admission' -count=1
```

Expected: PASS.

- [ ] **Step 7: Perform independent review gates**

Dispatch separate reviewers for:

1. evidence digest and UTF-8 span compatibility;
2. canonical observation losslessness against Task 1;
3. identity authority and directory-policy exclusion;
4. temporal conflict, counterevidence, and confidence independence;
5. whole-plan imports, tests, migration immutability, and documentation claims.

Every actionable finding receives a regression test before a fix. Re-run the
reviewer's focused command after each fix.

- [ ] **Step 8: Commit the verified Plan A documentation**

```bash
git add README.md core/doc.go
git commit -m "Document public core boundary"
```

- [ ] **Step 9: Record the phase result**

Append this exact phase summary to `.superpowers/sdd/progress.md`:

```text
Plan A complete:
- public evidence, identity, observation, and temporal packages
- current application still working
- legacy migrations unchanged through 00012
- canonical PostgreSQL cutover not yet implemented
- providers and manager-confidence still downstream/internal
- no live Google or model acceptance claimed
```

Do not push or open a PR without explicit approval.
