package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv(HTTPHostEnvironmentVariable, "")
	t.Setenv(HTTPPortEnvironmentVariable, "")
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "")
	clearObservabilityEnvironment(t)
	clearModelEnvironment(t)
	t.Setenv(IngestionLeaseDurationEnvironmentVariable, "")
	t.Setenv(IngestionAttemptTimeoutEnvironmentVariable, "")
	clearGoogleDirectoryEnvironment(t)

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if settings.HTTPAddress != "127.0.0.1:8080" {
		t.Errorf("HTTPAddress = %q, want %q", settings.HTTPAddress, "127.0.0.1:8080")
	}
	if settings.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want %v", settings.ReadHeaderTimeout, 5*time.Second)
	}
	if settings.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", settings.LogLevel, "info")
	}
	if settings.Telemetry.Enabled {
		t.Error("Telemetry.Enabled = true, want false")
	}
	if settings.Telemetry.Endpoint != "127.0.0.1:4317" {
		t.Errorf("Telemetry.Endpoint = %q, want %q", settings.Telemetry.Endpoint, "127.0.0.1:4317")
	}
	if !settings.Telemetry.Insecure {
		t.Error("Telemetry.Insecure = false, want true")
	}
	if settings.Telemetry.MetricExportInterval != 10*time.Second {
		t.Errorf("Telemetry.MetricExportInterval = %v, want %v", settings.Telemetry.MetricExportInterval, 10*time.Second)
	}
	if settings.Telemetry.ServiceName != "stacks" {
		t.Errorf("Telemetry.ServiceName = %q, want %q", settings.Telemetry.ServiceName, "stacks")
	}
	if settings.Telemetry.TraceSampleRatio != 1 {
		t.Errorf("Telemetry.TraceSampleRatio = %v, want 1", settings.Telemetry.TraceSampleRatio)
	}
	if settings.Application.Model.MaxAttempts != defaultModelMaxAttempts {
		t.Errorf("Application.Model.MaxAttempts = %d, want %d", settings.Application.Model.MaxAttempts, defaultModelMaxAttempts)
	}
	if settings.Application.Model.DataMode != "" || settings.Application.Model.Provider != "" || settings.Application.Model.ModelID != "" {
		t.Error("Application.Model selected a data mode, provider, or model ID without explicit configuration")
	}
	if settings.Application.IngestionLeaseDuration != defaultIngestionLeaseDuration {
		t.Errorf("Application.IngestionLeaseDuration = %v, want %v", settings.Application.IngestionLeaseDuration, defaultIngestionLeaseDuration)
	}
	if settings.Application.IngestionAttemptTimeout != defaultIngestionAttemptTimeout {
		t.Errorf("Application.IngestionAttemptTimeout = %v, want %v", settings.Application.IngestionAttemptTimeout, defaultIngestionAttemptTimeout)
	}
	if settings.Application.ExtractionPromptVersion != defaultExtractionPromptVersion {
		t.Errorf("Application.ExtractionPromptVersion = %q, want %q", settings.Application.ExtractionPromptVersion, defaultExtractionPromptVersion)
	}
}

func TestSettingsValidateQueryRequiresOnlyDatabaseAndBoundedQuerySettings(t *testing.T) {
	settings := Settings{
		Database: DatabaseSettings{
			URL:    "postgres://app:synthetic@127.0.0.1/stacks",
			Scopes: []DatabaseScope{DatabaseScopeCore},
		},
		Query: QuerySettings{
			MaxEntities:   16,
			MaxPredicates: 32,
			MaxChronology: 1000,
		},
		Application: ApplicationSettings{Directory: GoogleDirectorySettings{Enabled: true}},
	}
	if err := settings.Validate(CommandQuery); err != nil {
		t.Fatalf("Settings.Validate(query) error = %v, want only database URL and query settings required", err)
	}
}

func TestSettingsValidateQueryRejectsOutOfBoundsLimitsWithoutValues(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*QuerySettings)
		wantName  string
	}{
		{name: "entities below minimum", configure: func(settings *QuerySettings) { settings.MaxEntities = 0 }, wantName: QueryMaxEntitiesEnvironmentVariable},
		{name: "entities above maximum", configure: func(settings *QuerySettings) { settings.MaxEntities = 65 }, wantName: QueryMaxEntitiesEnvironmentVariable},
		{name: "predicates below minimum", configure: func(settings *QuerySettings) { settings.MaxPredicates = 0 }, wantName: QueryMaxPredicatesEnvironmentVariable},
		{name: "predicates above maximum", configure: func(settings *QuerySettings) { settings.MaxPredicates = 257 }, wantName: QueryMaxPredicatesEnvironmentVariable},
		{name: "chronology below minimum", configure: func(settings *QuerySettings) { settings.MaxChronology = 0 }, wantName: QueryMaxChronologyEnvironmentVariable},
		{name: "chronology above maximum", configure: func(settings *QuerySettings) { settings.MaxChronology = 10001 }, wantName: QueryMaxChronologyEnvironmentVariable},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			settings := Settings{
				Database: DatabaseSettings{URL: "postgres://app:synthetic@127.0.0.1/stacks", Scopes: []DatabaseScope{DatabaseScopeCore}},
				Query:    QuerySettings{MaxEntities: 16, MaxPredicates: 32, MaxChronology: 1000},
			}
			testCase.configure(&settings.Query)

			err := settings.Validate(CommandQuery)
			if err == nil || !strings.Contains(err.Error(), testCase.wantName) {
				t.Fatalf("Settings.Validate(query) error = %v, want bounded %s rejection", err, testCase.wantName)
			}
			if strings.Contains(err.Error(), "10001") || strings.Contains(err.Error(), "257") || strings.Contains(err.Error(), "65") {
				t.Fatalf("Settings.Validate(query) error exposed configured limit: %v", err)
			}
		})
	}
}

func validGoogleDirectorySettings() GoogleDirectorySettings {
	return GoogleDirectorySettings{
		Enabled: true, OAuthClientFile: "/synthetic/directory-client.json", OAuthTokenFile: "/synthetic/directory-token.json",
		EmailDomains: []string{"corp.example"}, Freshness: 24 * time.Hour, RetryAfter: 15 * time.Minute, MaxAttempts: 3,
	}
}

func clearGoogleDirectoryEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		GoogleDirectoryEnabledEnvironmentVariable,
		GoogleDirectoryClientFileEnvironmentVariable,
		GoogleDirectoryTokenFileEnvironmentVariable,
		GoogleDirectoryDomainsEnvironmentVariable,
		GoogleDirectoryFreshnessEnvironmentVariable,
		GoogleDirectoryRetryAfterEnvironmentVariable,
		GoogleDirectoryMaxAttemptsEnvironmentVariable,
	} {
		t.Setenv(name, "")
	}
}

func TestGoogleDirectoryDefaultsDisabled(t *testing.T) {
	clearGoogleDirectoryEnvironment(t)

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.Application.Directory.Enabled {
		t.Fatal("directory integration is enabled by default")
	}
	if settings.Application.Directory.OAuthClientFile != "" || settings.Application.Directory.OAuthTokenFile != "" || len(settings.Application.Directory.EmailDomains) != 0 {
		t.Fatalf("disabled directory settings = %#v, want zero configuration", settings.Application.Directory)
	}
	if settings.Application.Directory.Freshness != defaultGoogleDirectoryFreshness || settings.Application.Directory.RetryAfter != defaultGoogleDirectoryRetryAfter || settings.Application.Directory.MaxAttempts != defaultGoogleDirectoryMaxAttempts {
		t.Fatalf("directory defaults = %#v, want named defaults", settings.Application.Directory)
	}
}

func TestLoadReadsGoogleDirectorySettings(t *testing.T) {
	clearGoogleDirectoryEnvironment(t)
	t.Setenv(GoogleDirectoryEnabledEnvironmentVariable, "true")
	t.Setenv(GoogleDirectoryClientFileEnvironmentVariable, "/synthetic/directory-client.json")
	t.Setenv(GoogleDirectoryTokenFileEnvironmentVariable, "/synthetic/directory-token.json")
	t.Setenv(GoogleDirectoryDomainsEnvironmentVariable, "corp.example,  ,division.example")
	t.Setenv(GoogleDirectoryFreshnessEnvironmentVariable, "36h")
	t.Setenv(GoogleDirectoryRetryAfterEnvironmentVariable, "20m")
	t.Setenv(GoogleDirectoryMaxAttemptsEnvironmentVariable, "2")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := GoogleDirectorySettings{
		Enabled: true, OAuthClientFile: "/synthetic/directory-client.json", OAuthTokenFile: "/synthetic/directory-token.json",
		EmailDomains: []string{"corp.example", "division.example"}, Freshness: 36 * time.Hour, RetryAfter: 20 * time.Minute, MaxAttempts: 2,
	}
	if !reflect.DeepEqual(settings.Application.Directory, want) {
		t.Fatalf("directory settings = %#v, want %#v", settings.Application.Directory, want)
	}
}

func TestGoogleDirectoryEnabledValidationRejectsInvalidSettings(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		invalidate func(*GoogleDirectorySettings)
	}{
		{name: "missing client path", invalidate: func(settings *GoogleDirectorySettings) { settings.OAuthClientFile = "" }},
		{name: "padded client path", invalidate: func(settings *GoogleDirectorySettings) { settings.OAuthClientFile = " /synthetic/client.json " }},
		{name: "missing token path", invalidate: func(settings *GoogleDirectorySettings) { settings.OAuthTokenFile = "" }},
		{name: "padded token path", invalidate: func(settings *GoogleDirectorySettings) { settings.OAuthTokenFile = " /synthetic/token.json " }},
		{name: "missing domains", invalidate: func(settings *GoogleDirectorySettings) { settings.EmailDomains = nil }},
		{name: "invalid domain", invalidate: func(settings *GoogleDirectorySettings) { settings.EmailDomains = []string{"invalid domain"} }},
		{name: "nonpositive freshness", invalidate: func(settings *GoogleDirectorySettings) { settings.Freshness = 0 }},
		{name: "nonpositive retry after", invalidate: func(settings *GoogleDirectorySettings) { settings.RetryAfter = 0 }},
		{name: "too few attempts", invalidate: func(settings *GoogleDirectorySettings) { settings.MaxAttempts = 0 }},
		{name: "too many attempts", invalidate: func(settings *GoogleDirectorySettings) { settings.MaxAttempts = 4 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings := validApplicationSettings()
			settings.Directory = validGoogleDirectorySettings()
			testCase.invalidate(&settings.Directory)
			for _, command := range []Command{CommandSync, CommandDoctor, CommandReview} {
				if err := settings.Validate(command); err == nil {
					t.Fatalf("Validate(%s) error = nil, want directory setting rejection", command)
				}
			}
		})
	}
}

func TestGoogleDirectoryEnabledValidationDoesNotExposeInvalidDomain(t *testing.T) {
	const privateDomain = "private-name@example.test"
	settings := validApplicationSettings()
	settings.Directory = validGoogleDirectorySettings()
	settings.Directory.EmailDomains = []string{privateDomain}

	err := settings.Validate(CommandSync)
	if err == nil {
		t.Fatal("Validate(sync) error = nil, want invalid domain rejection")
	}
	if !strings.Contains(err.Error(), GoogleDirectoryDomainsEnvironmentVariable) {
		t.Fatalf("Validate(sync) error = %v, want bounded configuration variable", err)
	}
	if strings.Contains(err.Error(), privateDomain) || strings.Contains(err.Error(), "invalid directory approved domain") {
		t.Fatalf("Validate(sync) error exposed private or lower-level detail: %v", err)
	}
}

func TestApplicationSettingsValidateGoogleAuthSeparatesDriveAndDirectoryPaths(t *testing.T) {
	settings := ApplicationSettings{}
	if err := settings.Validate(CommandAuth); err != nil {
		t.Fatalf("Validate(auth) error = %v, want target-specific validation", err)
	}
	if err := settings.ValidateGoogleAuth(GoogleAuthDrive); err == nil {
		t.Fatal("ValidateGoogleAuth(google) error = nil, want Drive path requirement")
	}
	if err := settings.ValidateGoogleAuth(GoogleAuthDirectory); err == nil {
		t.Fatal("ValidateGoogleAuth(google-directory) error = nil, want directory path requirement")
	}
	settings.GoogleOAuthClientFile = "/synthetic/drive-client.json"
	settings.GoogleOAuthTokenFile = "/synthetic/drive-token.json"
	settings.Directory.OAuthClientFile = "/synthetic/directory-client.json"
	settings.Directory.OAuthTokenFile = "/synthetic/directory-token.json"
	if err := settings.ValidateGoogleAuth(GoogleAuthDrive); err != nil {
		t.Fatalf("ValidateGoogleAuth(google) error = %v", err)
	}
	if err := settings.ValidateGoogleAuth(GoogleAuthDirectory); err != nil {
		t.Fatalf("ValidateGoogleAuth(google-directory) error = %v", err)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	t.Setenv(HTTPHostEnvironmentVariable, "0.0.0.0")
	t.Setenv(HTTPPortEnvironmentVariable, "9090")
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "7")
	t.Setenv(LogLevelEnvironmentVariable, "debug")
	t.Setenv(OTelEnabledEnvironmentVariable, "true")
	t.Setenv(OTelEndpointEnvironmentVariable, "collector:4317")
	t.Setenv(OTelInsecureEnvironmentVariable, "false")
	t.Setenv(OTelMetricIntervalEnvironmentVariable, "3s")
	t.Setenv(OTelServiceNameEnvironmentVariable, "stacks-test")
	t.Setenv(OTelTraceSampleRatioEnvironmentVariable, "0.25")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if settings.HTTPAddress != "0.0.0.0:9090" {
		t.Errorf("HTTPAddress = %q, want %q", settings.HTTPAddress, "0.0.0.0:9090")
	}
	if settings.ReadHeaderTimeout != 7*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want %v", settings.ReadHeaderTimeout, 7*time.Second)
	}
	if settings.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", settings.LogLevel, "debug")
	}
	if !settings.Telemetry.Enabled {
		t.Error("Telemetry.Enabled = false, want true")
	}
	if settings.Telemetry.Endpoint != "collector:4317" {
		t.Errorf("Telemetry.Endpoint = %q, want %q", settings.Telemetry.Endpoint, "collector:4317")
	}
	if settings.Telemetry.Insecure {
		t.Error("Telemetry.Insecure = true, want false")
	}
	if settings.Telemetry.MetricExportInterval != 3*time.Second {
		t.Errorf("Telemetry.MetricExportInterval = %v, want %v", settings.Telemetry.MetricExportInterval, 3*time.Second)
	}
	if settings.Telemetry.ServiceName != "stacks-test" {
		t.Errorf("Telemetry.ServiceName = %q, want %q", settings.Telemetry.ServiceName, "stacks-test")
	}
	if settings.Telemetry.TraceSampleRatio != 0.25 {
		t.Errorf("Telemetry.TraceSampleRatio = %v, want 0.25", settings.Telemetry.TraceSampleRatio)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv(HTTPPortEnvironmentVariable, "70000")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid port error")
	}
}

func TestLoadRejectsInvalidTimeout(t *testing.T) {
	t.Setenv(ReadHeaderTimeoutEnvironmentVariable, "never")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid timeout error")
	}
}

func TestLoadRejectsInvalidObservabilitySettings(t *testing.T) {
	tests := []struct {
		name     string
		variable string
		value    string
	}{
		{name: "log level", variable: LogLevelEnvironmentVariable, value: "verbose"},
		{name: "enabled", variable: OTelEnabledEnvironmentVariable, value: "sometimes"},
		{name: "insecure", variable: OTelInsecureEnvironmentVariable, value: "perhaps"},
		{name: "metric interval", variable: OTelMetricIntervalEnvironmentVariable, value: "soon"},
		{name: "negative metric interval", variable: OTelMetricIntervalEnvironmentVariable, value: "-1s"},
		{name: "sample ratio", variable: OTelTraceSampleRatioEnvironmentVariable, value: "1.1"},
		{name: "NaN sample ratio", variable: OTelTraceSampleRatioEnvironmentVariable, value: "NaN"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearObservabilityEnvironment(t)
			t.Setenv(test.variable, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
		})
	}
}

func TestLoadReadsApplicationSettings(t *testing.T) {
	t.Setenv(DatabaseURLEnvironmentVariable, "postgres://stacks:test@localhost:5432/stacks")
	t.Setenv(GoogleFolderIDEnvironmentVariable, "folder-id")
	t.Setenv(GoogleOAuthClientFileEnvironmentVariable, "/tmp/client.json")
	t.Setenv(GoogleOAuthTokenFileEnvironmentVariable, "/tmp/token.json")
	t.Setenv(TranscriptTitlesEnvironmentVariable, "Transcript, Meeting transcript")
	t.Setenv(NotesTitlesEnvironmentVariable, "Meeting notes")
	t.Setenv(AWSProfileEnvironmentVariable, "stacks")
	t.Setenv(AWSRegionEnvironmentVariable, "us-east-1")
	t.Setenv(DataModeEnvironmentVariable, "personal")
	t.Setenv(ModelProviderEnvironmentVariable, "bedrock")
	t.Setenv(ModelIDEnvironmentVariable, "model-id")
	t.Setenv(ModelMaxTokensEnvironmentVariable, "1000")
	t.Setenv(ModelMaxAttemptsEnvironmentVariable, "3")
	t.Setenv(IngestionLeaseDurationEnvironmentVariable, "2m")
	t.Setenv(IngestionAttemptTimeoutEnvironmentVariable, "90s")
	t.Setenv(ExtractionPromptVersionEnvironmentVariable, "extract-test-v1")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := validApplicationSettings()
	want.Model.MaxAttempts = 3
	want.IngestionLeaseDuration = 2 * time.Minute
	want.IngestionAttemptTimeout = 90 * time.Second
	want.ExtractionPromptVersion = "extract-test-v1"
	want.TranscriptTitles = []string{"Transcript", "Meeting transcript"}
	if diff := diffApplicationSettings(settings.Application, want); diff != "" {
		t.Error(diff)
	}
}

func TestApplicationSettingsValidateSyncRequiresCorpusAndDisclosureSettings(t *testing.T) {
	tests := []struct {
		name       string
		invalidate func(*ApplicationSettings)
	}{
		{name: "Google folder ID", invalidate: func(settings *ApplicationSettings) { settings.GoogleFolderID = "" }},
		{name: "Google OAuth client file", invalidate: func(settings *ApplicationSettings) { settings.GoogleOAuthClientFile = "" }},
		{name: "Google OAuth token file", invalidate: func(settings *ApplicationSettings) { settings.GoogleOAuthTokenFile = "" }},
		{name: "transcript titles", invalidate: func(settings *ApplicationSettings) { settings.TranscriptTitles = nil }},
		{name: "notes titles", invalidate: func(settings *ApplicationSettings) { settings.NotesTitles = nil }},
		{name: "model data mode", invalidate: func(settings *ApplicationSettings) { settings.Model.DataMode = "" }},
		{name: "model provider", invalidate: func(settings *ApplicationSettings) { settings.Model.Provider = "" }},
		{name: "model ID", invalidate: func(settings *ApplicationSettings) { settings.Model.ModelID = "" }},
		{name: "model max tokens", invalidate: func(settings *ApplicationSettings) { settings.Model.MaxOutputTokens = 0 }},
		{name: "model max attempts", invalidate: func(settings *ApplicationSettings) { settings.Model.MaxAttempts = 0 }},
		{name: "extraction prompt version", invalidate: func(settings *ApplicationSettings) { settings.ExtractionPromptVersion = "" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := validApplicationSettings()
			test.invalidate(&settings)

			if err := settings.Validate(CommandSync); err == nil {
				t.Fatal("Validate() error = nil, want missing setting error")
			}
		})
	}
}

func TestApplicationSettingsValidateAllowsDefaultAWSCredentialChainForSync(t *testing.T) {
	settings := validApplicationSettings()
	settings.Model.AWSProfile = ""
	if err := settings.Validate(CommandSync); err != nil {
		t.Errorf("Validate(%q) error = %v, want optional AWS profile", CommandSync, err)
	}
}

func TestApplicationSettingsValidateRejectsSupersededPromptContractsWithUpgradeGuidance(t *testing.T) {
	settings := validApplicationSettings()
	settings.ExtractionPromptVersion = "extract-v1"

	err := settings.Validate(CommandSync)
	if err == nil {
		t.Fatal("Validate() error = nil, want superseded prompt rejection")
	}
	if !strings.Contains(err.Error(), ExtractionPromptVersionEnvironmentVariable) ||
		!strings.Contains(err.Error(), "extract-v2") ||
		!strings.Contains(err.Error(), "sync") {
		t.Fatalf("Validate() error = %q, want bounded config name, current version, and resync guidance", err)
	}
	if strings.Contains(err.Error(), "extract-v1") {
		t.Fatalf("Validate() error echoed unsupported user-controlled version: %v", err)
	}
}

func TestLoadRejectsInvalidOrUnboundedIngestionLeaseDuration(t *testing.T) {
	for _, value := range []string{"0s", "not-a-duration", "2h"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(IngestionLeaseDurationEnvironmentVariable, value)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want bounded positive lease duration rejection")
			}
		})
	}
}

func TestLoadRejectsAttemptTimeoutThatCanOutliveLease(t *testing.T) {
	tests := []struct {
		name    string
		lease   string
		attempt string
	}{
		{name: "equal", lease: "1m", attempt: "1m"},
		{name: "longer", lease: "1m", attempt: "2m"},
		{name: "no cleanup margin", lease: "1m", attempt: "58s"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(IngestionLeaseDurationEnvironmentVariable, testCase.lease)
			t.Setenv(IngestionAttemptTimeoutEnvironmentVariable, testCase.attempt)
			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want attempt deadline safely below lease")
			}
		})
	}
}

func TestApplicationSettingsValidateSyncRequiresAttemptTimeoutBelowLease(t *testing.T) {
	settings := validApplicationSettings()
	settings.IngestionAttemptTimeout = settings.IngestionLeaseDuration

	if err := settings.Validate(CommandSync); err == nil {
		t.Fatal("Validate(sync) error = nil, want attempt timeout below lease")
	}
}

func TestApplicationSettingsValidateSyncRejectsEmptyNormalizedTitleSet(t *testing.T) {
	settings := validApplicationSettings()
	settings.TranscriptTitles = []string{"  \t "}

	if err := settings.Validate(CommandSync); err == nil {
		t.Fatal("Validate() error = nil, want empty normalized title set error")
	}
}

func TestApplicationSettingsValidateRejectsWhitespaceOnlyRequiredSettings(t *testing.T) {
	tests := []struct {
		name       string
		command    Command
		invalidate func(*ApplicationSettings)
	}{
		{name: "Google folder ID", command: CommandSync, invalidate: func(settings *ApplicationSettings) { settings.GoogleFolderID = " \t " }},
		{name: "Google OAuth client file", command: CommandSync, invalidate: func(settings *ApplicationSettings) { settings.GoogleOAuthClientFile = " \t " }},
		{name: "Google OAuth token file", command: CommandSync, invalidate: func(settings *ApplicationSettings) { settings.GoogleOAuthTokenFile = " \t " }},
		{name: "AWS region", command: CommandSync, invalidate: func(settings *ApplicationSettings) { settings.Model.AWSRegion = " \t " }},
		{name: "model ID", command: CommandSync, invalidate: func(settings *ApplicationSettings) { settings.Model.ModelID = " \t " }},
		{name: "extraction prompt version", command: CommandSync, invalidate: func(settings *ApplicationSettings) { settings.ExtractionPromptVersion = " \t " }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := validApplicationSettings()
			test.invalidate(&settings)

			if err := settings.Validate(test.command); err == nil {
				t.Fatal("Validate() error = nil, want whitespace-only setting error")
			}
		})
	}
}

func TestApplicationSettingsValidateRejectsOverlappingNormalizedTabTitles(t *testing.T) {
	settings := validApplicationSettings()
	settings.TranscriptTitles = []string{"Transcript"}
	settings.NotesTitles = []string{"  transcript  "}

	err := settings.Validate(CommandSync)
	if err == nil {
		t.Fatal("Validate() error = nil, want overlapping titles error")
	}
}

func TestApplicationSettingsValidationDoesNotExposeOverlappingTitle(t *testing.T) {
	const secretLikeTitle = "Synthetic Secret Title Marker"
	settings := validApplicationSettings()
	settings.TranscriptTitles = []string{secretLikeTitle}
	settings.NotesTitles = []string{"  synthetic   secret title marker "}

	err := settings.Validate(CommandSync)
	if err == nil {
		t.Fatal("Validate(sync) error = nil, want overlapping titles rejection")
	}
	if !strings.Contains(err.Error(), TranscriptTitlesEnvironmentVariable) ||
		!strings.Contains(err.Error(), NotesTitlesEnvironmentVariable) {
		t.Fatalf("Validate(sync) error = %v, want bounded title setting names", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(secretLikeTitle)) {
		t.Fatalf("Validate(sync) error exposed loaded title: %v", err)
	}
}

func TestApplicationSettingsValidateDoctorAllowsDefaultAWSCredentialChain(t *testing.T) {
	settings := validApplicationSettings()
	settings.Model.AWSProfile = ""

	if err := settings.Validate(CommandDoctor); err != nil {
		t.Fatalf("Validate(doctor) error = %v, want optional profile", err)
	}
}

func TestApplicationSettingsValidateDoctorRequiresEveryPreflightSetting(t *testing.T) {
	tests := []struct {
		name       string
		invalidate func(*ApplicationSettings)
	}{
		{name: "Google folder", invalidate: func(settings *ApplicationSettings) { settings.GoogleFolderID = "" }},
		{name: "Google client file", invalidate: func(settings *ApplicationSettings) { settings.GoogleOAuthClientFile = "" }},
		{name: "Google token file", invalidate: func(settings *ApplicationSettings) { settings.GoogleOAuthTokenFile = "" }},
		{name: "transcript titles", invalidate: func(settings *ApplicationSettings) { settings.TranscriptTitles = nil }},
		{name: "notes titles", invalidate: func(settings *ApplicationSettings) { settings.NotesTitles = nil }},
		{name: "model mode", invalidate: func(settings *ApplicationSettings) { settings.Model.DataMode = "" }},
		{name: "model provider", invalidate: func(settings *ApplicationSettings) { settings.Model.Provider = "" }},
		{name: "model ID", invalidate: func(settings *ApplicationSettings) { settings.Model.ModelID = "" }},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			settings := validApplicationSettings()
			testCase.invalidate(&settings)
			if err := settings.Validate(CommandDoctor); err == nil {
				t.Fatal("Validate(doctor) error = nil, want missing preflight setting error")
			}
		})
	}
}

func validApplicationSettings() ApplicationSettings {
	return ApplicationSettings{
		GoogleFolderID:        "folder-id",
		GoogleOAuthClientFile: "/tmp/client.json",
		GoogleOAuthTokenFile:  "/tmp/token.json",
		TranscriptTitles:      []string{"Transcript"},
		NotesTitles:           []string{"Meeting notes"},
		Model: ModelSettings{
			DataMode:        "personal",
			Provider:        "bedrock",
			ModelID:         "model-id",
			MaxOutputTokens: 1000,
			MaxAttempts:     defaultModelMaxAttempts,
			AWSProfile:      "stacks",
			AWSRegion:       "us-east-1",
		},
		IngestionLeaseDuration:  defaultIngestionLeaseDuration,
		IngestionAttemptTimeout: defaultIngestionAttemptTimeout,
		ExtractionPromptVersion: "extract-v2",
	}
}

func diffApplicationSettings(got, want ApplicationSettings) string {
	if got.GoogleFolderID != want.GoogleFolderID ||
		got.GoogleOAuthClientFile != want.GoogleOAuthClientFile ||
		got.GoogleOAuthTokenFile != want.GoogleOAuthTokenFile ||
		got.Model != want.Model ||
		got.IngestionLeaseDuration != want.IngestionLeaseDuration ||
		got.IngestionAttemptTimeout != want.IngestionAttemptTimeout ||
		got.ExtractionPromptVersion != want.ExtractionPromptVersion {
		return "application settings did not match environment values"
	}
	if len(got.TranscriptTitles) != len(want.TranscriptTitles) || len(got.NotesTitles) != len(want.NotesTitles) {
		return "application title sets did not match environment values"
	}
	for index := range want.TranscriptTitles {
		if got.TranscriptTitles[index] != want.TranscriptTitles[index] {
			return "application transcript titles did not match environment values"
		}
	}
	for index := range want.NotesTitles {
		if got.NotesTitles[index] != want.NotesTitles[index] {
			return "application notes titles did not match environment values"
		}
	}
	return ""
}

func clearObservabilityEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		LogLevelEnvironmentVariable,
		OTelEnabledEnvironmentVariable,
		OTelEndpointEnvironmentVariable,
		OTelInsecureEnvironmentVariable,
		OTelMetricIntervalEnvironmentVariable,
		OTelServiceNameEnvironmentVariable,
		OTelTraceSampleRatioEnvironmentVariable,
	} {
		t.Setenv(name, "")
	}
}
