# Stacks

Stacks builds provenance-backed temporal knowledge from personal source
documents. This proof of concept reads tabbed Gemini meeting Docs from one
Google Drive folder and analyzes one explicitly configured employee-manager
pair for changes in observable interaction patterns.

The analysis does not claim access to a manager's private beliefs or mental
state. It reports dated, transcript-backed signals such as delegation,
scrutiny, endorsement, support, and future responsibility. Every report keeps
counterevidence, uncertainty, gaps, and citations visible.

## Requirements

- Go 1.26 or newer
- Docker with Compose
- a Google installed-application OAuth client with read-only Drive and Docs
  access
- for optional directory enrichment, a separate Google installed-application
  OAuth client with read-only Workspace directory access
- one explicitly selected model provider: an OpenAI API key, an Anthropic API
  key, or AWS credentials available through the default credential chain or an
  optional shared profile
- an explicit compatible model ID; Bedrock also requires an explicit AWS region

## Configure the local environment

Copy the example and edit the copy; never commit `.env`:

```sh
cp .env.example .env
openssl rand -hex 24
openssl rand -hex 24
```

Use the generated values for `STACKS_DB_ADMIN_PASSWORD` and
`STACKS_DB_APP_PASSWORD`, and put the application password into
`STACKS_DATABASE_URL`. Set the corpus, Google, AWS, model, and pair values for
your environment. `.env` is loaded by the proof-of-concept Make targets below;
the Go process itself reads environment variables and does not parse dotenv
files.

Google's downloaded OAuth client JSON and Stacks' token JSON must live outside
the repository at the explicit paths in `STACKS_GOOGLE_OAUTH_CLIENT_FILE` and
`STACKS_GOOGLE_OAUTH_TOKEN_FILE`. `stacks auth google` uses an installed-app
loopback flow with read-only Drive and Docs scopes and writes the token with
owner-only permissions. No service-account or domain-wide-delegation flow is
implemented.

### Environment variables

| Variable | Default / example | Used for |
| --- | --- | --- |
| `STACKS_HTTP_HOST` | `127.0.0.1` | HTTP bind host |
| `STACKS_HTTP_PORT` | `8080` | HTTP bind port |
| `STACKS_READ_HEADER_TIMEOUT_SECONDS` | `5` | Maximum request-header read time |
| `STACKS_LOG_LEVEL` | `info` | Zap level: `debug`, `info`, `warn`, or `error` |
| `STACKS_OTEL_ENABLED` | `false` | Enable OTLP logs, metrics, and traces |
| `STACKS_OTEL_ENDPOINT` | `127.0.0.1:4317` | OTLP gRPC endpoint |
| `STACKS_OTEL_INSECURE` | `true` | Use plaintext OTLP transport |
| `STACKS_OTEL_METRIC_EXPORT_INTERVAL` | `10s` | Metric export interval as a Go duration |
| `STACKS_OTEL_SERVICE_NAME` | `stacks` | OpenTelemetry service name |
| `STACKS_OTEL_TRACE_SAMPLE_RATIO` | `1` | Parent-based sampling ratio from `0` to `1` |
| `STACKS_DB_PORT` | `5432` | Host port for the local Compose database |
| `STACKS_DATABASE_URL` | no default | Application PostgreSQL URL; contains the app password and must remain local |
| `STACKS_GOOGLE_FOLDER_ID` | no default | One direct-child Drive folder boundary |
| `STACKS_GOOGLE_OAUTH_CLIENT_FILE` | no default | External installed-app OAuth client JSON path |
| `STACKS_GOOGLE_OAUTH_TOKEN_FILE` | no default | External owner-only OAuth token JSON path |
| `STACKS_GOOGLE_DIRECTORY_ENABLED` | `false` | Enable optional on-demand Google Workspace directory identity enrichment |
| `STACKS_GOOGLE_DIRECTORY_OAUTH_CLIENT_FILE` | no default | External directory-only installed-app OAuth client JSON path |
| `STACKS_GOOGLE_DIRECTORY_OAUTH_TOKEN_FILE` | no default | External owner-only directory OAuth token JSON path |
| `STACKS_GOOGLE_DIRECTORY_EMAIL_DOMAINS` | no default | Comma-separated approved work-email domains for directory lookup and automatic exact-email authority |
| `STACKS_GOOGLE_DIRECTORY_FRESHNESS` | `24h` | Reuse window for conclusive directory results |
| `STACKS_GOOGLE_DIRECTORY_RETRY_AFTER` | `15m` | Retry-after window for transient directory outcomes |
| `STACKS_GOOGLE_DIRECTORY_MAX_ATTEMPTS` | `3` | Bounded directory lookup attempts, from `1` to `3` |
| `STACKS_TRANSCRIPT_TITLES` | no default | Comma-separated exact transcript titles after case/whitespace normalization |
| `STACKS_NOTES_TITLES` | no default | Comma-separated exact Gemini-notes titles after normalization |
| `STACKS_DATA_MODE` | no default | Explicit disclosure mode: `personal` or `restricted` |
| `STACKS_MODEL_PROVIDER` | no default | Explicit provider: `bedrock`, `openai`, or `anthropic` |
| `STACKS_MODEL_ID` | no default | Provider model or inference-profile ID; no model is guessed |
| `STACKS_MODEL_MAX_OUTPUT_TOKENS` | no default | Required positive output-token limit for `sync` and `analyze` |
| `STACKS_MODEL_MAX_ATTEMPTS` | `5` | Positive retry-attempt bound, at most `5` |
| `OPENAI_API_KEY` | no default | Personal-mode OpenAI credential; keep only in the ignored `.env` |
| `ANTHROPIC_API_KEY` | no default | Personal-mode Anthropic credential; keep only in the ignored `.env` |
| `STACKS_AWS_PROFILE` | no default | Optional shared AWS profile; all AWS commands use the default credential chain when unset |
| `STACKS_AWS_REGION` | no default | Bedrock-only control-plane and runtime region |
| `STACKS_INGEST_LEASE_DURATION` | `5m` | Positive extraction-claim duration, bounded to at most `1h` |
| `STACKS_INGEST_ATTEMPT_TIMEOUT` | `4m` | Per-document attempt deadline; must leave at least `5s` before the extraction lease expires |
| `STACKS_EXTRACTION_PROMPT_VERSION` | `extract-v2` | Versioned extraction prompt; v2 keeps model-proposed email non-authoritative |
| `STACKS_ANALYSIS_PROMPT_VERSION` | `analyze-v1` | Versioned pair-analysis prompt |
| `STACKS_EMPLOYEE_ENTITY_ID` | no default | Accepted employee entity used by `analyze` |
| `STACKS_MANAGER_ENTITY_ID` | no default | Accepted manager entity used by `analyze` |

`STACKS_DB_ADMIN_PASSWORD` and `STACKS_DB_APP_PASSWORD` are local secrets used
by Compose and migrations, so they intentionally have no example values.
`STACKS_TEST_DATABASE_URL` is the credential-bearing application-role input
used by repository integration tests. `STACKS_TEST_MIGRATION_DATABASE_URL` is
the schema-capable admin-role input used only by isolated forward-migration
upgrade tests. Both are used only by the integration-test target.

## Optional Google Workspace directory identity enrichment

Stacks can enrich unresolved people with on-demand calls to the Google People
API `people.searchDirectoryPeople` method. Directory access uses exactly the
`https://www.googleapis.com/auth/directory.readonly` scope. It has a separate
installed-application OAuth client and owner-only token file from Drive/Docs;
authorizing directory access never changes or broadens the Drive token. Stacks
does not implement service-account impersonation or domain-wide delegation.

The People API request reads only `metadata`, `names`, and `emailAddresses`.
Stacks stores the provider-scoped person identifier and directory source type,
display name, primary and alternate normalized work emails, available source
observation time, and Stacks' recorded time. It deliberately does not request
organization, title, department, manager or reporting-line, membership, phone,
postal address, photo, biography, or birthday fields.

Directory enrichment is disabled by default. When enabled, both external OAuth
paths and at least one approved domain in
`STACKS_GOOGLE_DIRECTORY_EMAIL_DOMAINS` are required. A lookup occurs only for
a source-grounded person mention that remains unresolved after accepted aliases
are checked. There is no startup enumeration, background crawl, bulk directory
synchronization, or organization-chart import.

Only a unique exact approved-domain work-email match from a Google domain
profile can earn automatic authority, and only when the email is
deterministically source-bound or explicitly supplied by the reviewer. A
citation-verified model proposal, a name-only result, ambiguity, a conflict, or
any non-profile result remains review-only. Provider ordering and confidence
never establish identity. An accepted reviewer link and the aliases explicitly
authorized by that decision remain effective for later exact resolution until
an append-only correction supersedes the decision.

Directory data is additive and optional. Disabled, denied, revoked, throttled,
or unavailable directory access leaves source preservation, ingestion, and
analysis usable; unresolved mentions can be retried later. To disable lookup,
set `STACKS_GOOGLE_DIRECTORY_ENABLED=false`. IT can revoke the separate
directory token or OAuth grant without revoking or rewriting Drive access.

Doctor reports directory readiness independently:

- disabled is an expected configuration state; authorization is not checked;
- locally ready means the separate OAuth material and requested scope are
  usable, but no live person lookup has been exercised;
- missing, invalid, denied, or unavailable directory authorization is a
  warning/degraded optional state and does not make required dependencies
  unhealthy.

The directory doctor probe may refresh its OAuth token in memory, but it never
calls `searchDirectoryPeople`, persists a profile, or invokes a model. Directory
profile snapshots, lookup attempts and matches, accepted provider links,
decisions, and corrections are kept as provenance-bearing local PostgreSQL
audit records. Ordinary logs, metrics, traces, errors, and sync output exclude
names, emails, provider person identifiers, profile payloads, OAuth material,
and lookup query values; they contain only bounded outcomes, counts, timing, and
policy metadata.

Future employment, title, team, manager, and organization relationships remain
separate provenance-bearing temporal observations. They are not inferred from
or stored as timeless directory identity facts.

Configure the separate client/token paths and approved domains, then authorize
and check readiness before the first bounded sync:

```sh
make auth-google-directory
make doctor
make sync
make review ARGS="list"
make review ARGS="show <proposal-id>"
make review ARGS="accept-directory <proposal-id> <directory-profile-id>"
```

The directory-backed `review list` and `review show` output is private local
operator output and may contain bounded identity evidence needed for a
decision. Do not copy it into logs, commits, tickets, or shared reports.

## Model provider operation

`STACKS_DATA_MODE`, `STACKS_MODEL_PROVIDER`, and `STACKS_MODEL_ID` have no
defaults. Configure one provider deliberately; Stacks has no provider or model
fallback. Direct OpenAI and Anthropic operation is permitted only in `personal`
mode. `restricted` mode permits only Bedrock.

For every personal-provider run, the folder selected by
`STACKS_GOOGLE_FOLDER_ID` must be a synthetic-only disclosure boundary. Every
in-scope direct-child Google Doc and every tab must contain synthetic test
content. Do not copy company documents or other private source material into
that folder. Stacks does not create this corpus or broaden the existing
read-only Google OAuth scopes.

`make doctor` is metadata/readiness-only and never invokes a model. It checks
the database, a bounded representative Google Doc, provider credentials, and
non-invoking model metadata; for restricted Bedrock it also inspects invocation
logging. It does not prove that a paid runtime request will succeed.

The paid runtime acceptance is a separate, explicit action. Before `make sync` or
`make analyze`, obtain approval for paid provider calls, select only one
provider, restrict the configured folder to the smallest synthetic corpus
needed, and set deliberate `STACKS_MODEL_MAX_OUTPUT_TOKENS` and
`STACKS_MODEL_MAX_ATTEMPTS` bounds. `make sync` and `make analyze` may invoke the
selected model; `make doctor` does not.

### Personal OpenAI operation

Set these values in the ignored `.env`, replacing the placeholders with an
explicit compatible model and a local secret:

```dotenv
STACKS_DATA_MODE=personal
STACKS_MODEL_PROVIDER=openai
STACKS_MODEL_ID=replace-with-explicit-openai-model
STACKS_MODEL_MAX_OUTPUT_TOKENS=2048
STACKS_MODEL_MAX_ATTEMPTS=1
OPENAI_API_KEY=replace-with-local-secret
```

Run `make doctor` first. After separately approving the bounded paid runtime
acceptance, run `make sync` and then `make analyze` through the normal workflow.
Each OpenAI Responses request is stateless and sets `store: false`. That request
setting disables Responses application-state storage; it is not a contractual
Zero Data Retention agreement and does not establish an organization-level
retention guarantee.

### Personal Anthropic operation

Set these values in the ignored `.env`, again using an explicit compatible
model and a local secret:

```dotenv
STACKS_DATA_MODE=personal
STACKS_MODEL_PROVIDER=anthropic
STACKS_MODEL_ID=replace-with-explicit-anthropic-model
STACKS_MODEL_MAX_OUTPUT_TOKENS=2048
STACKS_MODEL_MAX_ATTEMPTS=1
ANTHROPIC_API_KEY=replace-with-local-secret
```

Run `make doctor` first. After separately approving the bounded paid runtime
acceptance, run `make sync` and then `make analyze`. Anthropic uses stateless
Messages requests with native JSON-schema output. This implementation does not
establish a contractual provider-retention guarantee.

### Restricted Bedrock operation

Set restricted mode, Bedrock, an explicit model or inference-profile ID, and an
explicit region. `STACKS_AWS_PROFILE` is optional; when empty, the AWS SDK uses
its default credential chain.

```dotenv
STACKS_DATA_MODE=restricted
STACKS_MODEL_PROVIDER=bedrock
STACKS_MODEL_ID=replace-with-explicit-bedrock-model-or-inference-profile
STACKS_MODEL_MAX_OUTPUT_TOKENS=2048
STACKS_MODEL_MAX_ATTEMPTS=1
STACKS_AWS_PROFILE=
STACKS_AWS_REGION=replace-with-explicit-region
```

Run `make doctor` to inspect readiness and the account-level Bedrock model
invocation-logging configuration without invoking the model or changing AWS
configuration. Restricted `sync` and `analyze` fail closed unless logging is
confirmed `disabled`. `enabled`, `unknown`, access denied, timeout, or any
inspection failure stops the command; restricted `sync` performs this check
before Google authorization or source discovery. Only after the check succeeds
and paid calls are explicitly approved should the model-invoking workflow run.

Local tests and personal-provider acceptance do not validate Bedrock runtime
quota, company Google Drive OAuth/IAM, Bedrock logging-inspection permission in
the target account, or organization approval for company-IP processing. Keep
those as separate acceptance gates.

## Database and command workflow

Start PostgreSQL and apply forward-only migrations before running corpus
commands:

```sh
make db-up
make db-migrate
make db-status
```

Then run the explicit workflow:

```sh
make auth-google
make doctor
make sync

make entities ARGS="list"
make entities ARGS="show <entity-id>"
make review ARGS="list"
make review ARGS="show <proposal-id>"
make review ARGS="accept <proposal-id> <entity-id>"
# Or: reject, create a person, or correct the current effective decision.
make review ARGS="reject <proposal-id>"
make review ARGS="create <proposal-id> --name <name> [--email <email>]"
make review ARGS="correct <effective-decision-id> <entity-id>"

make analyze
```

`stacks doctor` is read-only. It checks PostgreSQL connectivity and the complete
required set of applied migrations; Google OAuth material, configured-folder
access, and one representative all-tabs classification; selected-provider
credentials; non-invoking model metadata; and restricted Bedrock invocation
logging. Its readiness checks may load existing OAuth material and refresh an
existing token in memory, but it never starts interactive authorization,
performs a directory person search, applies migrations, syncs or persists graph
data, extracts content, invokes a model, changes configuration, or
enables/disables logging. Missing or expired Google authorization directs the
operator to `stacks auth google`.

Migration 9 grants the application role only the schema usage and table read
access needed to inspect Goose migration status. It does not grant schema
creation, migration ownership, or broader database administration rights.

Doctor requires only the database, Google folder and OAuth paths, tab-title
sets, explicit data mode, provider, model ID, and the selected provider's
credential settings. An AWS profile is optional for Bedrock. Model invocation
limits, retry settings, prompt versions, and pair IDs are not part of its
read-only preflight contract.

If AWS credential validation fails, refresh credentials in the active default
credential chain or the explicitly configured shared profile. Doctor does not
assume that a profile is configured.

Sync claims each exact source-version and extraction-configuration derivation
for `STACKS_INGEST_LEASE_DURATION`. A concurrent sync reports that document as
incomplete/busy without invoking the selected model. Failed attempts release
their claim; an interrupted process can be retried after the finite lease
expires. Changing the provider, Bedrock region, model ID, token limit, prompt
version, or extraction schema creates a new auditable extraction run while
retaining the immutable source version and earlier derivations.

This build supports exactly `extract-v2` and `analyze-v1`. Explicit older or
unknown prompt-version settings fail before Google, AWS, Bedrock, or PostgreSQL
dependencies are constructed. Update both settings to the versions above and
run `sync` again before analysis so current derivations replace retired work.

Every claimed model-and-persistence attempt is canceled no later than
`STACKS_INGEST_ATTEMPT_TIMEOUT`, with a required cleanup margin before the
claim expires. The bounded failure state releases the claim while it is still
owned, so another sync cannot begin duplicate model work merely because a long
attempt reached the lease boundary.

The admission-boundary migrations preserve every pre-fix model payload and its
provenance for audit, but exclude superseded extraction runs, mentions,
decisions and their aliases, observations, signals, and reports from current
resolution and analysis. Migration 7 retires work created before the
`extract-v2` prompt/schema, v4 extraction namespace, and v5 analysis policy.
Migration 8 also retires rows that could have paired a fetched undated title
with a stale date from an earlier Drive listing. Current sync uses the v5
extraction namespace and analysis uses policy v6. Run `sync` again to produce a
current snapshot-coherent derivation, then review pair identities again; old
proposals and corrections cannot re-admit their retired model mentions.

The first post-upgrade sync also recognizes the exact revision-inclusive digest
used by older builds, attaches the revision-free stable content identity to the
existing immutable document version, and reprocesses only because the
derivation contract changed. A Drive revision-marker change alone therefore
does not create a duplicate source version. The original revision remains
immutable provenance.

Migration 11 gives each logical source document an explicit current completed
version. A changed document switches that pointer only when its validated
extraction commits atomically; prior versions and derivations remain immutable
audit history but no longer contribute identities or signals to current review
and analysis. Re-observing an already completed version reuses its extraction
without another model call and makes that version current again.

Doctor's provider availability checks do not invoke a model and therefore
cannot prove runtime quota, throughput, compatible structured output, or
successful inference. A credential and model metadata check can pass while
`sync` or `analyze` still fails. Treat runtime behavior as not live-validated
until an explicitly authorized invocation succeeds.

## Tab and evidence rules

Discovery reads only supported Google Docs whose direct parent is the
configured folder. It does not recurse into folders or follow document links.
Docs are fetched with all tabs, including nested child tabs, and tab hierarchy
and display order are preserved.

Tab roles are deterministic. Titles are normalized for case and whitespace and
matched only against `STACKS_TRANSCRIPT_TITLES` or `STACKS_NOTES_TITLES`; the
two sets may not overlap. Each analyzed document must classify exactly one
transcript tab. Missing or multiple transcript matches are visible failures.
Gemini notes are preserved as secondary model-derived material, but a signal
cannot rely on notes alone: its citations must map exactly to transcript text.

Source-valid meeting dates use one explicit Drive title contract. A dated
document title must begin with exactly one valid bracketed ISO date followed by
a space and a non-empty description, for example `[2026-07-20] Weekly review`.
The date is stored as the meeting date at UTC midnight. Titles with no leading
marker, malformed dates, or multiple bracketed ISO dates remain temporally
unknown. Drive creation or modification times, model output, deadlines, and
other dates in transcript or notes content are never substituted for the
meeting date.

Drive list and fetch metadata are treated as snapshots rather than freely
interchangeable fields. When the fetched document has a title, its title and
title-derived meeting time are used together, including an unknown time after
a rename. Only a fetch response with no title falls back to both the listed
title and its listed meeting time.

## Review, corrections, and bounded conclusions

Ambiguous identity matches remain proposals. Model-proposed email is preserved
with its exact citation for audit, but it is never used for automatic resolution
or taught as an alias by accepting a proposal; grounded names resolve and are
reviewed independently. Only an email explicitly supplied to `review create`
becomes a decision-owned accepted email alias. `review list` prints the highest
ranked guess, confidence, alternative count, bounded reason, and bounded cited
transcript context for local inspection. A ranked guess and confidence do not
become graph truth until accepted. `review accept`, `review reject`, and `review
create` append decisions and decision-owned alias assertions. `review correct`
appends a replacement that supersedes the prior effective decision without
deleting it or its audit history; aliases owned by the old decision stop being
authoritative. A subsequent analysis uses the corrected identity while earlier
decisions, analysis inputs, and provenance remain available for audit.

`analyze` can return only:

- `insufficient evidence`
- `no material directional change detected`
- `mixed or conflicting signals`
- `possible declining-confidence signal`

The last conclusion is a cautious hypothesis about observable signals, never a
fact that a manager has lost confidence. Two dated meetings and specific
earlier/later evidence are structural admission requirements, not proof of
hidden state. Confidence describes extraction uncertainty and never selects
truth or erases conflict.

## Privacy and model disclosure

Sync and analysis send transcript material to the selected model provider.
Personal OpenAI and Anthropic runs are therefore limited to the synthetic-only
folder described above. OpenAI requests set `store: false`; Anthropic requests
use stateless Messages. Neither behavior is evidence of a contractual retention
guarantee.

Stacks does not enable Bedrock model invocation logging, but an AWS organization
or account can configure it externally. When enabled, full model inputs and
outputs may be captured in CloudWatch Logs or S3. Doctor reports `disabled`,
`enabled`, or `unknown`; `unknown`, including an AccessDenied inspection result,
must never be treated as safe for restricted operation.

Operational logs, metrics, and traces exclude transcript and notes text,
prompts, raw model output, names, emails, Drive titles and URLs, credentials,
and OAuth tokens. Bedrock telemetry records only bounded outcomes, configured
model and prompt versions, attempts, token counts, and wall/provider latency.
The local review and analysis commands deliberately print private evidence to
their explicit terminal output for operator inspection.

## Service and observability

`make run` starts the health service on `127.0.0.1:8080` by default:

```sh
curl http://127.0.0.1:8080/healthz
```

The optional local observability stack runs an OpenTelemetry Collector,
Prometheus, Tempo, Loki, and Grafana:

```sh
make obs-up
STACKS_OTEL_ENABLED=true make run
```

Grafana is available at `http://127.0.0.1:3000`. `make obs-down` preserves its
named volumes.

## Verification and live acceptance

The deterministic repository checks are:

```sh
make fmt
make test
make staticcheck
make build
git diff --check
```

With the local database configured and running, also run:

```sh
make db-up
make db-migrate
set -a; . ./.env; set +a
make test-integration
```

Live validation is separate from those checks. Personal OpenAI and Anthropic
acceptance must be run separately, with explicit paid-call approval and only
the bounded synthetic personal corpus. Each run requires doctor to pass;
syncing at least two dated tabbed Gemini Docs; a repeated unchanged sync with no
new versions or model work; resolving the configured pair; inspecting every
analysis citation and counterevidence result; correcting one identity; and
confirming a new analysis uses the correction without erasing old provenance.
Do not copy names, emails, Drive URLs, transcript text, prompts, or model output
into commits or reports. If credentials, permissions, model access, or quota
block any step, report the implementation as test-complete but not
live-validated.

Passing personal-provider acceptance still does not validate Bedrock runtime
quota or inference, company Google Drive OAuth/IAM, Bedrock logging-inspection
permissions in the company account, or approval to process company IP. Report
those gates as unvalidated until each is tested in its intended environment.

`make db-down` and `make obs-down` stop local services without deleting named
volumes. Removing database volumes is deliberately outside the normal workflow.
