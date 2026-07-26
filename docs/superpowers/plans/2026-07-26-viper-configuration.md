# Viper Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one strict, explicit YAML/JSON configuration source for non-secret Stacks settings while preserving environment-only credentials, typed command validation, single-pass Cobra parsing, and lazy provider construction.

**Architecture:** `internal/config` reads and structurally validates an explicitly selected document, then uses one fresh `viper.New()` instance to merge named defaults, the file, and exact environment bindings into existing typed `Settings`. `internal/cli` carries an optional config path and a typed offline-validation target; `internal/app` loads and validates after Cobra selects a leaf, then starts the existing runtime and command-provider interfaces through one deferred bootstrap. Help and invalid syntax never load configuration, and `config validate` never starts the bootstrap.

**Tech Stack:** Go 1.26, `github.com/spf13/cobra v1.10.2`, `github.com/spf13/viper v1.21.0`, Viper's `go.yaml.in/yaml/v3 v3.0.4` parser dependency, Go standard `testing`, existing Zap/OpenTelemetry/application composition, local OrbStack PostgreSQL/pgvector.

## Global Constraints

- Accept configuration only through an explicit root `--config <path>`; support `.yaml`, `.yml`, and `.json`, compared case-insensitively.
- Do not add config-name discovery, search paths, XDG/home lookup, dotenv parsing, `AutomaticEnv`, remote configuration, watchers, hot reload, package-global Viper state, or user-facing `config explain`.
- Create one fresh `viper.New()` instance per load and keep Viper imports inside `internal/config`.
- Use exact environment bindings and precedence `changed operational flag > non-empty environment > explicit file > named Go default`.
- There is no operational settings flag in this change; do not invent one merely to exercise the future highest-precedence layer.
- Treat an explicitly blank or whitespace-only `--config` as invalid; omission continues to mean defaults plus environment.
- Read an explicit file once, validate those bytes, and pass the same bytes to Viper through an in-memory reader.
- Reject unknown keys, duplicate keys, nulls, non-object roots, wrong scalar/object/array shapes, non-string list members, and unrepresentable typed values.
- Keep `STACKS_DATABASE_URL`, `STACKS_MIGRATION_DATABASE_URL`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, Compose passwords, integration-test URLs, and AWS default-chain credentials environment-only.
- Permit OAuth client/token file paths in configuration; never read, print, copy, or commit their credential contents.
- Keep `STACKS_EMPLOYEE_ENTITY_ID` and `STACKS_MANAGER_ENTITY_ID` environment-only so the temporary manager-confidence analysis does not become installation configuration.
- Keep unsupported legacy provider variables environment-only rejection signals.
- Preserve current empty-environment semantics. Empty file strings use defaults for defaulted strings and remain unset for optional/required strings; whitespace is not normalized to empty.
- Preserve existing command-specific ranges, cross-field rules, provider disclosure policy, error identity, private-output boundaries, and exact database-reset guard.
- Validate the selected command, including the selected Google auth target, before observability or live dependency construction.
- `config validate` may parse and validate settings but must not initialize observability or construct PostgreSQL, Drive, Directory, AWS, Anthropic, OpenAI, or model clients.
- Keep Cobra inside `internal/cli`, Viper inside `internal/config`, lifecycle ownership inside `internal/app`, and `cmd/stacks` limited to composition.
- Use synthetic fixtures only. Do not call Google Drive, Workspace Directory, Bedrock, Anthropic, or OpenAI.
- Do not print, copy, commit, or inspect secrets or private document contents.

## File Structure

### New files

- `internal/config/document.go`: one-read file selection, YAML/JSON parsing, duplicate detection, and strict schema validation.
- `internal/config/document_test.go`: structural, extension, privacy, and one-read document tests.
- `internal/config/loader.go`: Viper instance setup, exact bindings, typed source conversion, and settings construction.
- `internal/config/loader_test.go`: precedence, defaults, file/env normalization, secret boundary, and state-isolation tests.
- `internal/cli/config.go`: bounded offline-validation success output after application validation.
- `internal/cli/config_test.go`: typed target formatting, invalid invocation, and private-output tests.
- `internal/app/bootstrap.go`: settings-loader and deferred-bootstrap ports, target validation, and bounded shutdown ownership.
- `internal/app/bootstrap_test.go`: loader/validation/bootstrap ordering, shutdown, and offline validation tests.
- `config.example.yaml`: safe complete YAML example containing non-secret placeholders only.
- `config.example.json`: equivalent safe JSON example.

### Existing files

- `internal/config/config.go`: retain settings types and range helpers; delegate legacy no-file `Load()` to the new loader.
- `internal/config/config_test.go`: keep current environment behavior assertions and update shared clearing helpers.
- `internal/config/database_test.go`: continue testing database scope and URL behavior through the no-file wrapper.
- `internal/config/model_settings_test.go`: continue testing provider credentials and unsupported variables through the no-file wrapper.
- `internal/cli/runner.go`: add persistent `--config`, typed configuration input, and `config validate` leaves.
- `internal/cli/runner_test.go`: prove changed/omitted config paths, target parsing, privacy, and fresh flags.
- `internal/app/execute.go`: replace eager-settings execution with parsed-invocation dispatch through the new lifecycle.
- `internal/app/execute_test.go`: retain routing assertions while using fake loaders and bootstraps.
- `internal/app/database_test.go`: retain database command isolation through deferred execution dependencies.
- `cmd/stacks/main.go`: remove eager config/observability setup and compose the concrete deferred bootstrap.
- `cmd/stacks/main_test.go`: prove concrete bootstrap composition and no provider construction before validation.
- `go.mod`, `go.sum`: pin Viper and its reviewed graph.
- `README.md`: document syntax, schema, precedence, strictness, validation, and acceptance boundaries.
- `.env.example`: distinguish environment-only credentials, action inputs, and optional file-backed overrides using safe placeholders.
- `AGENTS.md`: record the constrained Viper dependency exception.

---

### Task 1: Enforce the strict one-read configuration document boundary

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/config/document.go`
- Create: `internal/config/document_test.go`

**Interfaces:**
- Consumes:
  - The complete dotted-key schema in `docs/superpowers/specs/2026-07-26-viper-configuration-design.md`.
  - `go.yaml.in/yaml/v3 v3.0.4`, the parser selected by Viper `v1.21.0`.
- Produces:

```go
package config

type configDocument struct {
	Format string
	Data   []byte
}

func loadConfigDocument(path *string) (configDocument, error)
func validateConfigDocument(format string, data []byte) error
```

`nil` means no file and returns a zero `configDocument`. A non-nil path is
explicit, must be nonblank, must have an accepted extension, and is read once.

- [ ] **Step 1: Add the strict YAML parser companion**

Run:

```sh
go get go.yaml.in/yaml/v3@v3.0.4
```

This is Viper's published YAML parser dependency promoted to a direct
root-module requirement because Stacks uses its AST for duplicate, alias,
merge-key, and null detection. Do not add another YAML implementation.

- [ ] **Step 2: Add failing file-selection and format tests**

Create `internal/config/document_test.go` with table tests using `t.TempDir()`.
Use `os.WriteFile` only in tests to create synthetic fixtures:

```go
func TestLoadConfigDocumentSelectsExplicitFormats(t *testing.T) {
	for _, extension := range []string{".yaml", ".YML", ".json"} {
		t.Run(extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stacks"+extension)
			if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			document, err := loadConfigDocument(&path)
			if err != nil {
				t.Fatalf("loadConfigDocument() error = %v", err)
			}
			if len(document.Data) == 0 {
				t.Fatal("loadConfigDocument() returned no bytes")
			}
			wantFormat := "yaml"
			if strings.EqualFold(extension, ".json") {
				wantFormat = "json"
			}
			if document.Format != wantFormat {
				t.Fatalf("format = %q, want %q", document.Format, wantFormat)
			}
		})
	}
}

func TestLoadConfigDocumentRejectsExplicitBlankAndUnsupportedPaths(t *testing.T) {
	for _, value := range []string{"", "   ", "stacks.toml", "stacks"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			_, err := loadConfigDocument(&value)
			if err == nil {
				t.Fatal("loadConfigDocument() error = nil")
			}
		})
	}
}
```

Add separate tests for a missing path and a directory path. Assert the error
contains the operation category but never fixture contents. Add an unreadable
regular file by writing it with `0o600`, changing it to `0o000` for the load,
and restoring its mode with `t.Cleanup`; assert the failure is still a bounded
read operation and does not expose contents.

- [ ] **Step 3: Run the focused tests and verify the missing boundary**

Run:

```sh
go test ./internal/config -run 'TestLoadConfigDocument' -count=1
```

Expected: FAIL because `loadConfigDocument` does not exist.

- [ ] **Step 4: Implement explicit selection and a one-read document**

In `internal/config/document.go`, add named extension/format constants and
implement `loadConfigDocument` with `strings.TrimSpace`, `filepath.Ext`, and
one `os.ReadFile` call. Do not search for alternative files. Wrap errors as
operations without formatting file contents:

```go
func loadConfigDocument(path *string) (configDocument, error) {
	if path == nil {
		return configDocument{}, nil
	}
	if strings.TrimSpace(*path) == "" {
		return configDocument{}, errors.New("--config requires a nonblank path")
	}
	format, err := configFormat(filepath.Ext(*path))
	if err != nil {
		return configDocument{}, err
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		return configDocument{}, fmt.Errorf("read configuration file: %w", err)
	}
	if err := validateConfigDocument(format, data); err != nil {
		return configDocument{}, fmt.Errorf("validate configuration file: %w", err)
	}
	return configDocument{Format: format, Data: data}, nil
}
```

- [ ] **Step 5: Add failing strict-schema tests**

Add tests covering YAML and JSON for:

- valid nested objects and every accepted scalar/list shape;
- non-object root;
- unknown root and nested keys;
- duplicate root and nested keys;
- explicit `null`;
- YAML aliases, anchors, and merge keys;
- additional YAML documents;
- string where integer/number/boolean/object/list is required;
- non-string list members; and
- integers larger than the platform `int` range and non-finite/overflowing
  JSON numbers such as `1e10000`; and
- a second JSON value after the root.

Use small fixtures such as:

```go
func TestValidateConfigDocumentRejectsUnknownDuplicateNullAndWrongTypes(t *testing.T) {
	tests := []struct {
		name   string
		format string
		body   string
	}{
		{"unknown YAML key", "yaml", "http:\n  mystery: true\n"},
		{"duplicate YAML key", "yaml", "http:\n  port: 8080\n  port: 9090\n"},
		{"null YAML value", "yaml", "http:\n  host: null\n"},
		{"wrong YAML type", "yaml", "http:\n  port: \"8080\"\n"},
		{"duplicate JSON key", "json", `{"http":{"port":8080,"port":9090}}`},
		{"null JSON value", "json", `{"http":{"host":null}}`},
		{"wrong JSON list member", "json", `{"google":{"notes_titles":["Notes",7]}}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateConfigDocument(testCase.format, []byte(testCase.body))
			if err == nil {
				t.Fatal("validateConfigDocument() error = nil")
			}
			if strings.Contains(err.Error(), testCase.body) {
				t.Fatalf("error exposed document body: %v", err)
			}
		})
	}
}
```

Add one test that places synthetic credential-like values beneath rejected keys
such as `database.url` and `model.openai_api_key`; assert neither value appears
in the error.

- [ ] **Step 6: Run strict-schema tests and verify they fail**

Run:

```sh
go test ./internal/config -run 'TestValidateConfigDocument' -count=1
```

Expected: FAIL because strict structural validation is not implemented.

- [ ] **Step 7: Implement an explicit schema and strict YAML/JSON walkers**

Define unexported schema node kinds for object, string, integer, number,
boolean, duration string, and string array. Construct the entire approved
schema explicitly; do not reflect over `Settings`.

For YAML, decode into `yaml.Node`, require exactly one document/object, walk
mapping nodes while tracking keys per mapping, reject `!!null`, and validate
node tags/kinds against the schema. Reject aliases, anchors, merge keys, and a
second document instead of inheriting YAML behaviors that are not part of the
Stacks configuration language.

For JSON, use `json.Decoder.Token` with `UseNumber` and a recursive decoder that
tracks keys per object before inserting them. Require EOF after the root.
Validate the resulting typed tree against the same schema. Error messages name
only the offending dotted key and expected type.

Keep the accepted leaves exactly:

```text
http.host
http.port
http.read_header_timeout_seconds
log.level
telemetry.enabled
telemetry.endpoint
telemetry.insecure
telemetry.metric_export_interval
telemetry.service_name
telemetry.trace_sample_ratio
database.scopes
database.application_role
google.folder_id
google.oauth_client_file
google.oauth_token_file
google.transcript_titles
google.notes_titles
directory.enabled
directory.oauth_client_file
directory.oauth_token_file
directory.email_domains
directory.freshness
directory.retry_after
directory.max_attempts
model.data_mode
model.provider
model.id
model.max_output_tokens
model.max_attempts
model.aws_profile
model.aws_region
ingestion.lease_duration
ingestion.attempt_timeout
extraction.prompt_version
analysis.prompt_version
```

- [ ] **Step 8: Verify the strict document boundary**

Run:

```sh
go test ./internal/config -run 'TestLoadConfigDocument|TestValidateConfigDocument' -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 9: Commit the document boundary**

```sh
git add go.mod go.sum internal/config/document.go internal/config/document_test.go
git commit -m "Add strict configuration document boundary"
```

---

### Task 2: Merge defaults, explicit files, and exact environments into typed settings

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/config/loader.go`
- Create: `internal/config/loader_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/config/database_test.go`
- Modify: `internal/config/model_settings_test.go`

**Interfaces:**
- Consumes:
  - `configDocument` and `loadConfigDocument(*string)` from Task 1.
  - Existing `Settings`, `DatabaseSettings`, `TelemetrySettings`,
    `ApplicationSettings`, and their current validation helpers.
- Produces:

```go
package config

type LoadOptions struct {
	ConfigFile *string
}

func Load() (Settings, error)
func LoadWithOptions(LoadOptions) (Settings, error)
```

`Load()` remains the no-file convenience wrapper and calls
`LoadWithOptions(LoadOptions{})`. `LoadWithOptions` creates a fresh Viper
instance and returns only typed Stacks settings.

- [ ] **Step 1: Pin Viper and add failing precedence tests**

Add `github.com/spf13/viper v1.21.0` to the root module and let `go mod tidy`
select its published graph.

Create `internal/config/loader_test.go` with isolated environment setup and a
synthetic YAML file:

```go
func TestLoadWithOptionsAppliesEnvironmentOverFileOverDefaults(t *testing.T) {
	clearConfigurationEnvironment(t)
	path := writeConfigFixture(t, ".yaml", `
http:
  host: 192.0.2.10
  port: 8181
log:
  level: warn
model:
  max_attempts: 2
`)
	t.Setenv(HTTPPortEnvironmentVariable, "9191")

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if settings.HTTPAddress != "192.0.2.10:9191" {
		t.Fatalf("HTTPAddress = %q", settings.HTTPAddress)
	}
	if settings.LogLevel != "warn" {
		t.Fatalf("LogLevel = %q, want warn from file", settings.LogLevel)
	}
	if settings.Telemetry.ServiceName != defaultOTelServiceName {
		t.Fatalf("ServiceName = %q, want default", settings.Telemetry.ServiceName)
	}
	if settings.Application.Model.MaxAttempts != 2 {
		t.Fatalf("MaxAttempts = %d, want 2", settings.Application.Model.MaxAttempts)
	}
}
```

Add an equivalent JSON test, and a repeated-load test proving file and
environment state from the first load cannot appear in the second.

- [ ] **Step 2: Run precedence tests and verify they fail**

Run:

```sh
go test ./internal/config -run 'TestLoadWithOptions' -count=1
```

Expected: FAIL because `LoadOptions` and `LoadWithOptions` do not exist.

- [ ] **Step 3: Implement a fresh Viper instance and exact bindings**

Move source-loading logic from `config.go` into `loader.go`. Keep settings
types, validation methods, and named limits in their current focused files.

Implement:

```go
func Load() (Settings, error) {
	return LoadWithOptions(LoadOptions{})
}

func LoadWithOptions(options LoadOptions) (Settings, error) {
	document, err := loadConfigDocument(options.ConfigFile)
	if err != nil {
		return Settings{}, err
	}
	values := viper.New()
	setDefaults(values)
	if len(document.Data) != 0 {
		values.SetConfigType(document.Format)
		if err := values.ReadConfig(bytes.NewReader(document.Data)); err != nil {
			return Settings{}, fmt.Errorf("read validated configuration: %w", err)
		}
	}
	if err := bindEnvironment(values); err != nil {
		return Settings{}, err
	}
	return settingsFrom(values)
}
```

`setDefaults` must use the existing named Go defaults. `bindEnvironment` calls
`BindEnv(fileKey, exactEnvironmentName)` for every approved non-secret file
key and no other key. Do not call `AutomaticEnv` or `AllowEmptyEnv(true)`.

Add typed accessors that inspect the winning `v.Get(key)` value and validate
the concrete type before conversion. Environment winners are strings; file and
default winners retain their typed representation. Return bounded errors using
the existing environment name for operator continuity.

- [ ] **Step 4: Add failing normalization, range, and secret-boundary tests**

Cover:

- empty environment does not suppress a file value;
- empty file string selects defaults for defaulted string fields;
- empty file string remains unset for required/optional string fields;
- whitespace remains unchanged;
- environment CSV lists and file string arrays produce the current typed lists;
- file arrays may contain commas inside one string;
- all current integer, duration, log-level, sampling, scope, role, directory,
  lease-margin, and model-attempt failures;
- database URLs and API keys are read only from the environment;
- file attempts to define database URLs, API keys, employee/manager IDs, or
  legacy variables fail in Task 1 before settings construction;
- errors do not contain synthetic secret values or OAuth paths; and
- repeated loads use independent Viper instances.

Use a credential test shaped like:

```go
func TestLoadWithOptionsKeepsCredentialsEnvironmentOnly(t *testing.T) {
	clearConfigurationEnvironment(t)
	const databaseSecret = "postgres://synthetic-secret@localhost/stacks"
	const providerSecret = "synthetic-provider-secret"
	t.Setenv(DatabaseURLEnvironmentVariable, databaseSecret)
	t.Setenv(OpenAIAPIKeyEnvironmentVariable, providerSecret)
	path := writeConfigFixture(t, ".json", `{"model":{"provider":"openai"}}`)

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if settings.Database.URL != databaseSecret || settings.Application.Model.OpenAIAPIKey != providerSecret {
		t.Fatal("environment-only credentials were not retained in memory")
	}
}
```

Never log or print the compared values.

- [ ] **Step 5: Run the focused loader suite and verify failures**

Run:

```sh
go test ./internal/config -run 'TestLoadWithOptions' -count=1
```

Expected: new edge-case tests fail until typed accessors and exact normalization
are complete.

- [ ] **Step 6: Complete typed settings construction**

Build the same `Settings` shape currently returned by `Load`. Reuse or
generalize the current positive-integer, optional-integer, boolean, duration,
unit-interval, database-scope, title-set, and unsupported-environment helpers
so validation logic has one owner.

Read these environment-only values directly with `os.Getenv`:

```text
STACKS_DATABASE_URL
STACKS_MIGRATION_DATABASE_URL
OPENAI_API_KEY
ANTHROPIC_API_KEY
STACKS_EMPLOYEE_ENTITY_ID
STACKS_MANAGER_ENTITY_ID
```

Continue detecting non-empty unsupported provider variables with
`os.LookupEnv`. Do not bind any of those names to Viper.

- [ ] **Step 7: Preserve the existing no-file suite**

Keep `Load()` as:

```go
func Load() (Settings, error) {
	return LoadWithOptions(LoadOptions{})
}
```

Run the existing `config_test.go`, `database_test.go`, and
`model_settings_test.go` unchanged where possible. Update only shared
environment-clearing helpers or assertions whose source-aware error wording
deliberately changes; do not weaken behavioral assertions.

- [ ] **Step 8: Verify configuration behavior and the module graph**

Run:

```sh
go mod tidy
go test ./internal/config -count=1
make modules-check
git diff --check
```

Expected: PASS. Inspect `go.mod`, `go.sum`, and:

```sh
go mod graph | rg 'spf13/viper|go.yaml.in/yaml'
```

Confirm Viper is in the root module only and no remote-provider package was
added directly.

- [ ] **Step 9: Commit typed Viper loading**

```sh
git add go.mod go.sum internal/config/config.go internal/config/config_test.go internal/config/database_test.go internal/config/model_settings_test.go internal/config/loader.go internal/config/loader_test.go
git commit -m "Load typed settings through Viper"
```

---

### Task 3: Carry configuration selection and offline validation through Cobra

**Files:**
- Modify: `internal/cli/runner.go`
- Modify: `internal/cli/runner_test.go`
- Create: `internal/cli/config.go`
- Create: `internal/cli/config_test.go`

**Interfaces:**
- Consumes:
  - Existing fresh-tree `Runner` and typed `Invocation`.
- Produces:

```go
package cli

const CommandConfig CommandName = "config"
const ActionValidate Action = "validate"

type ConfigValidationInput struct {
	Command CommandName
	Action  Action
}

type ConfigValidateCommand struct {
	Output io.Writer
}

func (ConfigValidateCommand) Run(context.Context, Invocation) error

type Invocation struct {
	Command          CommandName
	Action           Action
	Arguments        []string
	ConfigFile       *string
	ConfigValidation *ConfigValidationInput
	CreatePerson     *CreatePersonInput
	AcceptDirectory  *AcceptDirectoryInput
}
```

Every leaf copies the explicitly changed root config value into `ConfigFile`.
`nil` means omitted; a non-nil pointer means explicitly changed. The
configuration-validation target is typed and never reparsed by the
application.

- [ ] **Step 1: Add failing persistent-config tests**

Add:

```go
func TestRunnerCarriesExplicitConfigBeforeAndAfterCommand(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "/synthetic/stacks.yaml", "sync"},
		{"sync", "--config", "/synthetic/stacks.yaml"},
	} {
		var got Invocation
		err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
			got = invocation
			return nil
		}}).Run(t.Context(), args)
		if err != nil {
			t.Fatalf("Run(%q) error = %v", args, err)
		}
		if got.ConfigFile == nil || *got.ConfigFile != "/synthetic/stacks.yaml" {
			t.Fatalf("ConfigFile = %#v", got.ConfigFile)
		}
	}
}

func TestRunnerDistinguishesOmittedAndBlankConfig(t *testing.T) {
	var omitted Invocation
	if err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
		omitted = invocation
		return nil
	}}).Run(t.Context(), []string{"serve"}); err != nil {
		t.Fatal(err)
	}
	if omitted.ConfigFile != nil {
		t.Fatalf("omitted ConfigFile = %#v, want nil", omitted.ConfigFile)
	}

	calls := 0
	err := (Runner{Execute: func(context.Context, Invocation) error {
		calls++
		return nil
	}}).Run(t.Context(), []string{"--config", "", "serve"})
	if err == nil || calls != 0 {
		t.Fatalf("blank config error/calls = %v/%d", err, calls)
	}
}
```

Add a whitespace-only case and a same-Runner test proving a first config path
does not appear in the second invocation.

- [ ] **Step 2: Run the focused CLI tests and verify failure**

Run:

```sh
go test ./internal/cli -run 'TestRunnerCarriesExplicitConfig|TestRunnerDistinguishesOmittedAndBlankConfig' -count=1
```

Expected: FAIL because the root flag and typed input do not exist.

- [ ] **Step 3: Add the fresh persistent flag and typed copy**

Define a named `configFlagName = "config"` constant. Register it with
`root.PersistentFlags().String(...)` on each fresh tree.

Before executing a leaf, check `command.Flags().Changed(configFlagName)` (which
includes inherited persistent flags), read the string, reject a trimmed blank,
copy it to a new local variable, and set `Invocation.ConfigFile = &copied`.
Never retain the flag value on `Runner`.

- [ ] **Step 4: Add failing `config validate` tree tests**

Add a table covering all supported targets:

```go
func TestRunnerParsesConfigValidationTargets(t *testing.T) {
	tests := []struct {
		args        []string
		wantCommand CommandName
		wantAction  Action
	}{
		{[]string{"config", "validate", "serve"}, CommandServe, ""},
		{[]string{"config", "validate", "doctor"}, CommandDoctor, ""},
		{[]string{"config", "validate", "sync"}, CommandSync, ""},
		{[]string{"config", "validate", "entities"}, CommandEntities, ""},
		{[]string{"config", "validate", "review"}, CommandReview, ""},
		{[]string{"config", "validate", "analyze"}, CommandAnalyze, ""},
		{[]string{"config", "validate", "db-migrate"}, CommandDBMigrate, ""},
		{[]string{"config", "validate", "db-status"}, CommandDBStatus, ""},
		{[]string{"config", "validate", "db-reset"}, CommandDBReset, ""},
		{[]string{"config", "validate", "auth", "google"}, CommandAuth, ActionAuthGoogle},
		{[]string{"config", "validate", "auth", "google-directory"}, CommandAuth, ActionAuthGoogleDirectory},
	}
	for _, testCase := range tests {
		var got Invocation
		err := (Runner{Execute: func(_ context.Context, invocation Invocation) error {
			got = invocation
			return nil
		}}).Run(t.Context(), testCase.args)
		if err != nil {
			t.Fatalf("Run(%q) error = %v", testCase.args, err)
		}
		if got.Command != CommandConfig || got.Action != ActionValidate ||
			got.ConfigValidation == nil ||
			got.ConfigValidation.Command != testCase.wantCommand ||
			got.ConfigValidation.Action != testCase.wantAction {
			t.Fatalf("Run(%q) invocation = %#v", testCase.args, got)
		}
	}
}
```

Add invalid target, missing target, extra argument, and `auth` without a target
cases. Assert `Execute` is never called and private input is absent from both
the returned error and stderr.

- [ ] **Step 5: Implement declarative validation leaves**

Add:

```text
config
└── validate
    ├── serve
    ├── auth
    │   ├── google
    │   └── google-directory
    ├── doctor
    ├── sync
    ├── entities
    ├── review
    ├── analyze
    ├── db-migrate
    ├── db-status
    └── db-reset
```

Each no-argument leaf emits `CommandConfig`, `ActionValidate`, and one
`ConfigValidationInput`. Do not accept arbitrary strings and then parse them in
`internal/app`.

- [ ] **Step 6: Add bounded offline-validation output**

In `internal/cli/config_test.go`, test every valid typed target, a missing
`ConfigValidationInput`, and an invalid auth action. Successful output is
exactly one of:

```text
configuration valid for serve
configuration valid for auth google
configuration valid for auth google-directory
```

with the equivalent fixed target name for every other supported command.
Assert no argument, config path, setting value, or environment value is
formatted.

Implement `ConfigValidateCommand.Run` as a transport-only writer. It validates
the already-typed invocation shape, formats a target from a closed switch, and
writes one line to its explicit `Output`. It does not load or validate
settings; `internal/app` invokes it only after application validation succeeds.

- [ ] **Step 7: Verify CLI privacy, help, completion, and fresh state**

Run:

```sh
go test ./internal/cli -count=1
git diff --check
```

Expected: PASS, including all existing Cobra tests.

- [ ] **Step 8: Commit the typed Cobra configuration transport**

```sh
git add internal/cli/runner.go internal/cli/runner_test.go internal/cli/config.go internal/cli/config_test.go
git commit -m "Add typed configuration CLI inputs"
```

---

### Task 4: Move loading, validation, and shutdown ownership into the application

**Files:**
- Create: `internal/app/bootstrap.go`
- Create: `internal/app/bootstrap_test.go`
- Modify: `internal/app/execute.go`
- Modify: `internal/app/execute_test.go`
- Modify: `internal/app/database_test.go`

**Interfaces:**
- Consumes:
  - `config.LoadOptions` from Task 2.
  - `cli.Invocation.ConfigFile` and `cli.ConfigValidationInput` from Task 3.
  - Existing `Runtime`, `RuntimeFunc`, `CommandProvider`, and
    `CommandProviderFunc` interfaces in `internal/app`.
- Produces:

```go
package app

type SettingsLoader interface {
	Load(config.LoadOptions) (config.Settings, error)
}

type SettingsLoaderFunc func(config.LoadOptions) (config.Settings, error)

type ExecutionDependencies struct {
	Runtime         Runtime
	CommandProvider CommandProvider
	Shutdown        func(context.Context) error
}

type Bootstrap interface {
	Start(context.Context, config.Settings) (ExecutionDependencies, error)
}

type BootstrapFunc func(context.Context, config.Settings) (ExecutionDependencies, error)

func Execute(
	ctx context.Context,
	args []string,
	loader SettingsLoader,
	bootstrap Bootstrap,
	stdout, stderr io.Writer,
) error
```

The adapter functions implement their respective interface methods.
`ExecutionDependencies` preserves the current runtime and command-provider
contracts instead of inventing a parallel session abstraction.

```go
func (fn SettingsLoaderFunc) Load(options config.LoadOptions) (config.Settings, error) {
	return fn(options)
}

func (fn BootstrapFunc) Start(
	ctx context.Context,
	settings config.Settings,
) (ExecutionDependencies, error) {
	return fn(ctx, settings)
}
```

- [ ] **Step 1: Add failing lifecycle-order tests**

Create `internal/app/bootstrap_test.go` with a recording bootstrap:

```go
type recordingBootstrap struct {
	calls        *[]string
	dependencies ExecutionDependencies
	err          error
}

func (bootstrap recordingBootstrap) Start(context.Context, config.Settings) (ExecutionDependencies, error) {
	*bootstrap.calls = append(*bootstrap.calls, "bootstrap")
	return bootstrap.dependencies, bootstrap.err
}
```

Test valid `sync` ordering with the existing function adapters:

```go
func TestExecuteLoadsValidatesThenBootstrapsSelectedCommand(t *testing.T) {
	calls := []string{}
	settings := config.Settings{
		Database: config.DatabaseSettings{URL: "postgres://synthetic"},
		Application: validSyncSettingsForExecute("extract-v2", "analyze-v1"),
	}
	loader := SettingsLoaderFunc(func(config.LoadOptions) (config.Settings, error) {
		calls = append(calls, "load")
		return settings, nil
	})
	dependencies := ExecutionDependencies{
		Runtime: RuntimeFunc(func(context.Context, config.Settings) error {
			return errors.New("serve must not run")
		}),
		CommandProvider: CommandProviderFunc(func(
			context.Context, config.Settings, io.Writer, io.Writer,
		) (map[string]cli.Command, error) {
			calls = append(calls, "commands")
			return map[string]cli.Command{"sync": cli.CommandFunc(func(context.Context, cli.Invocation) error {
				calls = append(calls, "run")
				return nil
			})}, nil
		}),
		Shutdown: func(context.Context) error {
			calls = append(calls, "shutdown")
			return nil
		},
	}
	bootstrap := recordingBootstrap{calls: &calls, dependencies: dependencies}

	if err := Execute(t.Context(), []string{"sync"}, loader, bootstrap, io.Discard, io.Discard); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := strings.Join(calls, ","), "load,bootstrap,commands,run,shutdown"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}
```

Add a companion invalid-settings test with a recording bootstrap and assert
there is no `bootstrap` call.

- [ ] **Step 2: Add no-load syntax and no-bootstrap offline tests**

Add tests proving:

- root/nested help, completion help, unknown commands, unknown flags, and
  invalid arity never call the loader or bootstrap;
- a loader failure never calls bootstrap;
- normal Google auth validates the selected target before bootstrap;
- `config validate auth google` and `google-directory` run selected-target
  validation without bootstrap;
- every other offline target calls the same `Settings.Validate` path;
- success delegates to `cli.ConfigValidateCommand` and writes exactly its
  bounded fixed-target line;
- failures write no effective values;
- bootstrap failure preserves its sentinel identity;
- command failure still triggers shutdown; and
- command and shutdown errors are both discoverable with `errors.Is`; and
- a caller-canceled command preserves `context.Canceled` identity while
  shutdown receives a non-canceled context carrying the caller's values.

- [ ] **Step 3: Run lifecycle tests and verify the old signature fails**

Run:

```sh
go test ./internal/app -run 'TestExecute' -count=1
```

Expected: FAIL because the settings-loader/bootstrap interfaces and new
`Execute` signature do not exist.

- [ ] **Step 4: Implement target-specific pre-bootstrap validation**

In `bootstrap.go`, add:

```go
type validationTarget struct {
	Command config.Command
	Auth    config.GoogleAuthTarget
}

func targetForInvocation(cli.Invocation) (validationTarget, error)
func validateInvocation(config.Settings, validationTarget) error
```

For normal commands, derive the target from `Invocation.Command` and
`Invocation.Action`. For `CommandConfig`, require the typed
`ConfigValidationInput`; never create `config.CommandConfig`.

`validateInvocation` first calls `Settings.Validate(target.Command)`. When
`target.Auth` is present, it then calls
`Settings.Application.ValidateGoogleAuth(target.Auth)`. This makes both normal
auth and offline auth validation complete before observability.

- [ ] **Step 5: Implement deferred bootstrap and bounded shutdown**

`Execute` constructs one fresh `cli.Runner`. Its callback:

1. calls `loader.Load(config.LoadOptions{ConfigFile: invocation.ConfigFile})`;
2. resolves and validates the target;
3. invokes `cli.ConfigValidateCommand{Output: stdout}` and returns for
   `CommandConfig`;
4. calls `bootstrap.Start` only for executable application commands;
5. dispatches serve through `Runtime` or a non-server command through
   `CommandProvider`;
6. shuts down every successfully returned dependency set with a named
   ten-second timeout; and
7. returns `errors.Join(runError, shutdownError)`.

If the loader, bootstrap, selected runtime, command provider, or shutdown
function is absent when required, return a bounded not-configured error. Define
`const runtimeShutdownTimeout = 10 * time.Second` and create shutdown context
with
`context.WithTimeout(context.WithoutCancel(ctx), runtimeShutdownTimeout)` so
caller cancellation does not skip flushing while caller context values remain
available.

Keep the repeated validation in `cmd/stacks.commandProviderWithRuntime` as
defensive checks for its direct composition tests; the application validation
is the new pre-observability authority.

- [ ] **Step 6: Update existing app routing and database tests**

Replace eager `Settings`, `RuntimeFunc`, and `CommandProviderFunc` parameters
with one settings loader and recording bootstrap while preserving every
existing assertion:

- no-argument serve;
- entity/review typed arguments;
- selected Google auth;
- lazy sync/doctor/analyze routing;
- prompt-contract rejection before dependencies;
- database command isolation; and
- original error identity.

Do not delete a test merely because the lifecycle signature changed.

- [ ] **Step 7: Verify the application boundary**

Run:

```sh
go test ./internal/app -count=1
go test -race ./internal/app -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 8: Commit application-owned bootstrap**

```sh
git add internal/app/bootstrap.go internal/app/bootstrap_test.go internal/app/execute.go internal/app/execute_test.go internal/app/database_test.go
git commit -m "Move runtime bootstrap behind validation"
```

---

### Task 5: Wire deferred observability in the process entrypoint

**Files:**
- Modify: `cmd/stacks/main.go`
- Modify as required by the signature change: `cmd/stacks/main_test.go`
- Modify as required by the signature change: `cmd/stacks/database_test.go`
- Modify as required by the signature change: `cmd/stacks/canonical_composition_test.go`

**Interfaces:**
- Consumes:
  - `app.SettingsLoaderFunc`, `app.BootstrapFunc`, and
    `app.ExecutionDependencies` from Task 4.
  - Existing `config.LoadWithOptions`, `observability.New`, `app.Run`, and
    `commandProvider`.
- Produces:
  - One deferred bootstrap closure passed by `main` to `app.Execute`.

- [ ] **Step 1: Add a failing concrete-bootstrap composition test**

Extract an unexported constructor function variable or helper that tests can
invoke without running `main`:

```go
func newExecutionDependencies(
	ctx context.Context,
	settings config.Settings,
) (app.ExecutionDependencies, error)
```

Add tests with noop telemetry proving the returned dependencies:

- contain a non-nil runtime adapter without invoking `Serve` in this
  composition test;
- create the existing command map only when `Commands` is requested;
- construct decision and model telemetry from the same observability runtime;
- expose that runtime's `Shutdown`; and
- retain the exact validated settings in command-provider construction.

Do not call the concrete runtime's `Serve` merely to inspect wiring because
that starts a real HTTP listener. Application-level serve dispatch remains
covered with a fake `RuntimeFunc` in Task 4. Do not enable OTLP or construct
Google, database, AWS, Anthropic, OpenAI, or model clients.

- [ ] **Step 2: Run the focused main test and verify failure**

Run:

```sh
go test ./cmd/stacks -run 'TestExecutionDependencies' -count=1
```

Expected: FAIL because deferred composition does not exist.

- [ ] **Step 3: Replace eager main setup**

Keep bootstrap logger creation and signal context. Remove eager
`config.Load()`, `observability.New`, configured logger selection, and shutdown
from `main`.

Call:

```go
runErr := app.Execute(
	ctx,
	os.Args[1:],
	app.SettingsLoaderFunc(config.LoadWithOptions),
	app.BootstrapFunc(newExecutionDependencies),
	os.Stdout,
	os.Stderr,
)
```

`newExecutionDependencies` constructs `observability.Runtime`, then returns:

```go
app.ExecutionDependencies{
	Runtime: app.RuntimeFunc(func(ctx context.Context, settings config.Settings) error {
		return app.Run(ctx, settings, runtime.Logger(), runtime.TracerProvider(), runtime.MeterProvider())
	}),
	CommandProvider: app.CommandProviderFunc(func(
		ctx context.Context,
		settings config.Settings,
		stdout, stderr io.Writer,
	) (map[string]cli.Command, error) {
		decisions, err := runtime.DecisionRecorder()
		if err != nil {
			return nil, err
		}
		invocations, err := modeltelemetry.NewMetricsRecorder(runtime.MeterProvider().Meter("stacks"))
		if err != nil {
			return nil, err
		}
		return commandProvider(
			ctx, settings, stdout, stderr,
			runtime.TracerProvider().Tracer("stacks"), decisions, invocations,
		)
	}),
	Shutdown: runtime.Shutdown,
}
```

The adapters retain `settings` as method arguments, so do not close over a
second mutable settings copy. If constructing any dependency after
`observability.New` fails before returning the struct, shut the runtime down
before returning.

After `app.Execute`, report the final joined error once through the bootstrap
logger and set exit status. `main` no longer owns a telemetry runtime to shut
down or use for final reporting.

- [ ] **Step 4: Update composition tests without weakening provider isolation**

Update only compilation and lifecycle setup in existing `cmd/stacks` tests.
Preserve direct tests for:

- restricted disclosure before external construction;
- auth target isolation;
- database-command isolation;
- canonical repository composition; and
- lazy provider/model construction.

- [ ] **Step 5: Verify entrypoint composition**

Run:

```sh
go test ./cmd/stacks ./internal/app -count=1
go build ./cmd/stacks
git diff --check
```

Expected: PASS.

- [ ] **Step 6: Commit deferred process wiring**

```sh
git add cmd/stacks/main.go cmd/stacks/main_test.go cmd/stacks/database_test.go cmd/stacks/canonical_composition_test.go
git commit -m "Wire deferred process observability"
```

---

### Task 6: Document the operator contract

**Files:**
- Create: `config.example.yaml`
- Create: `config.example.json`
- Modify: `README.md`
- Modify: `.env.example`
- Modify: `AGENTS.md`
- Modify: `docs/superpowers/specs/2026-07-26-viper-configuration-design.md` only if implementation review uncovers a true contract correction

**Interfaces:**
- Consumes:
  - Final CLI help and schema implemented by Tasks 1-5.
- Produces:
  - Safe executable examples and an operator-facing precedence/secret contract.

- [ ] **Step 1: Add complete synthetic YAML and JSON examples**

Both files must express the same non-secret settings and no environment-only
inputs. Use documentation-only values such as:

```yaml
http:
  host: 127.0.0.1
  port: 8080
  read_header_timeout_seconds: 5
log:
  level: info
telemetry:
  enabled: false
  endpoint: 127.0.0.1:4317
  insecure: true
  metric_export_interval: 10s
  service_name: stacks
  trace_sample_ratio: 1
database:
  scopes: [core]
  application_role: stacks_app
google:
  folder_id: replace-with-folder-id
  oauth_client_file: /absolute/path/outside-repository/google-oauth-client.json
  oauth_token_file: /absolute/path/outside-repository/stacks-google-token.json
  transcript_titles: [Transcript]
  notes_titles: [Notes]
directory:
  enabled: false
  oauth_client_file: /absolute/path/outside-repository/directory-client.json
  oauth_token_file: /absolute/path/outside-repository/directory-token.json
  email_domains: [corp.example]
  freshness: 24h
  retry_after: 15m
  max_attempts: 3
model:
  data_mode: personal
  provider: openai
  id: replace-with-explicit-model-id
  max_output_tokens: 2048
  max_attempts: 1
  aws_profile: ""
  aws_region: ""
ingestion:
  lease_duration: 5m
  attempt_timeout: 4m
extraction:
  prompt_version: extract-v2
analysis:
  prompt_version: analyze-v1
```

The JSON file uses the same keys, arrays, booleans, numbers, and duration
strings. Do not add database URLs, API keys, passwords, employee/manager IDs,
tokens, or real local metadata.

- [ ] **Step 2: Test both examples through offline validation**

Run them with an offline target that requires no credentials:

```sh
go run ./cmd/stacks --config config.example.yaml config validate serve
go run ./cmd/stacks --config config.example.json config validate serve
```

Expected output:

```text
configuration valid for serve
```

No provider or database call occurs.

- [ ] **Step 3: Update README and CLI tree**

Document:

- `--config` before or after subcommands;
- the new `config validate` tree, including both auth targets;
- accepted extensions and absence of discovery;
- exact precedence;
- strict unknown/duplicate/null/type failures;
- empty environment and file semantics;
- file-eligible key table;
- environment-only secret and action-input table;
- OAuth paths versus credential contents;
- no dotenv parsing, remote config, watching, reload, or `config explain`;
- owner-readable permissions and uncommitted real local files; and
- offline validation versus doctor/live acceptance.

Update the command tree to include:

```text
config
└── validate
    ├── <application-target>
    └── auth
        ├── google
        └── google-directory
```

- [ ] **Step 4: Update environment and agent guidance**

In `.env.example`, keep safe placeholders for all secrets and explicitly label:

- database/provider credentials as environment-only;
- employee/manager IDs as temporary analyze inputs and environment-only; and
- file-eligible values as optional environment overrides.

In `AGENTS.md`, add:

```text
Viper is the intentional exception for the root application's configuration
source merging. Keep github.com/spf13/viper inside internal/config, use a fresh
instance per load, bind supported environment variables explicitly, and keep
Viper types out of core, adapters/postgres, providers, storage, application,
and domain contracts. Do not use package-global Viper state, AutomaticEnv,
search paths, remote providers, watchers, or reload callbacks.
```

Also document `go.yaml.in/yaml/v3` as the narrow strict-decoding companion
inside `internal/config`; it may inspect syntax but must not define settings,
precedence, defaults, or validation policy.

- [ ] **Step 5: Verify and commit operator documentation**

Run:

```sh
make fmt
go test ./internal/config ./internal/cli ./internal/app ./cmd/stacks -count=1
go run ./cmd/stacks --config config.example.yaml config validate serve
go run ./cmd/stacks --config config.example.json config validate serve
git diff --check
```

Expected: PASS.

Commit:

```sh
git add README.md .env.example AGENTS.md config.example.yaml config.example.json
git commit -m "Document explicit file configuration"
```

---

### Task 7: Run whole-branch review and completion gates

**Files:**
- Modify only when a failing test or review finding requires a scoped fix.
- Do not change migrations unless a concrete regression proves the schema must change.

**Interfaces:**
- Consumes:
  - The complete implementation and documentation from Tasks 1-6.
- Produces:
  - Exact verification evidence and an independently reviewed branch.

- [ ] **Step 1: Run the complete deterministic suite**

Run:

```sh
make fmt
make test
make test-race
make staticcheck
make build
make modules-check
git diff --check
```

Expected: PASS.

- [ ] **Step 2: Run local migration and PostgreSQL-gated acceptance**

Use the ignored existing `.env` through repository commands. Never print its
values:

```sh
make db-up
make db-migrate
make db-status
make test-integration
```

Expected:

- all embedded forward migrations remain applied;
- migration status reports no unexpected pending scope;
- every PostgreSQL-gated integration test passes; and
- no Google or model provider request occurs.

- [ ] **Step 3: Review the dependency and privacy boundary**

Run:

```sh
go list -m all | rg 'spf13|yaml|fsnotify|mapstructure|afero|cast'
go mod graph | rg 'spf13/viper'
rg -n 'github.com/spf13/viper' --glob '*.go' .
rg -n 'AutomaticEnv|WatchConfig|AddConfigPath|SetConfigName|remote' internal/config
git diff --check
```

Expected:

- Viper imports occur only in `internal/config`;
- prohibited Viper APIs have no matches in implementation code;
- examples and tests contain synthetic placeholders only; and
- no secrets or private source contents appear in the diff.

- [ ] **Step 4: Run an independent whole-branch review**

Give a fresh reviewer the approved spec, this plan, `git diff origin/main...HEAD`,
the exact verification output, and these review questions:

1. Can any syntax/help path read config or construct a live dependency?
2. Can any secret or temporary analyze input be supplied from a file?
3. Can unknown, duplicate, null, or mistyped values survive structural parsing?
4. Does precedence match flags, non-empty environment, file, defaults?
5. Does normal auth validate its selected target before observability?
6. Does offline validation avoid every live boundary?
7. Are Viper and Cobra confined to their intended packages?
8. Are cancellation and joined shutdown errors still discoverable?

Address every Critical or Important finding test-first, rerun the affected
focused tests, then rerun the complete deterministic and PostgreSQL gates.

- [ ] **Step 5: Record final branch evidence**

Run:

```sh
git status --short --branch
git log --oneline origin/main..HEAD
git diff --stat origin/main...HEAD
```

Expected: a clean feature branch containing the approved design/plan and
coherent implementation commits. Do not push or open a PR in this task.

## Final Delivery Boundary

After all tasks and the independent review pass:

- report the branch name and commit list;
- report every exact command and result;
- distinguish deterministic/local PostgreSQL acceptance from unrun Google
  Drive, Workspace Directory, Bedrock, Anthropic, OpenAI, quota,
  private-corpus, and model-quality acceptance;
- do not push, open a PR, merge, deploy, enable cloud logging, or invoke a model
  provider without explicit user authorization.
