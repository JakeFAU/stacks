# Google Directory Identity Enrichment

**Date:** 2026-07-24  
**Status:** Approved for implementation planning
**Scope:** Optional, on-demand Google Workspace directory identity enrichment for unresolved people in the manager-confidence workflow

**Acceptance boundary:** This status intentionally remains approved rather than
implemented because Task 10 does not have approval to perform a live Google
directory person lookup. Optional personal/work lookup acceptance, company IT
OAuth approval and directory visibility, organization-chart availability,
Bedrock quota, and company-IP model acceptance remain unvalidated.

## Context

Stacks currently resolves people from source-grounded mentions, accepted email
aliases, and reviewer-approved name aliases. This conservative policy prevents a
model-proposed identity from becoming graph truth, but it leaves a useful source
of identity evidence unused: a Google Workspace directory visible to an
authorized work account.

Meeting hosts and some attendees may appear with work email addresses. A unique
exact directory match for one of those addresses can safely anchor a canonical
person. A name-only directory result is useful as a review candidate, but it is
not strong enough to establish identity without a reviewer.

The integration must remain suitable for later company adoption. It must not
reuse or broaden the existing Google Drive authorization, assume administrator
access, require directory availability for ingestion, or collapse a directory
profile into timeless graph truth.

## Goals

- Enrich unresolved person mentions through on-demand directory lookups.
- Automatically resolve only a unique exact approved work-email match whose
  binding to the source mention is deterministic or reviewer-supplied.
- Keep every name-only match review-only until a reviewer accepts it.
- Treat a reviewer-approved name alias as authoritative for later exact-name
  resolution, unless more than one entity owns the same accepted alias.
- Preserve directory observations, identity links, review decisions, and
  corrections with provenance and recorded time.
- Keep directory availability additive: unavailable directory access must not
  fail source preservation, extraction, or ingestion completion.
- Use a narrow provider-neutral domain boundary with a Google People API adapter
  at the edge.
- Give IT an explicit least-privilege authorization, data-access, operational,
  and audit contract.
- Preserve a path for future temporal employment or reporting relationships
  without adding those relationships to this identity-only slice.

## Non-goals

- Bulk synchronization or enumeration of the company directory.
- Automatic organization-chart, manager, title, department, or reporting-line
  ingestion.
- Using directory names alone as automatic identity evidence.
- Treating provider ranking, model confidence, or name similarity as truth.
- Reusing the Drive OAuth token or adding directory scope to Drive
  authorization.
- Domain-wide delegation, service-account impersonation, or an Admin SDK
  requirement.
- Blocking startup, document ingestion, model extraction, or analysis because
  the optional directory is disabled or unavailable.
- A generic plugin registry, dynamic provider loading, or a broad identity
  platform.
- Sending private documents, transcript passages, prompts, or model output to a
  directory provider.

## Chosen approach

Add a small directory lookup contract near the identity-resolution consumer and
implement it with the Google People API
[`people.searchDirectoryPeople`](https://developers.google.com/people/api/rest/v1/people/searchDirectoryPeople).
That method searches the authenticated user's visible domain directory with the
read-only `https://www.googleapis.com/auth/directory.readonly` scope.

The initial adapter requests only the identity fields needed by this design:

- a provider-scoped person identifier;
- display name;
- primary work email when distinguishable;
- alternate work emails;
- Google directory source type.

The adapter searches Google Workspace domain profiles. Domain shared contacts
may be returned as review candidates if later enabled, but they must not
automatically resolve identities. The People API distinguishes
[`DIRECTORY_SOURCE_TYPE_DOMAIN_PROFILE`](https://developers.google.com/people/api/rest/v1/DirectorySourceType)
from `DIRECTORY_SOURCE_TYPE_DOMAIN_CONTACT`.

The existing Admin SDK Directory API is not the default. Its user endpoints use
administrator-oriented scopes and expose a broader contract than this feature
needs. A future company implementation may add an Admin SDK adapter behind the
same consumer-owned boundary if IT explicitly chooses that authorization model.

## Architecture

The enriched resolution flow is:

```text
source-grounded person mention
  -> existing accepted-alias resolution
  -> unresolved mention eligibility check
  -> durable recent-result lookup
  -> optional on-demand DirectoryLookup
  -> immutable directory snapshot and lookup outcome
  -> deterministic exact-email policy or review candidate
  -> append-only resolution decision
  -> accepted aliases and provider identity link
  -> existing temporal graph processing
```

### Package responsibilities

- `internal/entity` owns directory-neutral lookup inputs and results, work-email
  admission policy, candidate construction, and deterministic resolution rules.
- A focused Google edge package owns People API requests, OAuth scopes, response
  normalization, pagination bounds, and provider error classification. Google
  SDK types do not cross this boundary.
- `internal/ingest` coordinates directory enrichment after ordinary accepted
  aliases fail to resolve a mention. It continues processing when enrichment is
  disabled, unavailable, or inconclusive.
- `internal/storage` persists immutable lookup attempts, directory snapshots,
  candidate evidence, provider identity links, and decisions in explicit
  transactions.
- `internal/config` owns optional Google directory settings, approved work-email
  domains, freshness, retry bounds, and validation.
- `internal/doctor` performs bounded, read-only readiness checks and reports the
  optional directory separately from Drive and model providers.
- `cmd/stacks` constructs the optional adapter and keeps both authorization
  flows separate. It contains no matching policy.

The directory contract is deliberately smaller than the People API. It supports
an exact-email lookup and a bounded name search. It returns normalized domain
records and typed outcomes rather than provider request, response, or error
types.

## Lookup eligibility and query rules

Directory access occurs only for a person mention that remains unresolved after
the existing accepted-alias resolver runs.

Email evidence has two trust levels:

- **source-bound email:** deterministic source structure, a strict mailbox-form
  parser, or an explicit reviewer action binds the literal email to the
  mention;
- **citation-verified proposal:** deterministic validation proves that the
  model-returned email appears literally in cited evidence, but does not prove
  that the model associated it with the correct named person.

An email lookup is eligible only when the email:

- has one of those two evidence levels;
- is structurally valid after the existing email normalization rules;
- belongs to an explicitly configured approved work-email domain; and
- has not already received a fresh durable conclusive result.

A citation-verified model proposal may locate a directory-backed review
candidate, but it remains audit-only for automatic identity resolution. Model
confidence never changes that rule. Only a source-bound or reviewer-supplied
email can qualify for automatic exact-email resolution.

A name lookup is eligible only when the name is source-grounded, normalizes to a
non-empty person name, remains unresolved, and has no fresh durable conclusive
result. The adapter may use Google's prefix search, but local deterministic code
must normalize, filter, and rank returned candidates. Provider result order is
never a truth signal.

Lookups are on demand per unresolved mention. There is no startup enumeration,
background directory crawl, or attempt to mirror every employee.

## Deterministic identity policy

The resolution order is:

1. A unique currently admissible accepted email or name alias resolves as it
   does today.
2. If still unresolved, a fresh durable directory result is reused.
3. If eligible and configured, the directory adapter performs one bounded
   lookup with bounded retries.
4. The domain policy evaluates the complete bounded result set.

The outcomes are:

| Evidence | Result |
| --- | --- |
| Exactly one domain profile returns the exact normalized source-bound email, and the domain is approved | Automatically resolve |
| Exactly one domain profile returns an exact citation-verified model-proposed email without deterministic identity binding | Review candidate |
| More than one profile returns the exact email | Review required |
| One exact email conflicts with existing accepted email ownership or an existing provider identity link | Review required |
| Name-only domain profile match | Review candidate |
| Domain shared contact match | Review candidate |
| No match | Remain unresolved |
| Disabled, denied, throttled, unavailable, or bounded result limit exceeded | Remain unresolved and record the bounded outcome |

For a unique exact-email result:

- if exactly one existing entity owns the accepted email, link that entity to
  the directory identity;
- if no entity owns it, create a canonical person and accept the link
  atomically;
- use the observed directory display name as the new entity's initial
  presentation value without treating it as an accepted name alias;
- accept only the exact email alias automatically;
- do not automatically promote the source mention name or the directory display
  name to an accepted name alias.

The last rule preserves the agreed trust progression. A later name-only mention
still requires one reviewer decision. Once accepted, that normalized name is an
authoritative alias for future exact-name resolution. If two entities later
have the same currently admissible accepted name alias, the existing
exactly-one rule makes the name ambiguous again instead of guessing.

## Review lifecycle

A directory-backed review candidate shows:

- the source-grounded mention and existing cited review context;
- a bounded directory display name and masked or otherwise review-appropriate
  work-email presentation;
- whether the evidence came from a domain profile or shared contact;
- the recorded lookup time;
- existing canonical entity candidates, if any.

Accepting a directory candidate atomically:

- links the mention to an existing entity or creates a new person;
- links the provider-scoped directory identity to that entity;
- asserts only the aliases supported by the explicit review action; and
- records the reviewer decision and its derivation.

Rejecting a candidate preserves the candidate and lookup evidence. Correcting a
decision appends a replacement that supersedes the prior effective decision.
Current resolution snapshots use only the non-superseded, currently admissible
decision, while the complete history remains auditable.

A rejection is contextual evidence about that candidate and mention. It is not
a global assertion that the normalized name can never refer to that person.

## Persistence model

The implementation requires a forward-only migration after the current
migrations through the published `00011_current_document_version.sql`. Exact table and column names belong in the
implementation plan, but the durable contracts are:

### Directory lookup attempt

- stable idempotent identity derived from the mention, provider, query kind,
  policy version, and eligible input;
- mention reference rather than a second ungoverned copy of private source
  text;
- provider and query kind;
- bounded outcome;
- attempt count;
- recorded time;
- optional retry-after time;
- bounded provider error class without raw provider error text.

### Directory profile snapshot

- provider and provider-scoped subject identifier;
- provider directory source type;
- display name;
- primary and alternate normalized work emails;
- observation time and recorded time;
- stable content digest.

Snapshots are immutable. A changed provider response creates a new snapshot.
An unchanged response reuses the stable digest identity while recording the new
lookup attempt.

### Lookup match evidence

A lookup may return zero, one, or several profile snapshots. A durable join
records which bounded profiles were considered and the deterministic local
match reason. This makes ambiguity and later review reproducible without
depending on another live directory request.

### Entity directory identity link

An accepted link associates one canonical entity with one provider-scoped
directory identity through an automatic exact-email or reviewer decision. The
link retains its decision provenance and recorded time. Conflicting active
links are rejected transactionally and sent to review.

### Resolution candidates and aliases

Directory candidates may refer to an existing entity or an unaccepted directory
snapshot. Accepting a snapshot-only candidate can create the entity
transactionally.

Alias assertions must record which accepted decision authorized each alias.
Automatic exact-email resolution asserts the email only. Reviewer acceptance
may assert the source-grounded name and any explicitly approved email aliases.
Superseding the decision removes those assertions from current resolver
snapshots without deleting history.

## Temporal graph semantics

A directory profile is evidence observed at a recorded time, not a timeless
person record. Stacks preserves:

- when the provider profile was observed;
- when Stacks recorded it;
- which provider identity was linked;
- which automatic policy or human decision accepted the link.

This identity-only feature does not create employment or reporting edges.
Future title, team, employment, or manager data enters through separate
provenance-bearing observations and time-bounded relationships. Those future
relationships may use the accepted directory identity as an endpoint, but they
must not overwrite identity history or become permanent fields on an entity.

Mock and generated meeting notes continue to exercise the same entity,
observation, relationship, valid-time, recorded-time, and evidence contracts as
future company data.

## Authorization and IT boundary

Google Drive and Google directory authorization remain independent.

- `stacks auth google` continues to authorize only the existing Drive and Docs
  access.
- A distinct command, initially `stacks auth google-directory`, requests only
  `https://www.googleapis.com/auth/directory.readonly`.
- The directory uses its own OAuth client configuration path and token path.
- Token files remain outside the repository with owner-only permissions.
- Stacks never prints tokens, authorization headers, or credential contents.
- Enabling the directory does not rewrite or upgrade an existing Drive token.
- Disabling or revoking directory authorization leaves Drive ingestion usable.

The first implementation uses installed-application OAuth. It does not include
domain-wide delegation or service-account impersonation. IT may:

- approve or deny the OAuth application and scope;
- control which domain profiles are visible to the authorized user;
- restrict visible users through Google Workspace directory policy;
- revoke the token independently;
- provide a different adapter later without changing domain resolution rules.

Directory configuration is optional and disabled by default. When enabled,
configuration requires explicit OAuth paths and at least one approved work-email
domain. Runtime-tunable freshness and retry values live in `internal/config`
under `STACKS_*` environment variables. `.env.example` documents safe
placeholders only.

## Doctor and CLI behavior

`stacks doctor` adds separate privacy-safe checks for:

- directory configuration;
- directory authorization and requested scope;
- bounded directory API capability when it can be checked without a private
  source query.

Directory checks have optional dependency semantics. Disabled is an explicit
healthy state. Missing authorization, denied scope, or unavailable service is a
warning/degraded directory state and does not make PostgreSQL, Drive ingestion,
or the selected model provider unhealthy.

The capability probe must not search for a real person, print a profile, or
persist a profile. If the Google API does not offer a content-free readiness
operation, doctor reports authorization readiness separately and leaves live
identity lookup unexercised rather than enumerating directory data.

Normal `sync` output adds only bounded counts and outcomes such as directory
lookups attempted, reused, matched, ambiguous, unavailable, or pending review.
It does not print names, emails, provider person identifiers, or raw errors.

Review and entity commands may display private identity fields only where the
operator explicitly requested the local review detail needed to make a
decision. Existing private-context output boundaries continue to apply.

## Failure, retry, and resume behavior

Directory outcomes are classified as:

- `matched`;
- `no_match`;
- `ambiguous`;
- `disabled`;
- `not_configured`;
- `unauthorized`;
- `forbidden`;
- `rate_limited`;
- `unavailable`;
- `invalid_response`;
- `result_limit_exceeded`.

Exact names may change during implementation, but the bounded distinctions and
their behavior must remain.

Transient network, timeout, rate-limit, and service-unavailable failures receive
bounded retries with cancellation and provider `Retry-After` behavior respected
where available. Authorization, forbidden, invalid configuration, invalid
response, and result-limit failures do not receive blind retries.

Conclusive positive, negative, and ambiguous outcomes have a configured
freshness window. Transient failures have a shorter retry-after policy. A
resumed ingestion reuses fresh durable attempts and snapshots instead of
repeating the network call.

No directory failure rolls back already durable document versions, evidence
spans, extraction runs, or unresolved mentions. Directory enrichment is not a
required ingestion completion gate. Later syncs may enrich an already-preserved
unresolved mention when authorization or service availability returns.

Database writes for an automatic exact-email resolution are atomic: directory
snapshot, lookup evidence, entity creation or selection, provider identity
link, accepted email alias, decision, and effective proposal state either
commit together or remain retryable without partial authority.

## Privacy and observability

Directory access is a disclosure boundary because a query can contain a name or
email. It receives only the smallest eligible identity query, never document
text, transcript context, prompts, model responses, embeddings, or relationship
analysis.

Logs, metrics, traces, errors, and ordinary sync output may contain:

- provider name;
- query kind;
- bounded outcome;
- counts;
- durations;
- attempt number;
- policy version;
- low-cardinality retry or failure class.

They must not contain:

- names or email addresses;
- provider person identifiers;
- directory profile payloads;
- OAuth credentials or tokens;
- source text or review context;
- user-controlled query values as labels.

Successful directory spans are explicitly marked `OK`. Decision telemetry uses
the existing decision recorder with low-cardinality outcomes. The local
PostgreSQL records preserve the private audit evidence without exporting it to
telemetry.

## Verification strategy

Implementation follows test-driven development.

### Deterministic unit tests

- only literal citation-verified, source-bound, or reviewer-supplied valid
  approved-domain emails are eligible for any lookup;
- citation-verified model-proposed emails can create review candidates but
  never automatic identity authority;
- separately cited or merely co-occurring names and emails never become linked
  automatically;
- exactly one exact source-bound domain-profile email match auto-resolves;
- zero, duplicate, conflicting, shared-contact, external-domain, and name-only
  results never auto-resolve;
- automatic email resolution accepts no name alias;
- one reviewer acceptance makes that exact name authoritative later;
- duplicate accepted names remain ambiguous;
- corrections supersede authority without deleting history;
- provider ordering and map iteration cannot affect results;
- disabled and every bounded failure class preserve unresolved processing.

### Adapter tests

- requests use only the directory-readonly scope and required identity fields;
- prefix results are locally normalized and exact-email filtered;
- pagination and result counts are bounded;
- cancellation, timeouts, rate limits, and provider errors map to typed bounded
  outcomes;
- provider payloads and errors cannot reach logs or returned error strings.

### PostgreSQL-gated integration tests

- forward migration from the current schema through the new migration;
- upgrade preservation of existing entities, aliases, proposals, decisions,
  and admission flags;
- immutable snapshot and lookup-attempt idempotency;
- transaction rollback under injected entity/link/alias conflicts;
- automatic entity creation and exact-email assertion;
- review acceptance, rejection, correction, and effective-alias lifecycle;
- retry/resume reuse of durable conclusive results;
- later enrichment of a previously unresolved completed ingestion;
- concurrent exact-email attempts cannot create duplicate authority.

### Application and CLI tests

- directory dependencies are constructed lazily only when configured and
  eligible;
- Drive authorization and directory authorization use different credentials
  and scopes;
- directory degradation does not fail sync;
- doctor reports disabled, ready, and degraded optional states correctly;
- output remains bounded and contains no synthetic private identity values.

### Completion checks

After implementation:

```bash
make fmt
make test
go test -race ./...
make staticcheck
make build
make db-status
make test-integration
```

The PostgreSQL-gated suite runs with `STACKS_TEST_DATABASE_URL`. Live Google
directory acceptance is a separate explicit opt-in check using an authorized
test account and safe synthetic lookup inputs. It must not print or commit real
directory contents.

A passing local and synthetic suite does not claim company OAuth approval,
company-directory visibility, org-chart availability, Bedrock quota, or
company-IP acceptance. Those remain explicit Work Codex and IT handoff checks.

## Documentation and handoff

The implementation updates:

- README setup and command documentation;
- `.env.example` with safe placeholders;
- OAuth scope and token-separation documentation;
- directory fields accessed and fields deliberately not accessed;
- administrator approval and revocation guidance;
- disabled/degraded doctor semantics;
- private-data and telemetry boundaries;
- live acceptance checklist and unvalidated-company-boundary statement.

The handoff should allow company IT to answer:

1. Which Google API and OAuth scope are requested?
2. Which account performs each lookup?
3. Which directory subset can that account see?
4. Which fields are read and stored?
5. What causes a lookup?
6. Which identity decisions are automatic?
7. How are human decisions corrected and audited?
8. What happens when access is denied or revoked?
9. What data can appear in logs or telemetry?
10. How can the integration be disabled without affecting ingestion?

## Approved design summary

- Google People API behind a provider-neutral on-demand lookup boundary.
- Separate optional directory OAuth from Drive OAuth.
- Domain profiles and explicit approved work-email domains for automatic
  resolution.
- Unique exact source-bound work-email matches may auto-resolve; names never do.
- Reviewer-approved names become authoritative for future exact matches.
- Directory evidence and decisions are immutable, temporal, and
  provenance-bearing.
- Directory enrichment is additive and never an ingestion gate.
- Identity-only now; future org relationships remain separate temporal graph
  observations.
- No bulk directory synchronization, Admin SDK requirement, domain-wide
  delegation, cloud deployment, or private-data logging.
