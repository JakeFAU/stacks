# Manager Confidence Signal PoC

**Date:** 2026-07-21  
**Status:** Approved design  
**Scope:** One Google Drive folder, people-first entity resolution, one configured employee-manager pair, local CLI, and Amazon Bedrock extraction and analysis

## Purpose

Build the smallest useful temporal-graph workflow for a real corpus of Gemini meeting documents. The proof of concept detects changes in observable manager-employee interaction patterns and surfaces a cautious, evidence-backed hypothesis before those changes are necessarily explicit.

The product must not claim access to a manager's private mental state. It may report a `possible declining-confidence signal` when dated transcript evidence shows a meaningful change in observable patterns. Every report must also surface contrary evidence, uncertainty, and gaps.

This is not a generic RAG system. The first useful outcome is a longitudinal relationship analysis whose identity decisions, temporal claims, and citations are durable and auditable.

## Product boundaries

The proof of concept:

- reads Google Docs directly inside one configured Google Drive folder;
- treats documents outside that folder as out of scope, even when linked from an in-scope document;
- resolves people first while keeping the entity and resolution records usable for later concept resolution;
- analyzes one explicitly configured employee-manager relationship;
- uses a CLI for ingestion, entity review, and analysis;
- invokes the organization's approved Amazon Bedrock boundary for model extraction and synthesis;
- stores immutable evidence, identity decisions, temporal observations, and analysis provenance in PostgreSQL;
- produces cited, bounded conclusions rather than assertions about private mental state.

The proof of concept does not add embeddings, vector retrieval, a graph database, a web interface, Drive-wide search, automatic organization-chart discovery, generic graph traversal, or a broad ontology. Those capabilities must be earned by exercised product needs.

## Success criteria

A useful report answers whether observable interaction patterns between the configured manager and employee appear to be changing, including:

- changes in delegation or autonomy;
- changes in scrutiny or correction;
- explicit endorsement or trust;
- support or advocacy;
- assignment of future responsibility;
- evidence that contradicts a negative interpretation;
- exact meeting dates and transcript citations;
- visible temporal or identity uncertainty.

Allowed report conclusions are:

- `insufficient evidence`;
- `no material directional change detected`;
- `mixed or conflicting signals`;
- `possible declining-confidence signal`.

The system must never promote the last conclusion to a factual statement that the manager has lost confidence.

## Architecture

The local pipeline is:

```text
configured Drive folder
  -> immutable document and tab versions
  -> tab-specific evidence spans
  -> Bedrock proposals for people, attributed statements, and interaction signals
  -> deterministic validation and identity resolution
  -> CLI review inbox for ambiguous matches
  -> temporal analysis of the configured employee-manager pair
  -> cited CLI report
```

### Package responsibilities

- `internal/source/drive` lists direct folder children, reads Google Docs with all tabs, and returns provider-shaped source records. Google SDK types do not cross this boundary.
- `internal/ingest` hashes versions, persists immutable evidence, owns idempotent processing state, and coordinates extraction without containing provider-specific policy.
- `internal/extract` invokes a narrow model interface, validates structured proposals, and maps accepted output into domain inputs.
- `internal/entity` owns kind-neutral entities, aliases, mentions, match proposals, resolution policy, and append-only review decisions. The first resolver policy uses person-specific evidence.
- `internal/analysis` owns the finite interaction-signal vocabulary, chronological comparison, counterevidence, insufficiency rules, and report construction for the configured pair.
- `internal/storage` contains explicit PostgreSQL repositories and transaction boundaries.
- `internal/bedrock` implements the model boundary with the Bedrock Converse API.
- `cmd/stacks` wires dependencies and exposes commands. It contains no ingestion, resolution, or analysis policy.

PostgreSQL is the durable system of record. The existing `internal/knowledge` temporal and provenance contracts remain the basis for evidence-backed observations. The proof of concept may extend those contracts only where an implemented behavior requires it.

### Path to a richer graph

The analysis command is pair-specific, but persisted records are not. People are ordinary canonical entities. The manager relationship is a time-bounded relationship rather than special fields on a user record. Mentions, proposals, decisions, observations, and evidence links are reusable for other entity kinds.

Concepts later add a new entity kind and a different candidate-generation and matching policy. Richer graph traversal later operates on the same entity, observation, relationship, provenance, valid-time, and recorded-time records. It must not require rewriting proof-of-concept history.

## Google Drive and tabbed document ingestion

### Folder boundary

Drive discovery lists only supported Google Docs whose direct parent is the configured folder ID. It does not recurse into child folders or follow links to out-of-scope documents. The required authorization scope is read-only.

### Tab-aware retrieval

Gemini meeting artifacts are tabbed Google Docs. Ingestion must call `documents.get` with `includeTabsContent=true`. It must recursively traverse every top-level and child tab instead of reading the legacy first-tab `Document.body` representation.

Each tab version preserves:

- immutable tab ID;
- user-visible title;
- parent tab ID and tab path;
- display order;
- extracted text;
- content digest;
- its containing document version.

The document-version digest includes the ordered tab structure and each tab's content digest. A change to one tab creates a new immutable document version while leaving all prior versions available.

### Tab roles and epistemic status

Every tab is classified deterministically as `transcript`, `gemini-notes`, or `other`. Initial live setup inspects real tab IDs and titles and configures explicit normalized title sets for transcript and notes roles. The implementation must not guess based on tab position or model output. A title present in both configured sets is invalid configuration.

Exactly one transcript tab is required for analysis. A missing or ambiguous transcript classification is a visible per-document failure. Other tabs remain preserved but are not silently merged into the transcript.

The transcript is primary evidence for speaker attribution and interaction signals. Gemini notes are model-derived secondary material. They may suggest claims or help locate evidence, but no manager-confidence signal may rely on a notes tab alone. A signal must cite supporting transcript text.

### Source time

Meeting time comes from explicit source metadata or content when available. If it cannot be established, valid time remains unknown. Ingestion time is recorded separately and never substituted for missing source time.

## Durable records

### Documents and evidence

- **Source document:** provider, Drive document ID, title, locator, and source metadata.
- **Document version:** immutable document digest, Drive modification metadata, optional source meeting time, and recorded time.
- **Tab version:** immutable tab identity, hierarchy, role, text, and digest within a document version.
- **Evidence span:** tab version, stable start and end offsets, exact local text, and a tab-specific Drive locator.

Transcript text and evidence passages are private local data. They must not appear in logs, metrics, traces, panic output, or synthetic test fixtures copied from the live corpus.

### Entity resolution

- **Entity:** stable ID, kind, display name, and recorded time.
- **Alias:** normalized name or email associated with an entity through an accepted decision.
- **Mention:** surface form, mention role such as speaker or reference, and evidence span.
- **Resolution proposal:** one mention, ranked candidate entities, confidence, bounded reasons, derivation, and status.
- **Resolution decision:** append-only acceptance, rejection, or new-entity creation with recorded time and an optional superseded-decision reference.

An email equal to an accepted entity email, or a normalized alias previously accepted for exactly one entity, may resolve automatically. All other plausible matches remain proposals. The highest-ranked candidate is shown as a guess but does not become graph truth until accepted.

Rejecting or correcting a resolution never deletes the original proposal or prior decision. A correction appends a decision that explicitly supersedes the prior effective decision. At most one non-superseded decision is effective for a proposal, enforced transactionally. Later processing uses that effective decision while preserving the audit trail.

### Observations and interaction signals

An observation retains subject and object entity references, predicate, valid time, recorded time, evidence spans, derivation, epistemic status, and optional confidence.

An interaction signal is an inferred observation using a small versioned category set:

- delegation or autonomy;
- scrutiny or correction;
- endorsement or trust;
- support or advocacy;
- future responsibility.

Each signal records its direction, rationale, supporting transcript spans, optional contradicting spans, extraction model, prompt version, and confidence. Confidence describes extraction uncertainty; it is not a truth threshold or a conflict tie-breaker.

Signal direction is one of `strengthening`, `weakening`, `mixed`, or `unclear`. Before model synthesis, deterministic policy returns `insufficient evidence` unless eligible transcript-backed signals cover at least two distinct meetings with known valid time. Bedrock may propose only one of the four allowed report conclusions. A `possible declining-confidence signal` is accepted only when it cites at least one later weakening signal and at least one earlier comparison signal from different dated meetings. Otherwise validation downgrades the result to `insufficient evidence` or `mixed or conflicting signals`, as appropriate. These are structural admission rules, not proof that the hypothesis is true.

### Analysis runs

An analysis run records:

- the employee and manager entity IDs;
- every input document and tab version;
- every input observation and signal ID;
- analysis prompt and policy versions;
- Bedrock region and model or inference profile ID;
- output-token limit;
- generated hypothesis and report state;
- recorded time and completion state.

The stable identity of a completed analysis is derived from its pair, ordered inputs, and analysis versions. Re-running identical inputs does not create duplicate graph records or a semantically duplicate analysis.

## Processing flow

1. `stacks sync` lists direct Google Doc children of the configured folder.
2. Each document is fetched through the Docs API with all tab contents populated.
3. The tab tree is flattened in UI order while retaining parent relationships.
4. Deterministic tab classification identifies the transcript and notes independently.
5. An unchanged document-version digest ends processing without model work.
6. A changed digest creates an immutable document and tab version in a transaction.
7. Bedrock proposes mentions, attributed statements, and interaction signals with exact source-span references.
8. Deterministic validation rejects malformed values, unsupported categories, invalid dates, unknown entity references, and citations that do not map exactly to submitted tab text.
9. Strong accepted identifiers resolve automatically. Ambiguous matches enter the review inbox with a ranked guess.
10. Review decisions append audit history and update the effective resolution.
11. `stacks analyze` refuses to run until the configured pair has accepted identities.
12. Analysis orders eligible signals by valid time, preserves unknown time separately, considers counterevidence, and produces one bounded conclusion.
13. The CLI renders exact dates, signal uncertainty, citations to document tabs, counterevidence, and gaps.

Every retryable step uses stable input identities. A document version is not marked complete until required evidence and validated proposals are durable.

## Amazon Bedrock boundary

The proof of concept uses the Bedrock Runtime Converse API rather than provider-specific `InvokeModel` payloads. This keeps Anthropic, OpenAI, or another account-approved Bedrock model selectable through configuration without leaking provider SDK types into the domain.

Required runtime configuration lives in `internal/config` and uses `STACKS_*` environment variables. It includes AWS profile or default credential-chain behavior, AWS region, model or inference-profile ID, explicit maximum output tokens, bounded retry settings, and extraction and analysis prompt versions. There is no guessed model default when account access has not been verified.

Prompts request structured output. Model responses remain untrusted until schema and citation validation succeed. Each invocation records token counts, latency, bounded outcome, model ID, and prompt version without recording prompt or transcript contents.

Retryable throttling, timeout, service-unavailable, and internal-service failures receive bounded adaptive retries. Authentication, authorization, resource-not-found, invalid-configuration, and schema-validation failures do not receive blind retries.

Stacks does not enable Bedrock model invocation logging. An AWS organization may enable logging externally, which can capture full prompts and responses in CloudWatch Logs or S3. `stacks doctor` reports whether invocation logging is enabled when permissions allow inspection and warns when the state cannot be inspected.

## CLI contract

### `stacks auth google`

Runs the installed-application OAuth flow with a loopback callback and read-only Drive and Docs scopes. OAuth client configuration is supplied by an explicit path outside the repository. The refresh token is stored in a configured local file outside the repository with owner-only permissions. The command never prints tokens. There is no service-account or domain-wide-delegation path in the proof of concept.

### `stacks doctor`

Checks:

- PostgreSQL connectivity and migrations;
- Google OAuth configuration;
- access to the configured Drive folder;
- representative document tab discovery and transcript classification;
- AWS credential validity;
- Bedrock region and configured model availability;
- Bedrock invocation-logging status when inspectable.

It performs no authentication flow, extraction, or graph persistence. Missing or expired Google authorization directs the user to `stacks auth google`.

### `stacks sync`

Prints one bounded outcome per document: unchanged, completed, incomplete, or failed. It reports counts and operational IDs without printing private text or identities. A failure in one document does not roll back successful independent documents.

### `stacks entities list|show`

Displays canonical people, accepted aliases, mention counts, and cited local evidence required for review.

### `stacks review list|show`

Displays pending proposals with the highest-ranked guess, alternatives, confidence, bounded reasons, and transcript context.

### `stacks review accept|reject|create`

Accepts a proposed candidate, rejects the proposal, or creates and accepts a new person. Commands require an explicit proposal ID and fail on already superseded review state.

### `stacks analyze`

Uses the configured employee-manager pair. It analyzes only accepted identities and validated transcript-backed signals, then prints the bounded conclusion, chronological evidence, counterevidence, uncertainty, and tab-specific Drive citations.

## Error handling and recovery

- Drive or AWS authentication failures stop before dependent model work and return actionable remediation.
- Invalid configuration fails before a command starts durable work.
- One malformed document cannot erase or invalidate successful independent documents.
- Processing state and retry history are explicit for each immutable version.
- Interrupted processing resumes from durable state without duplicating evidence or proposals.
- Invalid model output is stored only as bounded diagnostic metadata when needed; raw output containing private transcript material is not logged.
- Analysis cannot use pending identity proposals as facts.
- Missing source time remains visible temporal uncertainty.
- Report generation must return `insufficient evidence` rather than invent a trend when eligible signals are too sparse.

## Privacy and observability

Operational telemetry may include stable internal IDs, content hashes, counts, durations, model ID, prompt version, token usage, and bounded outcomes.

Telemetry must never include:

- transcript or notes text;
- prompts or raw model output;
- person names or email addresses;
- Drive document titles or URLs;
- credentials, authorization headers, or OAuth tokens;
- user-controlled or otherwise unbounded metric labels.

Meaningful Drive, ingestion, storage, Bedrock, and command boundaries may own spans. Successful manually owned spans use `observability.FinishSpan`. Resolution and analysis choices use `observability.DecisionRecorder` events and distributions rather than child-span forests.

## Testing strategy

All automated fixtures use synthetic organizations, people, and meeting text.

### Domain tests

- entity kinds remain open to later concept entities;
- accepted aliases resolve deterministically;
- ambiguous mentions remain pending with a ranked guess;
- review accept, reject, and create decisions retain audit history;
- pending guesses cannot become resolved graph references;
- confidence does not select truth or erase conflict;
- temporal observations preserve valid time separately from recorded time;
- sparse, conflicting, or temporally unknown signals yield bounded conclusions.

### Drive and ingestion contract tests

- only direct supported Docs in the configured folder are discovered;
- links and files outside the folder are excluded;
- `includeTabsContent=true` is required by the source request contract;
- a document whose first tab is notes and second tab is transcript ingests and cites the transcript;
- nested child tabs are traversed in deterministic UI order;
- duplicate-looking transcript titles fail as ambiguous;
- missing transcript tabs fail visibly;
- notes and transcript text remain separate evidence sources;
- a notes-only proposed signal is rejected;
- unchanged content performs no model work;
- one tab change creates a new immutable document version;
- repeated processing creates no duplicate evidence, mentions, proposals, or observations;
- unknown meeting time remains unknown.

### Bedrock contract tests

- extraction and analysis requests set an explicit maximum output-token limit;
- structured responses validate before conversion to domain inputs;
- exact submitted transcript spans validate as citations;
- invented or mismatched citations are rejected;
- retryable and non-retryable service errors follow distinct policies;
- logs and telemetry exclude prompts and private contents.

### Storage and CLI integration tests

PostgreSQL integration tests exercise migrations, unique constraints, append-only decision history, transactions, resume behavior, and idempotency. CLI tests run the complete workflow with fake Drive and Bedrock boundaries and deterministic synthetic transcripts.

## Live acceptance

The proof of concept is live-validated only when all of the following succeed:

1. `stacks doctor` verifies the local database, configured Drive folder, tab classification, AWS credentials, and Bedrock model access.
2. `stacks sync` processes at least two distinct or changed Gemini meeting documents.
3. The employee-manager pair is configured and resolved, including at least one review decision when the corpus produces ambiguity.
4. A repeated sync proves no duplicate versions, evidence, proposals, observations, or model calls.
5. `stacks analyze` produces a bounded report whose every signal links to an exact transcript tab and passage.
6. At least one report includes and labels counterevidence, or explicitly states that none was found.
7. Correcting one identity decision changes subsequent analysis without erasing earlier decisions or analysis provenance.
8. `make fmt`, `make test`, and `make staticcheck` pass.

If credentials or account permissions prevent the live checks, the work may be reported as implemented and test-complete, but not live-validated or shipped.

## Implementation sequencing constraints

Implementation must proceed as a vertical slice under test-driven development:

1. tab-aware Drive source contract and immutable evidence;
2. person entities, mentions, proposals, and CLI review;
3. validated Bedrock extraction boundary;
4. temporal interaction signals and pair analysis;
5. end-to-end CLI workflow and live validation.

Each slice must have a failing behavior test before production code. Schema changes are forward-only. The implementation must not add speculative graph traversal, embeddings, a web UI, or concept extraction while delivering this proof of concept.
