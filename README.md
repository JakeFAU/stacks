# Stacks

Stacks builds provenance-backed temporal knowledge from personal source
documents. The current application reads tabbed Gemini meeting Docs from one
Google Drive folder and can analyze one explicitly configured employee-manager
pair for changes in observable interaction patterns.

The analysis does not claim access to a manager's private beliefs or mental
state. It reports dated, transcript-backed signals such as delegation,
scrutiny, endorsement, support, and future responsibility. Every report keeps
counterevidence, uncertainty, gaps, and citations visible.

## Public core (experimental)

The provider-neutral [`core`](./core) Go module contains the temporal evidence
primitives extracted from the application:

- `evidence`: immutable versioned sources and exact spans;
- `identity`: conservative accepted-alias resolution;
- `observation`: provenance-bearing claims with separate valid and recorded
  time; and
- `temporal`: deterministic aggregation and comparison.

PostgreSQL, model and source providers, operator configuration, and the
manager-confidence workflow remain downstream in the application during the
next extraction phases. The public core is experimental and has no independent
release yet.

The application PostgreSQL boundary stores canonical documents, evidence,
observations, identity authority, admission history, and extraction lifecycle
through the scoped core manifest. Optional Workspace directory identity
evidence uses its own independently configured directory scope.

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

## Command-line interface

`stacks --help` and nested `--help` commands describe the supported operator
syntax without constructing database or provider dependencies. Running
`stacks` and `stacks serve` are equivalent forms of the health service. Cobra
also provides shell-completion support through `stacks completion --help`; it
is an auxiliary transport command, not an application or provider command.

```text
stacks
├── serve
├── config
│   └── validate
│       ├── serve | doctor | sync | entities | review | analyze
│       ├── db-migrate | db-status | db-reset
│       └── auth
│           ├── google
│           └── google-directory
├── auth
│   ├── google
│   └── google-directory
├── doctor
├── sync
├── entities
│   ├── list
│   └── show <entity-id>
├── review
│   ├── list
│   ├── show <proposal-id>
│   ├── accept <proposal-id> <entity-id>
│   ├── accept-directory <proposal-id> <directory-profile-id> [--entity <entity-id>]
│   ├── reject <proposal-id>
│   ├── create <proposal-id> --name <name> [--email <email>]
│   └── correct <effective-decision-id> <entity-id>
├── analyze
├── db-migrate
├── db-status
└── db-reset <confirmation>
```

`analyze` runs the currently configured cited temporal analysis for the
accepted employee-manager pair; it does not select or install a provider.

## Configure the local environment

Copy the example and edit the copy; never commit `.env`:

```sh
cp .env.example .env
openssl rand -hex 24
openssl rand -hex 24
```

Use the generated values for `STACKS_DB_ADMIN_PASSWORD` and
`STACKS_DB_APP_PASSWORD`, and put the application password into
`STACKS_DATABASE_URL` and the administrator password into
`STACKS_MIGRATION_DATABASE_URL`. Set the corpus, Google, AWS, model, and pair
values for your environment. `.env` is loaded by the application Make targets
below; the Go process itself reads environment variables and does not parse
dotenv files.

Google's downloaded OAuth client JSON and Stacks' token JSON must live outside
the repository at the explicit paths in `STACKS_GOOGLE_OAUTH_CLIENT_FILE` and
`STACKS_GOOGLE_OAUTH_TOKEN_FILE`. `stacks auth google` uses an installed-app
loopback flow with read-only Drive and Docs scopes and writes the token with
owner-only permissions. No service-account or domain-wide-delegation flow is
implemented.

### Explicit configuration files

Non-secret settings may be stored in one explicitly selected YAML or JSON file.
Stacks accepts `.yaml`, `.yml`, and `.json` extensions, case-insensitively. It
does not discover configuration in the working directory, home directory, or
XDG locations: pass the path with the persistent root flag, before or after a
subcommand:

```sh
go run ./cmd/stacks --config config.example.yaml config validate serve
go run ./cmd/stacks config validate serve --config config.example.json
```

The precedence order, highest first, is an explicitly changed operational
command flag, a non-empty explicitly bound environment variable, the selected
file, then a named Go default. `--config` selects a source rather than
overriding an operational setting; the current CLI has no operational setting
flag. Empty environment variables are absent. In files, an empty string uses
the default for a defaulted string and remains unset for optional or required
strings. Surrounding whitespace is preserved, and an empty array remains an
explicit value.

Files are decoded strictly. The root must be one object, and unknown or
duplicate keys, `null`, wrong scalar/object/array shapes, non-string list
members, additional documents or values, and unrepresentable typed values are
errors. YAML aliases, anchors, and merge keys are also rejected. Stacks reads
the explicitly selected file once and does not parse dotenv files, search for
alternatives, use remote configuration, watch or reload files, or provide
`config explain`.

The checked-in [`config.example.yaml`](./config.example.yaml) and
[`config.example.json`](./config.example.json) contain the complete equivalent
file schema with synthetic values:

| File key | Type | Optional environment override |
| --- | --- | --- |
| `http.host` | string | `STACKS_HTTP_HOST` |
| `http.port` | integer | `STACKS_HTTP_PORT` |
| `http.read_header_timeout_seconds` | integer | `STACKS_READ_HEADER_TIMEOUT_SECONDS` |
| `log.level` | string | `STACKS_LOG_LEVEL` |
| `telemetry.enabled` | boolean | `STACKS_OTEL_ENABLED` |
| `telemetry.endpoint` | string | `STACKS_OTEL_ENDPOINT` |
| `telemetry.insecure` | boolean | `STACKS_OTEL_INSECURE` |
| `telemetry.metric_export_interval` | duration string | `STACKS_OTEL_METRIC_EXPORT_INTERVAL` |
| `telemetry.service_name` | string | `STACKS_OTEL_SERVICE_NAME` |
| `telemetry.trace_sample_ratio` | number | `STACKS_OTEL_TRACE_SAMPLE_RATIO` |
| `database.scopes` | array of strings | `STACKS_DATABASE_SCOPES` |
| `database.application_role` | string | `STACKS_DATABASE_APP_ROLE` |
| `google.folder_id` | string | `STACKS_GOOGLE_FOLDER_ID` |
| `google.oauth_client_file` | string path | `STACKS_GOOGLE_OAUTH_CLIENT_FILE` |
| `google.oauth_token_file` | string path | `STACKS_GOOGLE_OAUTH_TOKEN_FILE` |
| `google.transcript_titles` | array of strings | `STACKS_TRANSCRIPT_TITLES` |
| `google.notes_titles` | array of strings | `STACKS_NOTES_TITLES` |
| `directory.enabled` | boolean | `STACKS_GOOGLE_DIRECTORY_ENABLED` |
| `directory.oauth_client_file` | string path | `STACKS_GOOGLE_DIRECTORY_OAUTH_CLIENT_FILE` |
| `directory.oauth_token_file` | string path | `STACKS_GOOGLE_DIRECTORY_OAUTH_TOKEN_FILE` |
| `directory.email_domains` | array of strings | `STACKS_GOOGLE_DIRECTORY_EMAIL_DOMAINS` |
| `directory.freshness` | duration string | `STACKS_GOOGLE_DIRECTORY_FRESHNESS` |
| `directory.retry_after` | duration string | `STACKS_GOOGLE_DIRECTORY_RETRY_AFTER` |
| `directory.max_attempts` | integer | `STACKS_GOOGLE_DIRECTORY_MAX_ATTEMPTS` |
| `model.data_mode` | string | `STACKS_DATA_MODE` |
| `model.provider` | string | `STACKS_MODEL_PROVIDER` |
| `model.id` | string | `STACKS_MODEL_ID` |
| `model.max_output_tokens` | integer | `STACKS_MODEL_MAX_OUTPUT_TOKENS` |
| `model.max_attempts` | integer | `STACKS_MODEL_MAX_ATTEMPTS` |
| `model.aws_profile` | string | `STACKS_AWS_PROFILE` |
| `model.aws_region` | string | `STACKS_AWS_REGION` |
| `ingestion.lease_duration` | duration string | `STACKS_INGEST_LEASE_DURATION` |
| `ingestion.attempt_timeout` | duration string | `STACKS_INGEST_ATTEMPT_TIMEOUT` |
| `extraction.prompt_version` | string | `STACKS_EXTRACTION_PROMPT_VERSION` |
| `analysis.prompt_version` | string | `STACKS_ANALYSIS_PROMPT_VERSION` |

OAuth client and token paths are file-eligible, but the credential files and
their contents are not. Keep real configuration files uncommitted, store OAuth
material outside the repository with owner-readable permissions, and use only
safe placeholders in shared examples.

These inputs remain environment-only:

| Input | Boundary |
| --- | --- |
| `STACKS_DATABASE_URL`, `STACKS_MIGRATION_DATABASE_URL` | Credential-bearing runtime and migration database URLs |
| `STACKS_DB_ADMIN_PASSWORD`, `STACKS_DB_APP_PASSWORD` | Local Compose database credentials |
| `STACKS_DB_PORT` | Local Compose host-port input |
| `STACKS_TEST_DATABASE_URL`, `STACKS_TEST_MIGRATION_DATABASE_URL` | PostgreSQL integration-test credentials |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` | Direct model-provider credentials |
| AWS default-chain credentials, including `AWS_BEARER_TOKEN_BEDROCK` | AWS SDK credential boundary |
| `STACKS_EMPLOYEE_ENTITY_ID`, `STACKS_MANAGER_ENTITY_ID` | Temporary `analyze` action inputs, not installation configuration |

Any secret-looking, action-input, or unsupported legacy provider name in a
configuration file is an unknown key and fails closed.

`config validate` loads and validates the same target-specific settings used by
execution without initializing observability or constructing PostgreSQL,
Drive, Directory, AWS, Anthropic, OpenAI, or model clients:

```sh
stacks --config /absolute/path/stacks.yaml config validate serve
stacks --config /absolute/path/stacks.yaml config validate auth google
stacks --config /absolute/path/stacks.yaml config validate auth google-directory
```

The first form validates an application target. Other application targets are
`doctor`, `sync`, `entities`, `review`, `analyze`, `db-migrate`, `db-status`,
and `db-reset`. Successful validation confirms syntax and settings only. Use
`doctor` for read-only live dependency readiness and the separately approved
acceptance workflows below for network access, provider quota, corpus behavior,
and model quality.

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
| `STACKS_DB_ADMIN_PASSWORD` | no default | Local Compose administrator password; keep only in the ignored `.env` |
| `STACKS_DB_APP_PASSWORD` | no default | Local Compose application-role password; keep only in the ignored `.env` |
| `STACKS_DB_PORT` | `5432` | Host port for the local Compose database |
| `STACKS_DATABASE_URL` | no default | Application PostgreSQL URL; contains the app password and must remain local |
| `STACKS_MIGRATION_DATABASE_URL` | no default | Schema-capable PostgreSQL URL used only by `db-migrate` and the guarded local reset |
| `STACKS_DATABASE_SCOPES` | `core` | Comma-separated migration scopes; `core` is required and `directory` is optional |
| `STACKS_DATABASE_APP_ROLE` | `stacks_app` | Application role that receives manifest-owned least-privilege grants |
| `STACKS_TEST_DATABASE_URL` | no default | Application-role URL used by PostgreSQL-gated integration tests |
| `STACKS_TEST_MIGRATION_DATABASE_URL` | no default | Schema-capable URL used to create isolated PostgreSQL integration-test databases |
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
| `AWS_BEARER_TOKEN_BEDROCK` | no default | Optional Bedrock API-key credential understood by the AWS SDK; keep only in the ignored `.env` |
| `STACKS_AWS_PROFILE` | no default | Optional shared AWS profile; all AWS commands use the default credential chain when unset |
| `STACKS_AWS_REGION` | no default | Bedrock-only control-plane and runtime region |
| `STACKS_INGEST_LEASE_DURATION` | `5m` | Positive extraction-claim duration, bounded to at most `1h` |
| `STACKS_INGEST_ATTEMPT_TIMEOUT` | `4m` | Per-document attempt deadline; must leave at least `5s` before the extraction lease expires |
| `STACKS_EXTRACTION_PROMPT_VERSION` | `extract-v2` | Versioned extraction prompt; v2 keeps model-proposed email non-authoritative |
| `STACKS_ANALYSIS_PROMPT_VERSION` | `analyze-v1` | Versioned pair-analysis prompt |
| `STACKS_EMPLOYEE_ENTITY_ID` | no default | Accepted employee entity used by `analyze` |
| `STACKS_MANAGER_ENTITY_ID` | no default | Accepted manager entity used by `analyze` |

`STACKS_TEST_DATABASE_URL` is the credential-bearing application-role input
used by repository integration tests. `STACKS_TEST_MIGRATION_DATABASE_URL` is
the schema-capable admin-role input used only by isolated migration
installation and drift tests. Both are used only by the integration-test
target.

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

The canonical database has one required `core` scope and one optional
`directory` scope. Add `directory` to `STACKS_DATABASE_SCOPES` only when
Workspace directory enrichment is enabled. Manager confidence is a temporary
query use case over canonical observations; it is not an installation option,
schema, or migration scope. Google Drive and model providers are runtime
adapters and are never constructed by database commands.

The local image includes pgvector so later vector work does not require a
different image, but the canonical core does not install the `vector`
extension. No vector object is part of the current schema.

`make db-status` reports each known scope independently:

- `absent`: no ledger exists; run `make db-migrate` for a configured scope;
- `pending`: the ledger is valid but newer embedded migrations remain; run
  `make db-migrate`;
- `current`: ledger checksums and the live schema fingerprint match;
- `checksum_mismatch`: applied migration bytes do not match this build; do not
  adopt or repair the ledger manually; and
- `schema_drift`: the ledger is current but an owned database object differs;
  inspect the local change instead of applying migrations blindly.

Doctor reports the same states through read-only checks. A non-current core
scope is unhealthy. An absent directory scope is healthy when directory
enrichment is disabled and actionable when it is enabled.

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

Doctor reads the canonical scoped migration ledgers and schema fingerprints
with the least-privileged application role. It does not apply migrations,
create schemas, or require migration ownership.

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

The canonical schema admits reviewer identity work only from a completed,
admitted extraction run for the source document's current version and from an
independently admitted mention. Later quarantine or a newer current document
version removes that proposal from current review without deleting its
immutable evidence or derivation history.

Document content identity is stable across provider revision-marker changes.
Provider revisions remain append-only source observations, while a changed
content digest creates a new immutable document version. The source document's
current-version pointer changes only after the selected version's validated
extraction commits atomically. Re-observing an already completed version reuses
its extraction without another model call and can make that version current
again.

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
make test-race
make staticcheck
make build
make modules-check
git diff --check
```

With the local database configured and running, also run:

```sh
make db-up
make db-migrate
make db-status
make test-integration ENV_FILE=.env
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
volumes. During early development, the deliberately destructive canonical
PostgreSQL reset is:

```sh
make db-reset CONFIRM=delete-local-stacks-postgres
```

This is the only transition from the retired proof-of-concept migration chain:
migrations `00001` through `00012` have no in-place upgrade or row-copy path.
The command irrecoverably deletes the existing local Compose PostgreSQL data.
Use it only when that data is disposable.

The reset command accepts only loopback database URLs, rejects ambient Docker
or Compose redirection, verifies the exact local Stacks PostgreSQL service and
named volume, removes only that volume, recreates PostgreSQL, and applies the
embedded canonical migrations. It is not part of normal startup or migration
workflow.

Plan C does not deploy anything or enable cloud logging. Existing local
observability remains optional and keeps the privacy rules above. Passing the
deterministic and local PostgreSQL checks validates only the canonical engine
and PostgreSQL adapter; it does not validate live Google Drive, Workspace
Directory, Bedrock, Anthropic, OpenAI, or private-corpus acceptance.
