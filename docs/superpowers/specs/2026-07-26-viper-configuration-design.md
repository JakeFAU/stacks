# Viper Configuration Design

**Date:** 2026-07-26

**Status:** Approved direction; implementation contract

## Purpose

Stacks has a growing set of deliberately configurable runtime settings. Cobra
now provides a typed command boundary, but `internal/config` still reads only
environment variables before Cobra parses the selected command.

This change adds a constrained Viper-backed configuration loader. Operators may
put non-secret settings in one explicit YAML or JSON file while credentials
remain environment-only. The loader preserves typed Stacks settings,
command-specific validation, privacy boundaries, and deterministic startup.

Viper is a source-merging mechanism, not the configuration contract. Stacks
continues to own the schema, defaults, validation, error behavior, and
dependency lifecycle.

## Goals

The change will:

- add a persistent root `--config <path>` option;
- accept one explicit YAML or JSON file containing approved non-secret keys;
- define deterministic precedence across flags, environment, file, and
  defaults;
- reject unknown, duplicate, null, and incorrectly typed file values;
- keep credentials and command operands out of configuration files;
- validate the selected command before observability or live dependencies are
  constructed;
- add an offline command-specific configuration validator; and
- document the complete schema and secret boundary for operators.

The change will not add configuration discovery, implicit search paths, dotenv
parsing, remote configuration, file watching, hot reload, package-global Viper
state, automatic environment scanning, or a plugin system.

## Selected Architecture

Cobra owns argument parsing and Viper-backed loading occurs only after Cobra
has selected a syntactically valid leaf.

The rejected alternatives are:

1. pre-parsing `--config` in `main`, which would parse process arguments twice
   and could make help depend on configuration;
2. binding the Cobra tree directly to package-global Viper state, which would
   couple CLI transport, configuration, and lifecycle behavior.

The selected flow is:

```text
argv
  -> fresh Cobra tree parses once
  -> typed invocation plus explicit config path
  -> fresh Viper-backed loader
  -> typed config.Settings
  -> command-specific validation
  -> observability initialization
  -> selected dependency construction
  -> command execution
  -> observability shutdown
```

Help, completion, unknown commands, unknown flags, and invalid argument arity
stop before the loader. Configuration failures stop before observability and
before PostgreSQL, Google, AWS, Anthropic, OpenAI, or other live dependencies.

## Component Boundaries

### `internal/cli`

`internal/cli` remains the only package that imports Cobra.

The root command receives a persistent `--config` string flag. A valid leaf
passes the explicit path through an ordinary typed invocation or execution
request; no `cobra.Command` or `pflag.FlagSet` crosses the CLI boundary.
The request also preserves whether the flag was explicitly changed so an
omitted path can be distinguished from `--config ""`.

The command tree remains fresh for every `Runner.Run` call. `--config` works
before or after a subcommand and cannot retain a value between runs.

### `internal/config`

`internal/config` remains the only package that imports Viper. Each load creates
one short-lived `viper.New()` instance. The package owns:

- the accepted file schema;
- exact environment bindings;
- named defaults;
- type conversion;
- strict file validation;
- source precedence;
- construction of `config.Settings`; and
- existing value and range validation.

Viper types do not escape this package. `Settings.Validate(command)` remains
the authoritative application validation boundary.

The loader must not use `viper.GetViper`, package-level setters,
`AutomaticEnv`, environment key replacers as an implicit contract, config
search paths, remote providers, `WatchConfig`, or reload callbacks.

### `internal/app`

`internal/app` owns deferred bootstrap after a leaf has been parsed. It:

1. loads settings from the typed configuration request;
2. validates the selected command;
3. initializes observability when the selected command requires execution;
4. lazily constructs only the selected command's dependencies;
5. executes the command; and
6. shuts observability down.

The boundary preserves underlying error identity. It does not stringify
configuration, provider, storage, cancellation, or domain errors.

### `cmd/stacks`

`cmd/stacks` remains process and dependency composition only. It constructs the
bootstrap logger, signal context, configuration loader, observability factory,
runtime factory, and command provider. It does not parse `--config`, import
Viper, or contain configuration policy.

The current eager configuration and observability setup moves behind the
application bootstrap so Cobra can parse exactly once.

## File Selection

Configuration files are optional. Omitting `--config` preserves the existing
defaults-plus-environment behavior.

An explicitly changed `--config` value must be nonblank after trimming.
`--config ""` and whitespace-only paths are CLI validation errors rather than
aliases for omission.

An explicit file must:

- exist and be readable;
- have a `.yaml`, `.yml`, or `.json` extension, compared
  case-insensitively;
- contain one object at its root; and
- conform exactly to the schema in this document.

Stacks does not infer a file name or search the current directory, home
directory, repository, XDG locations, or system configuration directories. A
missing file or unsupported extension is an error. The file is read once per
valid execution. The loader validates the bytes it read, selects the Viper
format from the accepted extension, and passes the same bytes to that Viper
instance through an in-memory reader.

## Precedence

The precedence order, highest first, is:

1. explicitly changed operational Cobra flags;
2. non-empty, explicitly bound environment variables;
3. the explicit configuration file;
4. named Go defaults.

`--config` selects a source; it is not itself an operational setting override.
This change does not add a broad flag mirror for every configuration key.
Future operational flags may participate only when a concrete command needs
one.

Every supported environment variable is bound explicitly to one schema key.
Stacks does not use `AutomaticEnv`. Empty environment variables remain absent,
matching current behavior:

- defaulted values use the named default;
- optional values remain unset; and
- required values remain missing and fail command-specific validation.

File values are typed and explicit. `null` and values of the wrong scalar,
object, or array type are invalid. An empty string for a defaulted string
setting is treated as absent and selects the named default, matching current
empty-environment behavior. An empty string for an optional or required string
remains unset, and command-specific validation determines whether that is
acceptable. Surrounding whitespace is preserved rather than treated as empty,
preserving current field-specific validation behavior. Empty arrays are
explicit values and remain subject to typed and command-specific validation.

## File Schema

YAML uses nested objects and JSON uses the equivalent object structure. The
dotted names below identify the complete accepted key set.

| File key | Type | Environment override |
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

Environment list variables retain their current comma-separated syntax.
File-backed list settings use arrays so commas inside an item are not treated
as separators.

OAuth client and token file paths are eligible settings. The files' contents
remain external and are never parsed or copied by the configuration loader.
The configuration file may still contain private local metadata such as folder
IDs, profile names, and title conventions; documentation recommends
owner-readable local permissions and keeping real local files uncommitted.

## Environment-Only Inputs

Credential-bearing values remain environment-only:

- `STACKS_DATABASE_URL`;
- `STACKS_MIGRATION_DATABASE_URL`;
- `OPENAI_API_KEY`;
- `ANTHROPIC_API_KEY`;
- AWS default-chain credentials, including bearer tokens;
- `STACKS_DB_ADMIN_PASSWORD`;
- `STACKS_DB_APP_PASSWORD`;
- `STACKS_TEST_DATABASE_URL`; and
- `STACKS_TEST_MIGRATION_DATABASE_URL`.

The first four are loaded directly into typed settings outside Viper. Compose,
integration-test, and AWS SDK credential inputs remain owned by their existing
boundaries. None is a Viper key or accepted file alias.

Command operands also remain outside the file schema. In particular,
`STACKS_EMPLOYEE_ENTITY_ID` and `STACKS_MANAGER_ENTITY_ID` remain temporary
environment inputs for the current `analyze` command. Their exclusion prevents
the manager-confidence use case from becoming an installation mode or durable
configuration boundary.

Unsupported legacy provider variables remain environment-only rejection
signals. They are not Viper bindings or accepted file keys:

- `STACKS_BEDROCK_MODEL_ID`;
- `STACKS_BEDROCK_MAX_TOKENS`;
- `STACKS_BEDROCK_MAX_ATTEMPTS`;
- `OPENAI_BASE_URL`;
- `OPENAI_ORG_ID`;
- `OPENAI_PROJECT_ID`;
- `ANTHROPIC_BASE_URL`;
- `ANTHROPIC_AUTH_TOKEN`; and
- `ANTHROPIC_PROFILE`.

Any secret-looking or unsupported name placed in the configuration file is an
unknown key and fails strict schema validation.

## Strict Decoding

Viper's permissive unmarshal behavior is not sufficient by itself. Before
merging sources, the loader validates the selected document against the
explicit schema.

Validation rejects:

- unknown root or nested keys;
- duplicate keys at any object depth;
- a non-object root;
- `null`;
- scalar/object/array shape mismatches;
- non-string members in string arrays; and
- values outside the representable type expected by typed parsing.

Errors may identify the configuration operation, supplied file, and offending
non-secret key. They must not echo values, file contents, credential contents,
environment values, OAuth paths, or private document material.

After structural validation, the dedicated Viper instance applies named
defaults, reads the explicit file, binds approved environment variables, and
constructs `Settings` through explicit typed accessors. Reflection over
`Settings` does not define the public schema.

Existing range and relationship rules remain authoritative, including:

- ports from `1` through `65535`;
- recognized Zap levels;
- finite trace sampling from `0` through `1`;
- positive durations;
- ingestion lease and cleanup-margin bounds;
- directory attempts from `1` through `3`;
- supported database scopes with exactly one `core`;
- safe PostgreSQL application-role identifiers;
- model-provider and disclosure-policy compatibility; and
- command-specific required settings.

## Offline Validation Command

This design deliberately expands the merged Cobra transport with one new
top-level `config` family. It does not reinterpret any existing command or add
configuration behavior to current command flags.

The Cobra tree adds:

```text
stacks [--config <path>] config validate <target>
stacks [--config <path>] config validate auth google
stacks [--config <path>] config validate auth google-directory
```

For non-auth commands, `<target>` is one of the existing top-level application
commands:

```text
serve
doctor
sync
entities
review
analyze
db-migrate
db-status
db-reset
```

The validator loads the same sources and invokes the same target-specific
validation used by execution. The two auth forms additionally run the existing
selected Google authorization validation, avoiding a false success from the
top-level `auth` command's intentionally minimal validation.

Success writes one bounded confirmation that names the validated target but
does not print effective values or their sources. Failure returns the same
wrapped validation error used by normal execution.

The command never initializes observability or constructs PostgreSQL, Drive,
Directory, AWS, Anthropic, OpenAI, or model-provider clients. It validates
syntax and settings, not network reachability, credentials, database
connectivity, migrations, provider quota, or corpus acceptance.

`config explain` is intentionally deferred. Effective-value and source
reporting needs a separate privacy and output contract. Internal tests may use
redacted source attribution, but this change adds no user-facing value dump.

## Errors and Privacy

Cobra retains `SilenceErrors` and `SilenceUsage`; `main` remains the final
error reporter. Pre-leaf syntax errors remain the bounded
`invalid command syntax` result and do not repeat private arguments.

Configuration errors:

- describe the failed operation;
- name only approved non-secret keys or environment variable names needed for
  remediation;
- preserve wrapped filesystem or parsing error identity where useful; and
- never format loaded values.

Provider, storage, and domain errors remain unchanged and discoverable through
`errors.Is`. Configuration loading does not log effective settings.

## Documentation

The implementation updates:

- `README.md` with `--config`, exact precedence, file-selection rules, strict
  decoding behavior, offline validation, and the environment-only secret
  boundary;
- `.env.example` so environment-only credentials and optional overrides remain
  represented by safe placeholders;
- one safe YAML example and one equivalent JSON example using synthetic values;
- CLI command-tree documentation and generated-help expectations; and
- `AGENTS.md` with the approved Viper dependency boundary.

Examples must not contain real folder IDs, entity IDs, credential locations,
database passwords, provider keys, private titles, or private document
contents.

## Dependency Policy

The root module adds `github.com/spf13/viper` at `v1.21.0`, the latest stable
version reported by the Go module proxy when this design was written. The
implementation will verify the release through official documentation,
inspect `go.mod`, `go.sum`, and `go mod graph`, and record the pinned version
in the change.

Viper is an intentional dependency exception because it provides a mature,
well-tested precedence and source-merging mechanism. Its use is restricted to
`internal/config`:

- no Viper dependency in `core`;
- no Viper dependency in `adapters/postgres`;
- no package-global Viper state;
- no remote provider package;
- no config search paths;
- no environment auto-discovery;
- no watcher or reload lifecycle; and
- no Viper values crossing into application or domain contracts.

Transitive support for features outside this design does not authorize their
use.

## Testing

Implementation follows test-driven development. Each changed behavior is first
expressed as a failing public-boundary or focused loader test.

### Configuration loader

Tests cover:

1. no-file compatibility with current defaults and environment behavior;
2. accepted explicit YAML, YML, and JSON files;
3. missing, unreadable, unsupported-extension, malformed, and non-object files;
4. unknown and duplicate keys at multiple nesting depths;
5. null, scalar, object, array, and list-member type failures;
6. every precedence layer;
7. empty environment compatibility and explicit empty file values;
8. environment CSV versus file-array normalization;
9. existing numeric, duration, enum, scope, role, and cross-field bounds;
10. environment-only database and provider secrets remaining usable;
11. secret, action-operand, and legacy keys rejected from files;
12. errors that never contain loaded values or credential paths; and
13. a fresh Viper instance with no retained state across repeated loads.

### CLI and lifecycle

Tests cover:

1. root `--config` before and after subcommands;
2. omitted versus explicitly blank or whitespace-only `--config`;
3. exactly one load for a valid executable leaf;
4. no load for help, completion, unknown commands, unknown flags, or invalid
   arity;
5. configuration validation before observability and live dependency
   construction;
6. selected-command dependency isolation;
7. offline validation for every supported target and both auth targets;
8. no observability or provider construction during offline validation;
9. fresh Cobra flag and writer state across repeated runs;
10. existing generic syntax-error privacy; and
11. preservation of cancellation and wrapped error identity.

### Completion gate

The required verification is:

```sh
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
make db-migrate
make db-status
make test-integration
git diff --check
```

Database checks run against the existing local OrbStack PostgreSQL environment
through ignored local environment configuration. They verify that the startup
refactor and new CLI leaf do not regress migration or PostgreSQL-gated command
behavior.

No Google Drive, Workspace Directory, Bedrock, Anthropic, or OpenAI request is
part of this change. Passing local and PostgreSQL checks does not imply live
provider, quota, authorization, private-corpus, or model-quality acceptance.

## Deliberately Deferred Work

The following require separate designs:

- `config explain` or effective-value output;
- implicit config discovery or system-wide installation paths;
- flag mirrors for all settings;
- environment aliases not already supported;
- dotenv loading inside the application;
- remote configuration;
- file watching or hot reload;
- credential loading from configuration files;
- schema migrations for configuration files; and
- general replacement of the temporary `analyze` use case.

This change creates one hard, typed configuration boundary without turning
Viper into an application framework.
