# Google Directory Identity Enrichment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional, on-demand Google Workspace directory identity enrichment that automatically resolves only deterministically bound unique work-email matches and keeps name-only or model-associated matches reviewable.

**Architecture:** Preserve document completion as the required ingestion boundary, then run optional identity enrichment over durable unresolved mentions on both completed and unchanged derivations. Keep matching policy pure in `internal/entity`, orchestration and retry behavior in `internal/directory`, the People API in `internal/googledirectory`, and PostgreSQL transactions in `internal/storage`; Drive and directory OAuth remain separate scoped installations.

**Tech Stack:** Go 1.26, PostgreSQL 17 with pgvector and Goose forward migrations, Google People API v1, OAuth 2.0 installed-application flow, OpenTelemetry, Zap, and the standard Go testing package.

## Global Constraints

- Work in `/Users/jacob/dev/personal/stacks` and preserve unrelated work.
- The current schema ends at migration `00010`; add one forward-only migration, `00011_google_directory_identity.sql`.
- Do not invoke Bedrock, OpenAI, or Anthropic as part of directory tests or doctor checks.
- Do not deploy, enable cloud logging, push, open a PR, or merge without explicit user approval.
- Never print, log, trace, commit, or copy OAuth credentials, tokens, directory queries, names, emails, provider person identifiers, private source text, prompts, or model responses.
- Keep the populated `.env` ignored; `.env.example` contains safe placeholders only.
- Directory authorization is separate from Drive/Docs authorization and requests only `https://www.googleapis.com/auth/directory.readonly`.
- Directory lookup is disabled by default, on demand, bounded, and additive; its failure never changes a completed document into an incomplete or failed document.
- Only a unique exact approved-domain email with deterministic source binding or explicit reviewer authority may auto-resolve.
- A citation-verified model email, name-only match, domain shared contact, duplicate match, or conflicting existing link remains review-only.
- Automatic directory-email resolution asserts the email only; it never teaches the source or directory name as an accepted alias.
- A reviewer-approved name becomes authoritative for future exact-name resolution; corrections supersede decisions without deleting history.
- Directory snapshots, attempts, matches, links, and decisions preserve provider provenance and recorded time.
- Future employment and reporting relationships remain separate temporal observations; this implementation is identity-only.
- Use fakes for normal tests. Live Google directory acceptance is a separate opt-in step with safe synthetic queries and no directory contents in output.

---

## File map

### New files

- `internal/entity/directory.go`: provider-neutral directory profiles, query evidence, deterministic work-email admission, and evaluation policy.
- `internal/entity/directory_test.go`: policy, normalization, ambiguity, conflict, and name-trust regression tests.
- `internal/directory/service.go`: optional post-completion enrichment, bounded retries, freshness reuse, and privacy-safe summary.
- `internal/directory/service_test.go`: orchestration, retry, cancellation, reuse, and fail-soft tests.
- `internal/googleauth/oauth.go`: shared installed-application OAuth lifecycle parameterized by an explicit scope list.
- `internal/googleauth/oauth_test.go`: loopback, scope, token-permission, atomic-write, cancellation, and redaction tests.
- `internal/googledirectory/oauth.go`: directory-only OAuth wrappers and scope constant.
- `internal/googledirectory/oauth_test.go`: exact directory-only OAuth scope regression tests.
- `internal/googledirectory/client.go`: People API search adapter and bounded provider-error classification.
- `internal/googledirectory/client_test.go`: request-shape, pagination, normalization, result-limit, and error-sanitization tests.
- `internal/googledirectory/probe.go`: non-enumerating local authorization/token readiness probe for doctor.
- `internal/googledirectory/probe_test.go`: readiness and secret-redaction tests.
- `internal/storage/directory.go`: directory work loading, immutable snapshot persistence, automatic-link transaction, and review transaction support.
- `internal/storage/directory_test.go`: validation and stable-digest unit tests.
- `db/migrations/00011_google_directory_identity.sql`: forward-only directory provenance and candidate schema.

### Modified files

- `internal/source/drive/oauth.go` and `internal/source/drive/oauth_test.go`: delegate shared OAuth mechanics while preserving the current Drive/Docs scopes and public API.
- `internal/config/config.go`, `internal/config/poc.go`, and their tests: load and validate optional directory configuration.
- `internal/cli/auth.go` and `internal/cli/auth_test.go`: add `auth google-directory` without changing `auth google`.
- `internal/ingest/service.go`, `internal/ingest/service_test.go`, and `internal/ingest/validate.go`: invoke optional enrichment only after durable completion and on unchanged reruns.
- `internal/cli/sync.go` and `internal/cli/sync_test.go`: render bounded directory counts.
- `internal/storage/entities.go`, `internal/storage/entities_test.go`, and `internal/storage/integration_test.go`: directory candidates, decisions, aliases, corrections, concurrency, and upgrade coverage.
- `internal/cli/review.go`, `internal/cli/review_test.go`, `internal/cli/storage.go`, and `internal/cli/storage_test.go`: show and accept directory-backed candidates and optionally verify a reviewer-supplied email.
- `internal/doctor/service.go`, `internal/doctor/service_test.go`, `internal/doctor/postgres_integration_test.go`, and `internal/cli/doctor_test.go`: optional directory readiness with warning semantics and migration `00011`.
- `cmd/stacks/main.go` and `cmd/stacks/main_test.go`: lazy composition of the separate authorizer, probe, adapter, directory service, and shared database pool.
- `Makefile`, `.env.example`, and `README.md`: operator commands, safe configuration, IT authorization contract, and acceptance boundaries.

---

### Task 1: Define directory identity contracts and deterministic policy

**Files:**
- Create: `internal/entity/directory.go`
- Create: `internal/entity/directory_test.go`
- Modify: `internal/entity/entity.go`
- Modify: `internal/entity/resolver_test.go`

**Interfaces:**
- Consumes: existing `NormalizeName`, `NormalizeEmail`, `ValidEmail`, `EntitySnapshot`, and accepted-alias resolver behavior.
- Produces:

```go
const DirectoryPolicyVersion = "directory-identity-v1"

type DirectoryQueryKind string
type EmailEvidence string
type DirectorySource string
type DirectoryOutcome string

type DirectoryQuery struct {
	Kind          DirectoryQueryKind
	Name          string
	Email         string
	EmailEvidence EmailEvidence
}

type DirectoryEmail struct {
	Value   string
	Primary bool
}

type DirectoryProfile struct {
	Provider    string
	SubjectID   string
	Source      DirectorySource
	DisplayName string
	Emails      []DirectoryEmail
	ObservedAt  time.Time
}

type DirectoryIdentityLink struct {
	Provider  string
	SubjectID string
	EntityID  string
}

type DirectoryEvaluation struct {
	Outcome       DirectoryOutcome
	EntityID      string
	CreatePerson  bool
	AcceptedEmail string
	Profile       *DirectoryProfile
	Candidates    []DirectoryProfile
}

type DirectoryPolicy struct { /* immutable approved-domain set */ }

func NewDirectoryPolicy(domains []string) (DirectoryPolicy, error)
func (policy DirectoryPolicy) Evaluate(
	query DirectoryQuery,
	profiles []DirectoryProfile,
	snapshots []EntitySnapshot,
	links []DirectoryIdentityLink,
) DirectoryEvaluation
func SourceBoundMailbox(surface, email, quote string) bool
```

- [ ] **Step 1: Write failing constants and validation tests**

```go
func TestNewDirectoryPolicyRejectsEmptyInvalidAndDuplicateDomains(t *testing.T) {
	for _, domains := range [][]string{
		nil,
		[]string{""},
		[]string{"@example.test"},
		[]string{"example.test", "EXAMPLE.TEST"},
	} {
		if _, err := NewDirectoryPolicy(domains); err == nil {
			t.Fatalf("NewDirectoryPolicy(%q) error = nil", domains)
		}
	}
}

func TestSourceBoundMailboxRequiresOneExactNamedMailbox(t *testing.T) {
	if !SourceBoundMailbox("Riya Chen", "riya.chen@corp.example", "Riya Chen <riya.chen@corp.example>") {
		t.Fatal("exact named mailbox was not source-bound")
	}
	for _, quote := range []string{
		"Alex Reviewer and Riya Chen <riya.chen@corp.example>",
		"Alex Reviewer <riya.chen@corp.example>",
		"Riya Chen <other@corp.example>",
	} {
		if SourceBoundMailbox("Riya Chen", "riya.chen@corp.example", quote) {
			t.Fatalf("SourceBoundMailbox accepted %q", quote)
		}
	}
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/entity -run 'TestNewDirectoryPolicy|TestSourceBoundMailbox'`

Expected: FAIL because directory contracts do not exist.

- [ ] **Step 3: Implement typed constants and strict normalization**

Define:

```go
const (
	DirectoryQueryEmail DirectoryQueryKind = "email"
	DirectoryQueryName  DirectoryQueryKind = "name"

	EmailEvidenceSourceBound      EmailEvidence = "source_bound"
	EmailEvidenceCitationVerified EmailEvidence = "citation_verified"
	EmailEvidenceReviewerSupplied  EmailEvidence = "reviewer_supplied"
	EmailEvidenceNone              EmailEvidence = "none"

	DirectorySourceDomainProfile DirectorySource = "domain_profile"
	DirectorySourceDomainContact DirectorySource = "domain_contact"

	DirectoryMatched   DirectoryOutcome = "matched"
	DirectoryNoMatch   DirectoryOutcome = "no_match"
	DirectoryAmbiguous DirectoryOutcome = "ambiguous"
	DirectoryReview    DirectoryOutcome = "review"
	DirectoryDisabled  DirectoryOutcome = "disabled"
	DirectoryNotConfigured DirectoryOutcome = "not_configured"
	DirectoryUnauthorized DirectoryOutcome = "unauthorized"
	DirectoryForbidden DirectoryOutcome = "forbidden"
	DirectoryRateLimited DirectoryOutcome = "rate_limited"
	DirectoryUnavailable DirectoryOutcome = "unavailable"
	DirectoryInvalidResponse DirectoryOutcome = "invalid_response"
	DirectoryResultLimitExceeded DirectoryOutcome = "result_limit_exceeded"
)
```

`NewDirectoryPolicy` must trim, lowercase, validate DNS-shaped domain labels,
and reject normalized duplicates. `SourceBoundMailbox` must use
`mail.ParseAddress` on the complete trimmed quote and require exact normalized
name and email equality; it must not search for a mailbox inside surrounding
prose.

- [ ] **Step 4: Write failing evaluation tests**

Cover these exact cases:

```go
func TestDirectoryPolicyAutoCreatesForUniqueSourceBoundApprovedEmail(t *testing.T)
func TestDirectoryPolicyUsesExistingUniqueAcceptedEmailOwner(t *testing.T)
func TestDirectoryPolicyKeepsCitationVerifiedEmailReviewOnly(t *testing.T)
func TestDirectoryPolicyKeepsNameOnlyProfilesReviewOnly(t *testing.T)
func TestDirectoryPolicyRejectsExternalDomainAndSharedContactAuthority(t *testing.T)
func TestDirectoryPolicyKeepsDuplicateEmailMatchesAmbiguous(t *testing.T)
func TestDirectoryPolicyKeepsConflictingAliasOrProviderLinkReviewOnly(t *testing.T)
func TestDirectoryPolicySortsProfilesDeterministically(t *testing.T)
```

The citation-verified test must use a directory profile whose email and display
name both match; it still must return `DirectoryReview`.

- [ ] **Step 5: Run evaluation tests and verify RED**

Run: `go test ./internal/entity -run 'TestDirectoryPolicy'`

Expected: FAIL because `Evaluate` is not implemented.

- [ ] **Step 6: Implement the minimal pure policy**

Implementation rules:

```go
// Auto authority requires all of:
// - email query
// - source_bound or reviewer_supplied evidence
// - approved email domain
// - exactly one DOMAIN_PROFILE with the exact normalized email
// - no conflicting active provider link
// - zero or one accepted email owner
```

Return `CreatePerson=true` when no entity owns the email. Return the existing
entity ID when exactly one owner exists. Return review/ambiguous for every
other case. Never place a name into `AcceptedEmail` or mutate the supplied
snapshots.

- [ ] **Step 7: Run entity tests**

Run: `go test ./internal/entity`

Expected: PASS, including the existing duplicate accepted-name ambiguity tests.

- [ ] **Step 8: Commit**

```bash
git add internal/entity
git commit -m "Define directory identity policy"
```

---

### Task 2: Separate Google authorization and add optional configuration

**Files:**
- Create: `internal/googleauth/oauth.go`
- Create: `internal/googleauth/oauth_test.go`
- Modify: `internal/source/drive/oauth.go`
- Modify: `internal/source/drive/oauth_test.go`
- Create: `internal/googledirectory/oauth.go`
- Create: `internal/googledirectory/oauth_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/poc.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/model_settings_test.go`
- Modify: `internal/cli/auth.go`
- Modify: `internal/cli/auth_test.go`

**Interfaces:**
- Consumes: existing Drive installed-app OAuth behavior and owner-only token files.
- Produces:

```go
// internal/googleauth
func NewAuthorizer(clientFile, tokenFile string, scopes []string, output io.Writer) *Authorizer
func NewAuthorizedHTTPClient(ctx context.Context, clientFile, tokenFile string, scopes []string) (*http.Client, error)

// internal/config
type GoogleDirectorySettings struct {
	Enabled         bool
	OAuthClientFile string
	OAuthTokenFile  string
	EmailDomains    []string
	Freshness       time.Duration
	RetryAfter      time.Duration
	MaxAttempts     int
}

// Add this field to PoCSettings.
Directory GoogleDirectorySettings

type GoogleAuthTarget string
const (
	GoogleAuthDrive     GoogleAuthTarget = "google"
	GoogleAuthDirectory GoogleAuthTarget = "google-directory"
)
func (settings PoCSettings) ValidateGoogleAuth(target GoogleAuthTarget) error

// internal/googledirectory
const ReadOnlyScope = "https://www.googleapis.com/auth/directory.readonly"
func NewAuthorizer(clientFile, tokenFile string, output io.Writer) *googleauth.Authorizer
func NewAuthorizedHTTPClient(ctx context.Context, clientFile, tokenFile string) (*http.Client, error)
```

- [ ] **Step 1: Move the installed-app lifecycle behind explicit scopes**

Copy the existing loopback callback, random state, token exchange, owner-only
token loading, atomic replacement, cancellation, and redacted errors into
`internal/googleauth/oauth.go`. The constructor must reject an empty scope list
and defensively copy `scopes`.

- [ ] **Step 2: Port and strengthen OAuth tests**

Move the existing behavioral tests to `internal/googleauth/oauth_test.go` and
add:

```go
func TestAuthorizerUsesOnlySuppliedScopes(t *testing.T)
func TestAuthorizerRejectsEmptyScopesBeforeListening(t *testing.T)
func TestNewAuthorizedHTTPClientDoesNotExposeCredentialFileContents(t *testing.T)
```

Use only synthetic client IDs, secrets, codes, and tokens.

- [ ] **Step 3: Make Drive OAuth a thin compatibility wrapper**

Keep these existing signatures unchanged:

```go
func NewAuthorizer(clientFile, tokenFile string, output io.Writer) *googleauth.Authorizer
func NewAuthorizedHTTPClient(ctx context.Context, clientFile, tokenFile string) (*http.Client, error)
```

Pass only `drive.DriveReadonlyScope` and `docs.DocumentsReadonlyScope`. Retain
one public regression test asserting the exact Drive/Docs scope set.

- [ ] **Step 4: Add directory-only OAuth wrappers and scope tests**

`internal/googledirectory/oauth.go` must pass exactly `ReadOnlyScope`, never the
Drive or Docs scopes. Add a test that parses the generated authorization URL
and compares the complete scope list.

- [ ] **Step 5: Write failing configuration tests**

Add environment constants:

```go
GoogleDirectoryEnabledEnvironmentVariable     = "STACKS_GOOGLE_DIRECTORY_ENABLED"
GoogleDirectoryClientFileEnvironmentVariable  = "STACKS_GOOGLE_DIRECTORY_OAUTH_CLIENT_FILE"
GoogleDirectoryTokenFileEnvironmentVariable   = "STACKS_GOOGLE_DIRECTORY_OAUTH_TOKEN_FILE"
GoogleDirectoryDomainsEnvironmentVariable     = "STACKS_GOOGLE_DIRECTORY_EMAIL_DOMAINS"
GoogleDirectoryFreshnessEnvironmentVariable   = "STACKS_GOOGLE_DIRECTORY_FRESHNESS"
GoogleDirectoryRetryAfterEnvironmentVariable  = "STACKS_GOOGLE_DIRECTORY_RETRY_AFTER"
GoogleDirectoryMaxAttemptsEnvironmentVariable = "STACKS_GOOGLE_DIRECTORY_MAX_ATTEMPTS"
```

Tests must prove disabled is the zero-configuration default, enabled requires
both paths and at least one valid normalized domain, paths cannot contain
surrounding whitespace, freshness/retry durations are positive, and max
attempts is between 1 and 3.

- [ ] **Step 6: Run configuration tests and verify RED**

Run: `go test ./internal/config -run 'TestGoogleDirectory|TestPoCSettingsValidateGoogleAuth'`

Expected: FAIL because directory settings do not exist.

- [ ] **Step 7: Load and validate configuration**

Use named defaults:

```go
const (
	defaultGoogleDirectoryFreshness  = 24 * time.Hour
	defaultGoogleDirectoryRetryAfter = 15 * time.Minute
	defaultGoogleDirectoryMaxAttempts = 3
)
```

Parse comma-separated domains with the same trim-and-drop-empty shape as title
sets, then let `entity.NewDirectoryPolicy` perform semantic validation.
`Validate(CommandSync)` and `Validate(CommandDoctor)` validate directory
settings only when enabled. `ValidateGoogleAuth(GoogleAuthDirectory)` requires
the directory paths regardless of enabled state so authorization can be
performed before enabling the feature.

- [ ] **Step 8: Extend `AuthCommand` without scope crossover**

Change the command to:

```go
type AuthCommand struct {
	GoogleDrive     GoogleAuthorizer
	GoogleDirectory GoogleAuthorizer
}
```

`auth google` invokes only `GoogleDrive`; `auth google-directory` invokes only
`GoogleDirectory`; every other argument returns:

```text
usage: stacks auth google | stacks auth google-directory
```

Tests must use call-counting fakes and secret sentinel errors to prove the
wrong authorizer is never constructed or invoked and private error values are
not written to stdout.

- [ ] **Step 9: Run focused tests**

Run: `go test ./internal/googleauth ./internal/source/drive ./internal/googledirectory ./internal/config ./internal/cli -run 'TestAuthor|TestGoogleDirectory|TestAuthCommand'`

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/googleauth internal/googledirectory/oauth.go internal/googledirectory/oauth_test.go internal/source/drive/oauth.go internal/source/drive/oauth_test.go internal/config internal/cli/auth.go internal/cli/auth_test.go
git commit -m "Separate Google directory authorization"
```

---

### Task 3: Implement the bounded Google People directory adapter

**Files:**
- Create: `internal/directory/lookup.go`
- Create: `internal/googledirectory/client.go`
- Create: `internal/googledirectory/client_test.go`

**Interfaces:**
- Consumes: `entity.DirectoryQuery`, `entity.DirectoryProfile`, and the directory-readonly HTTP client from Task 2.
- Produces:

```go
// internal/directory
type LookupResult struct {
	Outcome    entity.DirectoryOutcome
	Profiles   []entity.DirectoryProfile
	RetryAfter time.Duration
}

type Lookup interface {
	Search(context.Context, entity.DirectoryQuery) (LookupResult, error)
}

// internal/googledirectory
func NewClient(ctx context.Context, httpClient *http.Client, maximumResults int) (*Client, error)
func (client *Client) Search(ctx context.Context, query entity.DirectoryQuery) (directory.LookupResult, error)
```

- [ ] **Step 1: Write request-shape tests with a fake HTTP transport**

For both email and name queries assert:

```text
GET /v1/people:searchDirectoryPeople
readMask=metadata,names,emailAddresses
sources=DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE
pageSize=25
query=<the supplied prefix>
```

The test transport must inspect the request and return synthetic JSON with
`resourceName`, `metadata.sources`, `names`, and `emailAddresses`.

- [ ] **Step 2: Run adapter tests and verify RED**

Run: `go test ./internal/googledirectory -run 'TestClientSearch'`

Expected: FAIL because `Client` does not exist.

- [ ] **Step 3: Implement client construction and profile conversion**

Construct `people.Service` with `option.WithHTTPClient`. Use a named default
page size of 25 and reject `maximumResults < 1`. Convert only:

```go
entity.DirectoryProfile{
	Provider:    "google_people",
	SubjectID:   person.ResourceName,
	Source:      entity.DirectorySourceDomainProfile,
	DisplayName: primaryName(person.Names),
	Emails:      normalizedEmails(person.EmailAddresses),
	ObservedAt:  observedSourceTime(person.Metadata),
}
```

Require nonempty subject ID, one nonempty display name, at least one valid
email, and a domain-profile source. Invalid provider records make the complete
lookup `invalid_response`; partial data must never earn authority.

- [ ] **Step 4: Add pagination, deduplication, and limit tests**

Tests must prove:

- repeated provider subjects and emails are deduplicated deterministically;
- page tokens are followed only while total results remain within the bound;
- a next page beyond the bound returns `result_limit_exceeded` and no
  authoritative profiles;
- returned profile and email order is stable regardless of provider order.

- [ ] **Step 5: Add bounded error-classification tests**

Use `googleapi.Error` responses for 401, 403, 429 with `Retry-After`, 500, and
400. Expected outcomes:

```text
401 -> unauthorized
403 -> forbidden
429 -> rate_limited
500/502/503/504 -> unavailable
other 4xx -> invalid_response
```

Context cancellation and deadlines return canonical context errors. Every
other transport failure becomes `unavailable` without returning provider error
text.

- [ ] **Step 6: Implement exact local filtering**

The API query is prefix-based. For an email query, retain only profiles
containing the exact normalized email. If none remain, return `no_match`; do
not treat prefix matches as candidates. Name queries may return bounded
profiles for local policy ranking.

- [ ] **Step 7: Run adapter and race tests**

Run:

```bash
go test ./internal/directory ./internal/googledirectory
go test -race ./internal/googledirectory
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/directory/lookup.go internal/googledirectory/client.go internal/googledirectory/client_test.go
git commit -m "Add Google directory lookup adapter"
```

---

### Task 4: Add forward-only directory provenance schema

**Files:**
- Create: `db/migrations/00011_google_directory_identity.sql`
- Modify: `internal/storage/migration_test.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/doctor/service.go`
- Modify: `internal/doctor/postgres_integration_test.go`

**Interfaces:**
- Consumes: existing mentions, proposals, candidates, decisions, entities, and alias assertions through migration `00010`.
- Produces: immutable lookup/profile evidence, directory-backed candidates, and decision-scoped identity assertions.

- [ ] **Step 1: Write the migration contract test**

Require all of these fragments and reject `DELETE FROM`, destructive updates to
existing identity rows, and `-- +goose Down`:

```text
CREATE TABLE stacks.directory_profile_snapshots
CREATE TABLE stacks.directory_profile_emails
CREATE TABLE stacks.directory_lookup_attempts
CREATE TABLE stacks.directory_lookup_matches
CREATE TABLE stacks.entity_directory_identity_assertions
ADD COLUMN directory_profile_snapshot_id
resolution_candidates_one_source
validate_effective_directory_identity
```

- [ ] **Step 2: Run the contract test and verify RED**

Run: `go test ./internal/storage -run TestGoogleDirectoryMigration`

Expected: FAIL because migration `00011` is absent.

- [ ] **Step 3: Create the forward schema**

Create these exact durable shapes, using bounded checks and 32-byte digests:

```sql
CREATE TABLE stacks.directory_profile_snapshots (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider IN ('google_people')),
    provider_subject_id text NOT NULL CHECK (btrim(provider_subject_id) <> ''),
    source_type text NOT NULL CHECK (source_type IN ('domain_profile', 'domain_contact')),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    observed_at timestamptz,
    recorded_at timestamptz NOT NULL,
    digest bytea NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    UNIQUE (provider, provider_subject_id, digest)
);

CREATE TABLE stacks.directory_profile_emails (
    snapshot_id uuid NOT NULL REFERENCES stacks.directory_profile_snapshots(id),
    normalized_email text NOT NULL CHECK (
        normalized_email = lower(btrim(normalized_email))
        AND normalized_email LIKE '%@%'
    ),
    is_primary boolean NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (snapshot_id, normalized_email),
    UNIQUE (snapshot_id, position)
);

CREATE TABLE stacks.directory_lookup_attempts (
    id uuid PRIMARY KEY,
    mention_id uuid NOT NULL REFERENCES stacks.mentions(id),
    provider text NOT NULL CHECK (provider IN ('google_people')),
    query_kind text NOT NULL CHECK (query_kind IN ('email', 'name')),
    email_evidence text NOT NULL CHECK (
        email_evidence IN ('none', 'source_bound', 'citation_verified', 'reviewer_supplied')
    ),
    query_digest bytea NOT NULL CHECK (octet_length(query_digest) = 32),
    policy_version text NOT NULL CHECK (btrim(policy_version) <> ''),
    outcome text NOT NULL CHECK (outcome IN (
        'matched', 'no_match', 'ambiguous', 'review', 'disabled',
        'not_configured', 'unauthorized', 'forbidden', 'rate_limited',
        'unavailable', 'invalid_response', 'result_limit_exceeded'
    )),
    attempt_count integer NOT NULL CHECK (attempt_count >= 0),
    retry_after timestamptz,
    recorded_at timestamptz NOT NULL,
    digest bytea NOT NULL UNIQUE CHECK (octet_length(digest) = 32)
);

CREATE TABLE stacks.directory_lookup_matches (
    lookup_attempt_id uuid NOT NULL REFERENCES stacks.directory_lookup_attempts(id),
    snapshot_id uuid NOT NULL REFERENCES stacks.directory_profile_snapshots(id),
    rank integer NOT NULL CHECK (rank >= 0),
    reason text NOT NULL CHECK (reason IN ('exact_email', 'name_candidate')),
    PRIMARY KEY (lookup_attempt_id, snapshot_id),
    UNIQUE (lookup_attempt_id, rank)
);

ALTER TABLE stacks.resolution_candidates
    ALTER COLUMN entity_id DROP NOT NULL,
    ADD COLUMN directory_profile_snapshot_id uuid
        REFERENCES stacks.directory_profile_snapshots(id),
    ADD CONSTRAINT resolution_candidates_one_source CHECK (
        (entity_id IS NOT NULL)::integer
        + (directory_profile_snapshot_id IS NOT NULL)::integer = 1
    );

CREATE UNIQUE INDEX resolution_candidates_directory_profile_unique
    ON stacks.resolution_candidates (proposal_id, directory_profile_snapshot_id)
    WHERE directory_profile_snapshot_id IS NOT NULL;

CREATE TABLE stacks.entity_directory_identity_assertions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    decision_id uuid NOT NULL REFERENCES stacks.resolution_decisions(id),
    entity_id uuid NOT NULL REFERENCES stacks.entities(id),
    lookup_attempt_id uuid NOT NULL REFERENCES stacks.directory_lookup_attempts(id),
    snapshot_id uuid NOT NULL REFERENCES stacks.directory_profile_snapshots(id),
    provider text NOT NULL CHECK (provider IN ('google_people')),
    provider_subject_id text NOT NULL CHECK (btrim(provider_subject_id) <> ''),
    recorded_at timestamptz NOT NULL,
    UNIQUE (decision_id, provider, provider_subject_id)
);
```

Add a deferrable constraint-trigger function
`stacks.validate_effective_directory_identity(provider, provider_subject_id)`
that rejects more than one distinct entity among non-superseded, currently
admissible accepted/created decisions for the same provider identity. Validate
both old and new identities when an assertion or decision changes.

Use this validation query inside the function:

```sql
IF (
    SELECT count(DISTINCT assertion.entity_id)
    FROM stacks.entity_directory_identity_assertions AS assertion
    JOIN stacks.resolution_decisions AS decision
      ON decision.id = assertion.decision_id
    WHERE assertion.provider = checked_provider
      AND assertion.provider_subject_id = checked_subject
      AND decision.superseded_by_id IS NULL
      AND decision.outcome IN ('accepted', 'created')
      AND decision.currently_admissible
) > 1 THEN
    RAISE EXCEPTION 'directory identity has conflicting effective entities';
END IF;
```

Create deferred constraint triggers on
`entity_directory_identity_assertions` insert/update/delete and
`resolution_decisions` update/delete. The decision trigger must validate every
directory identity referenced by both `OLD.id` and `NEW.id`.

- [ ] **Step 4: Add upgrade-preservation and constraint tests**

In an isolated migration schema created from `STACKS_TEST_MIGRATION_DATABASE_URL`:

- migrate legacy fixtures through `00010`;
- record counts and digests for entities, mentions, proposals, decisions,
  aliases, observations, signals, and analyses;
- apply `00011`;
- assert every count and digest is unchanged;
- insert one entity-backed candidate and one directory-backed candidate;
- assert zero-source and two-source candidates fail;
- assert conflicting effective provider identities fail at commit;
- supersede the first decision and assert the corrected link can commit.

- [ ] **Step 5: Update doctor migration requirements**

Add `11` to `requiredMigrationVersions()` only after the RED migration test
exists. Update the PostgreSQL doctor integration expectation to current through
`00011`.

- [ ] **Step 6: Apply and inspect the migration locally**

Run:

```bash
make db-up
make db-migrate
make db-status
```

Expected: migrations through `00011` applied with no pending migrations.

- [ ] **Step 7: Run migration integration tests**

Run:

```bash
go test ./internal/storage -run 'TestGoogleDirectoryMigration|TestMigrationUpgrade'
STACKS_TEST_DATABASE_URL="$STACKS_TEST_DATABASE_URL" \
STACKS_TEST_MIGRATION_DATABASE_URL="$STACKS_TEST_MIGRATION_DATABASE_URL" \
go test ./internal/storage ./internal/doctor -run 'TestMigration|TestPostgresProbe'
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add db/migrations/00011_google_directory_identity.sql internal/storage/migration_test.go internal/storage/integration_test.go internal/doctor
git commit -m "Persist directory identity provenance"
```

---

### Task 5: Implement directory persistence and atomic authority

**Files:**
- Create: `internal/storage/directory.go`
- Create: `internal/storage/directory_test.go`
- Modify: `internal/directory/lookup.go`
- Modify: `internal/storage/entities.go`
- Modify: `internal/storage/entities_test.go`
- Modify: `internal/storage/integration_test.go`

**Interfaces:**
- Consumes: schema from Task 4 and entity policy output from Task 1.
- Produces:

```go
// internal/directory
type PendingMention struct {
	MentionID       string
	ProposalID      string
	Surface         string
	NormalizedName  string
	ProposedEmail   string
	NameQuote       string
	EmailQuote      string
}

type Workset struct {
	Mentions []PendingMention
	Reused   int
}

type IdentityState struct {
	Snapshots []entity.EntitySnapshot
	Links     []entity.DirectoryIdentityLink
}

type PersistInput struct {
	Mention      PendingMention
	Query        entity.DirectoryQuery
	Lookup       LookupResult
	Evaluation   entity.DirectoryEvaluation
	AttemptCount int
	RecordedAt   time.Time
	RetryAfter   *time.Time
}

type PersistResult struct {
	AutoResolved bool
	EntityID     string
}

type Repository interface {
	LoadWork(context.Context, string, time.Time, time.Duration, time.Duration) (Workset, error)
	LoadIdentityState(context.Context) (IdentityState, error)
	Persist(context.Context, PersistInput) (PersistResult, error)
}
```

- [ ] **Step 1: Write stable-digest and validation tests**

Add tests for:

```go
func TestDirectoryProfileDigestIgnoresProviderOrder(t *testing.T)
func TestDirectoryLookupDigestIncludesMentionQueryPolicyAndOutcome(t *testing.T)
func TestValidateDirectoryPersistInputRejectsPrivateOrUnboundedReason(t *testing.T)
func TestDirectoryDecisionDigestPreservesLegacyDigestWhenNoDirectoryEvidence(t *testing.T)
```

Directory-backed decision digests use the version prefix
`stacks.resolution-decision.v2.directory`; existing non-directory decision
inputs must continue producing the current digest byte-for-byte.

- [ ] **Step 2: Run storage unit tests and verify RED**

Run: `go test ./internal/storage -run 'TestDirectory|TestResolutionDecisionDigestPreserves'`

Expected: FAIL because directory storage functions do not exist.

- [ ] **Step 3: Implement `LoadWork`**

Select current, admissible pending proposals for one completed derivation,
including exact evidence quotes needed only in process to classify
`SourceBoundMailbox`. Exclude:

- proposals with an effective decision;
- mentions or extraction runs marked non-admissible;
- attempts with fresh `matched`, `no_match`, `ambiguous`, or `review` outcomes;
- transient attempts whose `retry_after` is still in the future.

Return the number of fresh conclusive attempts as `Reused`. Never return query
values in an error.

- [ ] **Step 4: Implement current identity-state loading**

`LoadIdentityState` must return the same currently admissible accepted aliases
used by `EntitySnapshots`, plus non-superseded currently admissible directory
identity assertions. Sort snapshots, aliases, and links deterministically.
Reuse the existing alias query rather than defining a second acceptance rule.

- [ ] **Step 5: Implement immutable attempt and snapshot persistence**

Within one transaction:

- canonicalize and sort profiles/emails;
- derive UUIDv5 IDs from content digests;
- insert snapshots and emails idempotently;
- insert the attempt and match rows idempotently;
- reject any stored row whose immutable payload conflicts with the same ID.

Expected provider failures are persisted as bounded outcomes with zero profile
rows and no raw error.

- [ ] **Step 6: Implement atomic exact-email authority**

For `Evaluation.Outcome == DirectoryMatched`:

1. acquire transaction advisory locks for the normalized email and
   `provider + subject`;
2. reload current admissible email owners and provider identity owners;
3. downgrade to a persisted review candidate if ownership is now ambiguous or
   conflicting;
4. create a person when `CreatePerson` is true, using the directory display
   name only as presentation;
5. insert a directory-v2 accepted/created decision;
6. assert the accepted email only;
7. insert the provider identity assertion;
8. update the proposal status.

Do not call `insertMentionAliasAssertions`; add a specialized helper that
inserts only the explicitly supplied email alias.

- [ ] **Step 7: Persist review candidates**

For name, citation-verified email, shared-contact, duplicate, or conflict
evaluations, insert directory-backed `resolution_candidates` ordered after any
existing entity candidates. Use only finite confidence values and bounded
reasons `directory exact email requires review` or
`directory name candidate requires review`.

- [ ] **Step 8: Add PostgreSQL integration tests**

Prove:

- repeated identical persistence creates one snapshot, attempt, match,
  decision, alias, and link;
- automatic resolution creates a person when no owner exists;
- automatic resolution uses one existing email owner when present;
- only the email alias is admitted automatically;
- a name candidate creates no entity or decision;
- injected failure after entity creation rolls back every directory row;
- two concurrent exact-email operations cannot create duplicate authority;
- a later changed profile creates a new snapshot without mutating the old one.

- [ ] **Step 9: Run storage tests**

Run:

```bash
go test ./internal/storage
STACKS_TEST_DATABASE_URL="$STACKS_TEST_DATABASE_URL" go test ./internal/storage -run 'TestDirectory'
go test -race ./internal/storage
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/storage internal/directory/lookup.go
git commit -m "Store directory resolution evidence"
```

---

### Task 6: Orchestrate fail-soft lookup, retry, and resume

**Files:**
- Create: `internal/directory/service.go`
- Create: `internal/directory/service_test.go`
- Modify: `internal/observability/decision_test.go`

**Interfaces:**
- Consumes: `directory.Lookup` from Task 3 and `directory.Repository` from Task 5.
- Produces:

```go
type Summary struct {
	Attempted   int
	Reused      int
	Matched     int
	Review      int
	NoMatch     int
	Ambiguous   int
	Unavailable int
}

type Service struct {
	Lookup        Lookup
	Repository    Repository
	Policy        entity.DirectoryPolicy
	Enabled       bool
	Freshness     time.Duration
	RetryAfter    time.Duration
	MaxAttempts   int
	Tracer        trace.Tracer
	Decisions     DecisionRecorder
	Now           func() time.Time
	Wait          func(context.Context, time.Duration) error
}

type DecisionRecorder interface {
	Record(context.Context, observability.DecisionObservation) error
}

func (service *Service) Enrich(ctx context.Context, derivationID string) (Summary, error)
```

- [ ] **Step 1: Write fail-soft and disabled tests**

Tests must prove:

- disabled service returns a zero summary and performs no repository or lookup
  calls;
- a missing optional lookup returns `Unavailable=1` without an error;
- repository work-load or persistence errors increment `Unavailable` and do
  not expose a sentinel private error;
- context cancellation is the only operational error returned to the caller.

- [ ] **Step 2: Run service tests and verify RED**

Run: `go test ./internal/directory -run TestService`

Expected: FAIL because `Service` does not exist.

- [ ] **Step 3: Implement eligibility construction**

Load `IdentityState` once at the start of enrichment and pass its snapshots and
links to every pure policy evaluation. For each pending mention:

- build a citation-verified email query when a valid proposed email exists;
- upgrade it to `source_bound` only when `SourceBoundMailbox` succeeds;
- otherwise use a normalized name query;
- skip a mention with neither eligible email nor name.

Never combine separately cited names and emails into one source-bound identity.

- [ ] **Step 4: Implement bounded retry ownership**

Call `Lookup.Search` at most `MaxAttempts`. Retry only `rate_limited` and
`unavailable`. Use provider `RetryAfter` when positive, otherwise configured
`RetryAfter`. The injected `Wait` must stop immediately on cancellation.
Unauthorized, forbidden, invalid response, result limit, no match, review, and
ambiguous outcomes do not retry.

- [ ] **Step 5: Persist every terminal bounded outcome**

Call the pure policy only for successful profile results, then persist one
terminal attempt. If persistence fails, count `Unavailable` and continue to
the next mention. Aggregate counts without names, emails, subjects, proposal
IDs, or raw errors.

- [ ] **Step 6: Add reuse and retry tests**

Cover:

```go
func TestServiceReusesFreshDurableAttemptsWithoutNetwork(t *testing.T)
func TestServiceRetriesRateLimitThenPersistsMatch(t *testing.T)
func TestServiceDoesNotRetryUnauthorizedOrInvalidResponse(t *testing.T)
func TestServiceCancellationDuringBackoffStopsImmediately(t *testing.T)
func TestServiceContinuesAfterOneMentionFails(t *testing.T)
```

- [ ] **Step 7: Add privacy-safe tracing and decisions**

Use one `stacks.directory.enrich` span per derivation and one
`directory_lookup` decision per attempted query. Attributes may include query
kind, bounded outcome, attempt count, duration, and policy version only.
Explicitly finish successful spans with `observability.FinishSpan`.

- [ ] **Step 8: Run directory tests and race tests**

Run:

```bash
go test ./internal/directory
go test -race ./internal/directory
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/directory internal/observability/decision_test.go
git commit -m "Enrich unresolved identities on demand"
```

---

### Task 7: Run enrichment after durable ingestion completion

**Files:**
- Modify: `internal/ingest/service.go`
- Modify: `internal/ingest/service_test.go`
- Modify: `internal/ingest/validate.go`
- Modify: `internal/cli/sync.go`
- Modify: `internal/cli/sync_test.go`

**Interfaces:**
- Consumes:

```go
type IdentityEnricher interface {
	Enrich(context.Context, string) (directory.Summary, error)
}
```

- Produces:

```go
type Result struct {
	// existing fields
	Directory directory.Summary
}

type Summary struct {
	// existing fields
	Directory directory.Summary
}
```

- [ ] **Step 1: Write ordering tests**

Use call-recording fakes to prove:

```text
new derivation: PrepareVersion -> model -> CompleteVersion -> Enrich
complete derivation: PrepareVersion -> Enrich, with no model call
failed completion: no Enrich call
busy derivation: no Enrich call
```

- [ ] **Step 2: Run focused ingestion tests and verify RED**

Run: `go test ./internal/ingest -run 'TestSync.*Enrich|TestSync.*Directory'`

Expected: FAIL because `IdentityEnricher` is absent.

- [ ] **Step 3: Add optional post-completion enrichment**

Add `IdentityEnricher` to `ingest.Service`. Invoke it only after
`CompleteVersion` succeeds or `PrepareVersion` returns complete. Store its
summary on the document result and aggregate it into the overall summary.

If enrichment returns cancellation/deadline, preserve already completed
results and return the canonical context error. Every other enrichment error
must be converted to `Directory.Unavailable++` and must not change document
`Outcome`.

- [ ] **Step 4: Preserve extraction and lease behavior**

Do not add directory policy/version to the extraction derivation digest.
Directory availability must not create new model extraction runs. Do not keep
the extraction lease while making directory network calls.

- [ ] **Step 5: Add regression tests for model-email safety**

Keep and extend:

```go
func TestSyncKeepsSeparatelyCitedAlexNameAndBobEmailPendingWithoutTeachingAliases(t *testing.T)
func TestSyncNeverUsesCooccurringModelEmailForAutomaticResolution(t *testing.T)
```

Assert the directory query may be citation-verified, but the resulting decision
cannot be automatic unless the strict source-bound contract succeeds.

- [ ] **Step 6: Render bounded directory counts**

Append these fields to the final summary line only:

```text
directory_attempted=<n> directory_reused=<n> directory_matched=<n>
directory_review=<n> directory_no_match=<n>
directory_ambiguous=<n> directory_unavailable=<n>
```

Do not add per-document query values or provider subjects.

- [ ] **Step 7: Run ingestion and CLI tests**

Run:

```bash
go test ./internal/ingest ./internal/cli -run 'TestSync'
go test -race ./internal/ingest ./internal/cli
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/ingest internal/cli/sync.go internal/cli/sync_test.go
git commit -m "Run additive directory enrichment after sync"
```

---

### Task 8: Complete directory-backed review and correction lifecycle

**Files:**
- Modify: `internal/storage/entities.go`
- Modify: `internal/storage/entities_test.go`
- Modify: `internal/storage/integration_test.go`
- Modify: `internal/cli/review.go`
- Modify: `internal/cli/review_test.go`
- Modify: `internal/cli/storage.go`
- Modify: `internal/cli/storage_test.go`
- Modify: `internal/directory/service.go`
- Modify: `internal/directory/service_test.go`

**Interfaces:**
- Consumes: directory-backed candidates and snapshots persisted by Task 5.
- Produces:

```go
type ReviewCandidate struct {
	EntityID          string
	DirectoryProfileID string
	DisplayName       string
	MaskedEmail       string
	Source            string
	Confidence        *float64
	Reason            string
}

type AcceptDirectoryInput struct {
	ProposalID        string
	DirectoryProfileID string
	EntityID          string // empty means create
}

type ReviewerEmailVerifier interface {
	VerifyReviewerEmail(context.Context, string) (directory.ReviewerVerification, error)
}

type ReviewerVerification struct {
	Query        entity.DirectoryQuery
	Lookup       LookupResult
	Evaluation   entity.DirectoryEvaluation
	AttemptCount int
	RecordedAt   time.Time
	RetryAfter   *time.Time
}

func (service *Service) VerifyReviewerEmail(
	ctx context.Context,
	email string,
) (ReviewerVerification, error)

// Add this optional field to storage.CreateReviewPersonInput.
DirectoryVerification *directory.ReviewerVerification
```

- [ ] **Step 1: Write private projection tests**

Prove list output remains bounded and masked while `review show` displays the
explicit local detail needed for a decision. Neither path may include provider
subject IDs or raw API errors.

Use masking that retains the domain and at most the first local-part rune:

```text
r***@corp.example
```

- [ ] **Step 2: Add an explicit directory acceptance command**

Add:

```text
stacks review accept-directory <proposal-id> <directory-profile-id> [--entity <entity-id>]
```

Without `--entity`, create a person using the snapshot display name. With
`--entity`, link the profile to that existing person. The command must require
exact IDs and never select the highest-ranked candidate implicitly.

- [ ] **Step 3: Implement the atomic storage transition**

The transaction must:

- verify the proposal is pending and the snapshot is a candidate for it;
- reject a conflicting active provider identity;
- create or lock the chosen entity;
- insert an accepted/created directory-v2 decision;
- assert the source mention name because a reviewer explicitly linked it;
- insert the provider identity assertion;
- update proposal status.

- [ ] **Step 4: Carry directory evidence through corrections**

When correcting a directory-backed effective decision, copy its lookup and
snapshot evidence to the replacement decision, assert the reviewed name for
the new entity, and let the deferrable provider-identity constraint confirm
that only the replacement is effective. The old decision and assertion remain
immutable history.

- [ ] **Step 5: Verify reviewer-supplied emails additively**

When `review create --email` is used and directory integration is enabled,
call `VerifyReviewerEmail` with `EmailEvidenceReviewerSupplied` before the
storage transition. The verifier loads current `IdentityState`, applies the
same approved-domain and provider-link policy, and returns only bounded lookup
metadata:

- unique exact domain profile: persist the snapshot and identity assertion with
  the created decision;
- no match or unavailable: create the reviewer-authorized entity and email
  alias without a provider link;
- provider identity conflict: return a bounded conflict and make no writes.

Directory unavailability must never discard the explicit reviewer decision.

- [ ] **Step 6: Add lifecycle integration tests**

Prove:

- accepting a name-only candidate creates one accepted name alias;
- future exact-name resolution auto-resolves;
- accepting the same normalized name for two entities makes future resolution
  ambiguous;
- rejection preserves candidate and lookup evidence;
- correction removes old authority from current snapshots without deleting
  history;
- reviewer email verification failure is additive;
- conflicting provider identity is transactional and visible for review.

- [ ] **Step 7: Run review and integration tests**

Run:

```bash
go test ./internal/cli ./internal/storage ./internal/directory -run 'TestReview|TestDirectory.*Decision|TestResolver'
STACKS_TEST_DATABASE_URL="$STACKS_TEST_DATABASE_URL" go test ./internal/storage -run 'TestDirectory.*Review|TestAlias'
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/storage internal/cli internal/directory
git commit -m "Add directory identity review lifecycle"
```

---

### Task 9: Wire optional directory readiness and runtime composition

**Files:**
- Create: `internal/googledirectory/probe.go`
- Create: `internal/googledirectory/probe_test.go`
- Modify: `internal/doctor/service.go`
- Modify: `internal/doctor/service_test.go`
- Modify: `internal/cli/doctor_test.go`
- Modify: `cmd/stacks/main.go`
- Modify: `cmd/stacks/main_test.go`

**Interfaces:**
- Consumes: settings, OAuth wrappers, adapter, service, and storage contracts from prior tasks.
- Produces:

```go
type DirectoryProbe interface {
	CheckAuthorization(context.Context) error
}

const (
	CheckDirectoryConfiguration CheckName = "directory.configuration"
	CheckDirectoryAuthorization CheckName = "directory.authorization"
)
```

- [ ] **Step 1: Write doctor optional-state tests**

Required results:

```text
disabled -> configuration ok, authorization warning "not checked because disabled"
enabled and locally ready -> both ok
enabled and missing/invalid token -> configuration ok, authorization warning
enabled and forbidden/unavailable -> authorization warning
context canceled -> report.Err is canonical cancellation
```

No directory warning may make `Report.Healthy()` false.

- [ ] **Step 2: Implement a non-enumerating probe**

The probe may:

- validate OAuth client and owner-only token files;
- ask the OAuth token source to obtain or refresh a token;
- confirm the configured authorization was constructed for
  `directory.readonly`.

It must not call `searchDirectoryPeople`, list profiles, persist data, or print
token fields. If live API capability cannot be checked without a person query,
the success message must say authorization is locally ready and live lookup is
unexercised.

- [ ] **Step 3: Preserve restricted disclosure ordering**

In restricted mode, the existing Bedrock disclosure check still precedes every
Google Drive or directory network/token operation. Add call-order tests proving
a failed disclosure gate invokes neither Drive nor directory probes.

- [ ] **Step 4: Extend the composition runtime**

Add lazy constructors:

```go
newDriveAuthorizer     func(string, string, io.Writer) cli.GoogleAuthorizer
newDirectoryAuthorizer func(string, string, io.Writer) cli.GoogleAuthorizer
newDoctorDirectory     func(config.GoogleDirectorySettings) doctor.DirectoryProbe
newDirectoryLookup     func(context.Context, config.GoogleDirectorySettings) (directory.Lookup, error)
```

Open one PostgreSQL pool for sync and adapt it to both `ingest.Repository` and
`directory.Repository`. Construct the People client only when directory is
enabled. Inject `directory.Service` into `ingest.Service`; otherwise inject
`nil`. Inject the same directory service as the optional
`ReviewerEmailVerifier` for review commands so reviewer-supplied email checks
use the identical policy, scope, retry, and persistence boundary.

- [ ] **Step 5: Add lazy-construction tests**

Prove:

- disabled sync never constructs directory OAuth, client, or probe;
- `auth google` constructs only Drive authorizer;
- `auth google-directory` constructs only directory authorizer;
- `doctor` constructs a directory probe only when enabled;
- directory construction failure cannot occur before the restricted disclosure
  gate;
- no doctor path invokes a model runtime or directory search.

- [ ] **Step 6: Add bounded observability assertions**

Test captured spans and decisions for allowed fields. Search serialized test
telemetry for sentinel name, email, subject ID, token, and query values and
require zero matches.

- [ ] **Step 7: Run command, doctor, and race tests**

Run:

```bash
go test ./cmd/stacks ./internal/doctor ./internal/googledirectory
go test -race ./cmd/stacks ./internal/doctor ./internal/googledirectory
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/stacks internal/doctor internal/googledirectory/probe.go internal/googledirectory/probe_test.go
git commit -m "Wire optional directory readiness"
```

---

### Task 10: Document IT operation and perform complete verification

**Files:**
- Modify: `README.md`
- Modify: `.env.example`
- Modify: `Makefile`
- Modify: `docs/superpowers/specs/2026-07-24-google-directory-identity-enrichment-design.md`

**Interfaces:**
- Consumes: completed commands and environment names from Tasks 1-9.
- Produces: operator setup, least-privilege authorization, revocation, privacy, and acceptance documentation.

- [ ] **Step 1: Add safe environment placeholders**

Add:

```dotenv
# Optional Google Workspace directory identity enrichment. This uses a
# separate OAuth client/token and never broadens the Drive token.
STACKS_GOOGLE_DIRECTORY_ENABLED=false
STACKS_GOOGLE_DIRECTORY_OAUTH_CLIENT_FILE=/absolute/path/outside/repository/google-directory-oauth-client.json
STACKS_GOOGLE_DIRECTORY_OAUTH_TOKEN_FILE=/absolute/path/outside/repository/stacks-google-directory-token.json
STACKS_GOOGLE_DIRECTORY_EMAIL_DOMAINS=corp.example
STACKS_GOOGLE_DIRECTORY_FRESHNESS=24h
STACKS_GOOGLE_DIRECTORY_RETRY_AFTER=15m
STACKS_GOOGLE_DIRECTORY_MAX_ATTEMPTS=3
```

Do not add real domains, client IDs, tokens, emails, or directory contents.

- [ ] **Step 2: Add a Make target**

Add `auth-google-directory`:

```make
auth-google-directory:
	@test -f "$(ENV_FILE)" || (echo "copy .env.example to $(ENV_FILE) and configure Google directory OAuth paths" >&2; exit 1)
	@set -a; . "$(ENV_FILE)"; set +a; go run ./cmd/stacks auth google-directory
```

Add it to `.PHONY`.

- [ ] **Step 3: Document the IT contract**

README must state:

- People API method and exact scope;
- separate Drive and directory OAuth clients/tokens;
- installed-app OAuth only; no domain-wide delegation;
- directory fields read and fields deliberately not requested;
- domain profiles only for automatic email authority;
- approved-domain configuration;
- on-demand trigger and no bulk sync;
- automatic versus review-only rules;
- disable/revoke behavior;
- doctor disabled/ready/degraded semantics;
- local PostgreSQL audit records and telemetry exclusions;
- future org relationships remain separate temporal observations.

- [ ] **Step 4: Add exact operator commands**

Document:

```bash
make auth-google-directory
make doctor
make sync
make review ARGS="list"
make review ARGS="show <proposal-id>"
make review ARGS="accept-directory <proposal-id> <directory-profile-id>"
```

Mark directory output as private local review output.

- [ ] **Step 5: Run secret and private-data scans**

Run:

```bash
git diff --check
git grep -nE 'AIza|ya29\\.|sk-[A-Za-z0-9]|-----BEGIN .*PRIVATE KEY-----' -- ':!go.sum'
git grep -n 'corp.example' -- .env.example README.md docs internal
```

Expected: no credential-like values; `corp.example` appears only as a safe
placeholder or synthetic fixture.

- [ ] **Step 6: Run formatting and deterministic tests**

Run:

```bash
make fmt
make test
```

Expected: PASS.

- [ ] **Step 7: Run the full race suite**

Run: `go test -race ./...`

Expected: PASS.

- [ ] **Step 8: Run Staticcheck and build**

Run:

```bash
make staticcheck
make build
```

Expected: PASS with the pinned Staticcheck release and a newly built
`bin/stacks`.

- [ ] **Step 9: Verify migrations and PostgreSQL integration**

Run:

```bash
make db-up
make db-migrate
make db-status
STACKS_TEST_DATABASE_URL="$STACKS_TEST_DATABASE_URL" \
STACKS_TEST_MIGRATION_DATABASE_URL="$STACKS_TEST_MIGRATION_DATABASE_URL" \
make test-integration
```

Expected: migration `00011` applied, no pending migrations, and every
PostgreSQL-gated storage/doctor test passes.

- [ ] **Step 10: Run doctor without invoking providers**

Run: `make doctor`

Expected: PostgreSQL and existing configured checks retain their current
semantics; directory reports disabled, locally ready, or degraded without
performing a person search or model invocation.

- [ ] **Step 11: Perform optional live Google directory acceptance only with explicit approval**

Before the call:

- confirm the authorized account and approved domains are intentional;
- use a safe synthetic or explicitly approved test query;
- cap the run to one query;
- do not print, copy, or commit returned identity fields.

Run the directory-specific authorization, doctor, and one bounded sync. Record
only exit status and bounded counters. If no safe query is available, report
live Google directory acceptance as unvalidated.

- [ ] **Step 12: Record validation boundaries**

The final report must distinguish:

- deterministic and PostgreSQL integration passing;
- optional personal/work Google directory lookup acceptance, if actually run;
- company IT OAuth approval and company-directory visibility still unvalidated;
- org-chart availability still unvalidated;
- Bedrock quota and company-IP model acceptance still unvalidated.

- [ ] **Step 13: Mark the spec implemented only after all required checks pass**

Change the design status from `Approved for implementation planning` to
`Implemented` only when Tasks 1-10 required steps pass. Leave it approved if
live optional acceptance is the only remaining unvalidated boundary and state
that boundary explicitly.

- [ ] **Step 14: Commit**

```bash
git add README.md .env.example Makefile docs/superpowers/specs/2026-07-24-google-directory-identity-enrichment-design.md
git commit -m "Document directory identity operation"
```
