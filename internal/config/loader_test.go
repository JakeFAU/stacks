package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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

func TestLoadQueryUsesDefaultsAndInclusiveEnvironmentBounds(t *testing.T) {
	clearConfigurationEnvironment(t)

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.Query != (QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000}) {
		t.Fatalf("Query = %#v, want named defaults", settings.Query)
	}

	for _, testCase := range []struct {
		name     string
		variable string
		value    string
		assert   func(QuerySettings) bool
	}{
		{name: "minimum entities", variable: QueryMaxEntitiesEnvironmentVariable, value: "1", assert: func(settings QuerySettings) bool { return settings.MaxEntities == 1 }},
		{name: "maximum entities", variable: QueryMaxEntitiesEnvironmentVariable, value: "64", assert: func(settings QuerySettings) bool { return settings.MaxEntities == 64 }},
		{name: "minimum predicates", variable: QueryMaxPredicatesEnvironmentVariable, value: "1", assert: func(settings QuerySettings) bool { return settings.MaxPredicates == 1 }},
		{name: "maximum predicates", variable: QueryMaxPredicatesEnvironmentVariable, value: "256", assert: func(settings QuerySettings) bool { return settings.MaxPredicates == 256 }},
		{name: "minimum chronology", variable: QueryMaxChronologyEnvironmentVariable, value: "1", assert: func(settings QuerySettings) bool { return settings.MaxChronology == 1 }},
		{name: "maximum chronology", variable: QueryMaxChronologyEnvironmentVariable, value: "10000", assert: func(settings QuerySettings) bool { return settings.MaxChronology == 10000 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clearConfigurationEnvironment(t)
			t.Setenv(testCase.variable, testCase.value)

			settings, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !testCase.assert(settings.Query) {
				t.Fatalf("Query = %#v, want %s accepted", settings.Query, testCase.name)
			}
		})
	}
}

func TestLoadWithOptionsAppliesQueryEnvironmentOverFileOverDefaults(t *testing.T) {
	clearConfigurationEnvironment(t)
	path := writeConfigFixture(t, ".yaml", "query:\n  max_entities: 2\n  max_predicates: 34\n  max_chronology: 999\n")
	t.Setenv(QueryMaxEntitiesEnvironmentVariable, "3")

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	want := QuerySettings{MaxEntities: 3, MaxPredicates: 34, MaxChronology: 999}
	if settings.Query != want {
		t.Fatalf("Query = %#v, want %#v from environment over file", settings.Query, want)
	}
}

func TestLoadWithOptionsKeepsQueryLoadsIndependent(t *testing.T) {
	clearConfigurationEnvironment(t)
	path := writeConfigFixture(t, ".json", `{"query":{"max_entities":4,"max_predicates":40,"max_chronology":400}}`)

	if _, err := LoadWithOptions(LoadOptions{ConfigFile: &path}); err != nil {
		t.Fatalf("first LoadWithOptions() error = %v", err)
	}
	settings, err := LoadWithOptions(LoadOptions{})
	if err != nil {
		t.Fatalf("second LoadWithOptions() error = %v", err)
	}
	if settings.Query != (QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000}) {
		t.Fatalf("second Query = %#v, want defaults without prior file", settings.Query)
	}
}

func TestLoadRejectsInvalidQueryLimitsWithoutDisclosingValues(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		variable string
		value    string
		private  bool
	}{
		{name: "entities below minimum", variable: QueryMaxEntitiesEnvironmentVariable, value: "0"},
		{name: "entities above maximum", variable: QueryMaxEntitiesEnvironmentVariable, value: "65"},
		{name: "predicates below minimum", variable: QueryMaxPredicatesEnvironmentVariable, value: "0"},
		{name: "predicates above maximum", variable: QueryMaxPredicatesEnvironmentVariable, value: "257"},
		{name: "chronology below minimum", variable: QueryMaxChronologyEnvironmentVariable, value: "0"},
		{name: "chronology above maximum", variable: QueryMaxChronologyEnvironmentVariable, value: "10001"},
		{name: "not an integer", variable: QueryMaxChronologyEnvironmentVariable, value: "private-query-limit", private: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clearConfigurationEnvironment(t)
			t.Setenv(testCase.variable, testCase.value)

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), testCase.variable) {
				t.Fatalf("Load() error = %v, want bounded %s rejection", err, testCase.variable)
			}
			if testCase.private && strings.Contains(err.Error(), testCase.value) {
				t.Fatalf("Load() error exposed configured query limit: %v", err)
			}
		})
	}
}

func TestLoadWithOptionsAppliesJSONFileValues(t *testing.T) {
	clearConfigurationEnvironment(t)
	path := writeConfigFixture(t, ".json", `{
  "http": {"host": "192.0.2.20", "port": 8182},
  "log": {"level": "error"},
  "model": {"max_attempts": 3}
}`)

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if settings.HTTPAddress != "192.0.2.20:8182" {
		t.Fatalf("HTTPAddress = %q, want JSON file value", settings.HTTPAddress)
	}
	if settings.LogLevel != "error" {
		t.Fatalf("LogLevel = %q, want error from JSON file", settings.LogLevel)
	}
	if settings.Application.Model.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", settings.Application.Model.MaxAttempts)
	}
}

func TestLoadWithOptionsKeepsLoadsIndependent(t *testing.T) {
	clearConfigurationEnvironment(t)
	firstPath := writeConfigFixture(t, ".yaml", "http:\n  host: 192.0.2.30\n")
	t.Setenv(HTTPPortEnvironmentVariable, "9193")

	if _, err := LoadWithOptions(LoadOptions{ConfigFile: &firstPath}); err != nil {
		t.Fatalf("first LoadWithOptions() error = %v", err)
	}
	t.Setenv(HTTPPortEnvironmentVariable, "")

	settings, err := LoadWithOptions(LoadOptions{})
	if err != nil {
		t.Fatalf("second LoadWithOptions() error = %v", err)
	}
	if settings.HTTPAddress != "127.0.0.1:8080" {
		t.Fatalf("second HTTPAddress = %q, want defaults without prior file or environment", settings.HTTPAddress)
	}
}

func TestLoadWithOptionsUsesIndependentBindingMetadata(t *testing.T) {
	first := configurationEnvironmentBindings()
	if len(first) == 0 {
		t.Fatal("configurationEnvironmentBindings() returned no bindings")
	}
	first[0] = environmentBinding{}

	second := configurationEnvironmentBindings()
	if len(second) == 0 || second[0] != (environmentBinding{key: configKeyHTTPHost, name: HTTPHostEnvironmentVariable}) {
		t.Fatal("configurationEnvironmentBindings() shared mutable binding metadata")
	}
}

func TestLoadDoesNotBindRetiredAnalysisEnvironmentInputs(t *testing.T) {
	clearConfigurationEnvironment(t)
	baseline, err := Load()
	if err != nil {
		t.Fatalf("baseline Load() error = %v", err)
	}

	retired := []string{
		strings.ReplaceAll("STACKS_ANALXYSIS_PROMPT_VERSION", "X", ""),
		strings.ReplaceAll("STACKXS_EMPLOYEE_ENTITY_ID", "X", ""),
		strings.ReplaceAll("STACKXS_MANAGER_ENTITY_ID", "X", ""),
	}
	for _, binding := range configurationEnvironmentBindings() {
		for _, name := range retired {
			if binding.name == name {
				t.Fatalf("configuration binding retains retired environment name %q", name)
			}
		}
	}
	for _, name := range retired {
		t.Setenv(name, "synthetic-retired-value")
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() read retired environment input: %v", err)
	}
	if !reflect.DeepEqual(got, baseline) {
		t.Fatal("retired analysis environment inputs changed loaded settings")
	}
}

func TestLoadWithOptionsUsesDefaultsForEmptyDefaultedFileStrings(t *testing.T) {
	clearConfigurationEnvironment(t)
	path := writeConfigFixture(t, ".yaml", `
http:
  host: ""
log:
  level: ""
telemetry:
  endpoint: ""
  service_name: ""
database:
  application_role: ""
extraction:
  prompt_version: ""
`)

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if settings.HTTPAddress != "127.0.0.1:8080" || settings.LogLevel != defaultLogLevel ||
		settings.Telemetry.Endpoint != defaultOTelEndpoint || settings.Telemetry.ServiceName != defaultOTelServiceName ||
		settings.Database.ApplicationRole != defaultDatabaseAppRole ||
		settings.Application.ExtractionPromptVersion != defaultExtractionPromptVersion {
		t.Fatal("empty defaulted file strings did not select their named defaults")
	}
}

func TestLoadWithOptionsKeepsEmptyRequiredAndOptionalFileStringsUnset(t *testing.T) {
	clearConfigurationEnvironment(t)
	path := writeConfigFixture(t, ".json", `{
  "google": {"folder_id": "", "oauth_client_file": ""},
  "model": {"provider": "", "max_output_tokens": 10}
}`)

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if settings.Application.GoogleFolderID != "" || settings.Application.GoogleOAuthClientFile != "" ||
		settings.Application.Model.Provider != "" || settings.Application.Model.MaxOutputTokens != 10 {
		t.Fatal("empty required or optional file strings were not preserved as unset")
	}
}

func TestLoadWithOptionsPreservesFileStringWhitespace(t *testing.T) {
	clearConfigurationEnvironment(t)
	path := writeConfigFixture(t, ".yaml", "model:\n  id: ' model with spaces '\n")

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if settings.Application.Model.ModelID != " model with spaces " {
		t.Fatalf("ModelID = %q, want unchanged surrounding whitespace", settings.Application.Model.ModelID)
	}
}

func TestLoadWithOptionsNormalizesEnvironmentListsAndPreservesFileArrayItems(t *testing.T) {
	clearConfigurationEnvironment(t)
	t.Setenv(TranscriptTitlesEnvironmentVariable, "Transcript,  Meeting transcript,  ")
	path := writeConfigFixture(t, ".json", `{
  "google": {"notes_titles": ["Meeting, notes", "  Notes with spaces  "]}
}`)

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if !reflect.DeepEqual(settings.Application.TranscriptTitles, []string{"Transcript", "Meeting transcript"}) {
		t.Fatalf("TranscriptTitles = %#v, want normalized environment CSV", settings.Application.TranscriptTitles)
	}
	if !reflect.DeepEqual(settings.Application.NotesTitles, []string{"Meeting, notes", "  Notes with spaces  "}) {
		t.Fatalf("NotesTitles = %#v, want intact file array items", settings.Application.NotesTitles)
	}
}

func TestLoadWithOptionsRejectsInvalidFileValues(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wantName string
	}{
		{name: "port", contents: "http:\n  port: 0\n", wantName: HTTPPortEnvironmentVariable},
		{name: "read timeout", contents: "http:\n  read_header_timeout_seconds: 0\n", wantName: ReadHeaderTimeoutEnvironmentVariable},
		{name: "log level", contents: "log:\n  level: verbose\n", wantName: LogLevelEnvironmentVariable},
		{name: "sampling", contents: "telemetry:\n  trace_sample_ratio: 1.1\n", wantName: OTelTraceSampleRatioEnvironmentVariable},
		{name: "scopes", contents: "database:\n  scopes: [directory]\n", wantName: DatabaseScopesEnvironmentVariable},
		{name: "directory attempts", contents: "directory:\n  max_attempts: 4\n", wantName: GoogleDirectoryMaxAttemptsEnvironmentVariable},
		{name: "lease maximum", contents: "ingestion:\n  lease_duration: 2h\n", wantName: IngestionLeaseDurationEnvironmentVariable},
		{name: "lease margin", contents: "ingestion:\n  lease_duration: 1m\n  attempt_timeout: 1m\n", wantName: IngestionAttemptTimeoutEnvironmentVariable},
		{name: "model attempts", contents: "model:\n  max_attempts: 0\n", wantName: ModelMaxAttemptsEnvironmentVariable},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			clearConfigurationEnvironment(t)
			path := writeConfigFixture(t, ".yaml", testCase.contents)

			_, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
			if err == nil || !strings.Contains(err.Error(), testCase.wantName) {
				t.Fatalf("LoadWithOptions() error = %v, want bounded %s rejection", err, testCase.wantName)
			}
		})
	}
}

func TestLoadWithOptionsKeepsCredentialsEnvironmentOnly(t *testing.T) {
	clearConfigurationEnvironment(t)
	const databaseSecret = "postgres://synthetic-secret@localhost/stacks"
	const migrationDatabaseSecret = "postgres://synthetic-admin-secret@localhost/stacks"
	const providerSecret = "synthetic-provider-secret"
	const anthropicSecret = "synthetic-anthropic-secret"
	t.Setenv(DatabaseURLEnvironmentVariable, databaseSecret)
	t.Setenv(MigrationDatabaseURLEnvironmentVariable, migrationDatabaseSecret)
	t.Setenv(OpenAIAPIKeyEnvironmentVariable, providerSecret)
	t.Setenv(AnthropicAPIKeyEnvironmentVariable, anthropicSecret)
	path := writeConfigFixture(t, ".json", `{"model":{"provider":"openai"}}`)

	settings, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if settings.Database.URL != databaseSecret || settings.Database.MigrationURL != migrationDatabaseSecret ||
		settings.Application.Model.OpenAIAPIKey != providerSecret || settings.Application.Model.AnthropicAPIKey != anthropicSecret {
		t.Fatal("environment-only credentials were not retained in memory")
	}
}

func TestLoadWithOptionsRejectsCredentialKeysWithoutDisclosingValues(t *testing.T) {
	clearConfigurationEnvironment(t)
	const syntheticSecret = "synthetic-file-secret"
	path := writeConfigFixture(t, ".yaml", "database:\n  url: "+syntheticSecret+"\n")

	_, err := LoadWithOptions(LoadOptions{ConfigFile: &path})
	if err == nil || !strings.Contains(err.Error(), "database.url") {
		t.Fatalf("LoadWithOptions() error = %v, want database credential key rejection", err)
	}
	if strings.Contains(err.Error(), syntheticSecret) {
		t.Fatalf("LoadWithOptions() error disclosed a synthetic credential: %v", err)
	}
}

func writeConfigFixture(t *testing.T, extension, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stacks"+extension)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func clearConfigurationEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		HTTPHostEnvironmentVariable,
		HTTPPortEnvironmentVariable,
		ReadHeaderTimeoutEnvironmentVariable,
		LogLevelEnvironmentVariable,
		OTelEnabledEnvironmentVariable,
		OTelEndpointEnvironmentVariable,
		OTelInsecureEnvironmentVariable,
		OTelMetricIntervalEnvironmentVariable,
		OTelServiceNameEnvironmentVariable,
		OTelTraceSampleRatioEnvironmentVariable,
		DatabaseURLEnvironmentVariable,
		MigrationDatabaseURLEnvironmentVariable,
		DatabaseScopesEnvironmentVariable,
		DatabaseAppRoleEnvironmentVariable,
		GoogleFolderIDEnvironmentVariable,
		GoogleOAuthClientFileEnvironmentVariable,
		GoogleOAuthTokenFileEnvironmentVariable,
		TranscriptTitlesEnvironmentVariable,
		NotesTitlesEnvironmentVariable,
		GoogleDirectoryEnabledEnvironmentVariable,
		GoogleDirectoryClientFileEnvironmentVariable,
		GoogleDirectoryTokenFileEnvironmentVariable,
		GoogleDirectoryDomainsEnvironmentVariable,
		GoogleDirectoryFreshnessEnvironmentVariable,
		GoogleDirectoryRetryAfterEnvironmentVariable,
		GoogleDirectoryMaxAttemptsEnvironmentVariable,
		DataModeEnvironmentVariable,
		ModelProviderEnvironmentVariable,
		ModelIDEnvironmentVariable,
		ModelMaxTokensEnvironmentVariable,
		ModelMaxAttemptsEnvironmentVariable,
		AWSProfileEnvironmentVariable,
		AWSRegionEnvironmentVariable,
		OpenAIAPIKeyEnvironmentVariable,
		AnthropicAPIKeyEnvironmentVariable,
		IngestionLeaseDurationEnvironmentVariable,
		IngestionAttemptTimeoutEnvironmentVariable,
		ExtractionPromptVersionEnvironmentVariable,
		QueryMaxEntitiesEnvironmentVariable,
		QueryMaxPredicatesEnvironmentVariable,
		QueryMaxChronologyEnvironmentVariable,
		strings.ReplaceAll("STACKS_ANALXYSIS_PROMPT_VERSION", "X", ""),
		strings.ReplaceAll("STACKXS_EMPLOYEE_ENTITY_ID", "X", ""),
		strings.ReplaceAll("STACKXS_MANAGER_ENTITY_ID", "X", ""),
	} {
		t.Setenv(name, "")
	}
	for _, name := range unsupportedModelEnvironmentNames {
		t.Setenv(name, "")
	}
}
