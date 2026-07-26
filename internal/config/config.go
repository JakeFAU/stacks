package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"stacks/internal/modelpolicy"
)

const (
	defaultHTTPHost                         = "127.0.0.1"
	defaultHTTPPort                         = 8080
	defaultReadHeaderTimeoutSeconds         = 5
	defaultLogLevel                         = "info"
	defaultOTelEndpoint                     = "127.0.0.1:4317"
	defaultOTelMetricExportInterval         = 10 * time.Second
	defaultOTelServiceName                  = "stacks"
	defaultOTelTraceSampleRatio             = 1.0
	HTTPHostEnvironmentVariable             = "STACKS_HTTP_HOST"
	HTTPPortEnvironmentVariable             = "STACKS_HTTP_PORT"
	ReadHeaderTimeoutEnvironmentVariable    = "STACKS_READ_HEADER_TIMEOUT_SECONDS"
	LogLevelEnvironmentVariable             = "STACKS_LOG_LEVEL"
	OTelEnabledEnvironmentVariable          = "STACKS_OTEL_ENABLED"
	OTelEndpointEnvironmentVariable         = "STACKS_OTEL_ENDPOINT"
	OTelInsecureEnvironmentVariable         = "STACKS_OTEL_INSECURE"
	OTelMetricIntervalEnvironmentVariable   = "STACKS_OTEL_METRIC_EXPORT_INTERVAL"
	OTelServiceNameEnvironmentVariable      = "STACKS_OTEL_SERVICE_NAME"
	OTelTraceSampleRatioEnvironmentVariable = "STACKS_OTEL_TRACE_SAMPLE_RATIO"
	MigrationDatabaseURLEnvironmentVariable = "STACKS_MIGRATION_DATABASE_URL"
	DatabaseScopesEnvironmentVariable       = "STACKS_DATABASE_SCOPES"
	DatabaseAppRoleEnvironmentVariable      = "STACKS_DATABASE_APP_ROLE"
	defaultDatabaseScopes                   = "core"
	defaultDatabaseAppRole                  = "stacks_app"
)

// Settings contains validated runtime configuration.
type Settings struct {
	HTTPAddress       string
	ReadHeaderTimeout time.Duration
	LogLevel          string
	Telemetry         TelemetrySettings
	Database          DatabaseSettings
	Application       ApplicationSettings
}

// DatabaseScope identifies one selected embedded PostgreSQL migration scope.
type DatabaseScope string

const (
	DatabaseScopeCore      DatabaseScope = "core"
	DatabaseScopeDirectory DatabaseScope = "directory"
)

// DatabaseSettings separates least-privileged runtime inspection from
// schema-capable migration administration.
type DatabaseSettings struct {
	URL             string
	MigrationURL    string
	Scopes          []DatabaseScope
	ApplicationRole string
}

// TelemetrySettings controls OTLP export. Telemetry remains optional so the
// service can run without the local observability stack.
type TelemetrySettings struct {
	Enabled              bool
	Endpoint             string
	Insecure             bool
	MetricExportInterval time.Duration
	ServiceName          string
	TraceSampleRatio     float64
}

// Load reads and validates settings from the environment.
func Load() (Settings, error) {
	host := environmentOrDefault(HTTPHostEnvironmentVariable, defaultHTTPHost)
	port, err := positiveIntegerEnvironment(HTTPPortEnvironmentVariable, defaultHTTPPort)
	if err != nil {
		return Settings{}, err
	}
	if port > 65535 {
		return Settings{}, fmt.Errorf("%s must be at most 65535", HTTPPortEnvironmentVariable)
	}

	readHeaderTimeoutSeconds, err := positiveIntegerEnvironment(
		ReadHeaderTimeoutEnvironmentVariable,
		defaultReadHeaderTimeoutSeconds,
	)
	if err != nil {
		return Settings{}, err
	}

	logLevel := strings.ToLower(environmentOrDefault(LogLevelEnvironmentVariable, defaultLogLevel))
	if !validLogLevel(logLevel) {
		return Settings{}, fmt.Errorf("%s must be one of debug, info, warn, or error", LogLevelEnvironmentVariable)
	}

	telemetryEnabled, err := booleanEnvironment(OTelEnabledEnvironmentVariable, false)
	if err != nil {
		return Settings{}, err
	}
	telemetryInsecure, err := booleanEnvironment(OTelInsecureEnvironmentVariable, true)
	if err != nil {
		return Settings{}, err
	}
	metricExportInterval, err := durationEnvironment(
		OTelMetricIntervalEnvironmentVariable,
		defaultOTelMetricExportInterval,
	)
	if err != nil {
		return Settings{}, err
	}
	traceSampleRatio, err := unitIntervalEnvironment(
		OTelTraceSampleRatioEnvironmentVariable,
		defaultOTelTraceSampleRatio,
	)
	if err != nil {
		return Settings{}, err
	}
	modelMaxOutputTokens, err := optionalPositiveIntegerEnvironment(ModelMaxTokensEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	modelMaxAttempts, err := positiveIntegerEnvironment(
		ModelMaxAttemptsEnvironmentVariable,
		defaultModelMaxAttempts,
	)
	if err != nil {
		return Settings{}, err
	}
	ingestionLeaseDuration, err := durationEnvironment(
		IngestionLeaseDurationEnvironmentVariable,
		defaultIngestionLeaseDuration,
	)
	if err != nil {
		return Settings{}, err
	}
	if ingestionLeaseDuration > maximumIngestionLeaseDuration {
		return Settings{}, fmt.Errorf("%s must be no greater than %s", IngestionLeaseDurationEnvironmentVariable, maximumIngestionLeaseDuration)
	}
	ingestionAttemptTimeout, err := durationEnvironment(
		IngestionAttemptTimeoutEnvironmentVariable,
		defaultIngestionAttemptTimeout,
	)
	if err != nil {
		return Settings{}, err
	}
	if ingestionAttemptTimeout > ingestionLeaseDuration-minimumLeaseCleanupMargin {
		return Settings{}, fmt.Errorf("%s must leave at least %s before %s expires",
			IngestionAttemptTimeoutEnvironmentVariable, minimumLeaseCleanupMargin, IngestionLeaseDurationEnvironmentVariable)
	}
	googleDirectoryEnabled, err := booleanEnvironment(GoogleDirectoryEnabledEnvironmentVariable, false)
	if err != nil {
		return Settings{}, err
	}
	googleDirectoryFreshness, err := durationEnvironment(
		GoogleDirectoryFreshnessEnvironmentVariable,
		defaultGoogleDirectoryFreshness,
	)
	if err != nil {
		return Settings{}, err
	}
	googleDirectoryRetryAfter, err := durationEnvironment(
		GoogleDirectoryRetryAfterEnvironmentVariable,
		defaultGoogleDirectoryRetryAfter,
	)
	if err != nil {
		return Settings{}, err
	}
	googleDirectoryMaxAttempts, err := positiveIntegerEnvironment(
		GoogleDirectoryMaxAttemptsEnvironmentVariable,
		defaultGoogleDirectoryMaxAttempts,
	)
	if err != nil {
		return Settings{}, err
	}
	if googleDirectoryMaxAttempts > defaultGoogleDirectoryMaxAttempts {
		return Settings{}, fmt.Errorf("%s must be between 1 and %d", GoogleDirectoryMaxAttemptsEnvironmentVariable, defaultGoogleDirectoryMaxAttempts)
	}
	databaseScopes, err := databaseScopesEnvironment()
	if err != nil {
		return Settings{}, err
	}

	return Settings{
		HTTPAddress:       net.JoinHostPort(host, strconv.Itoa(port)),
		ReadHeaderTimeout: time.Duration(readHeaderTimeoutSeconds) * time.Second,
		LogLevel:          logLevel,
		Telemetry: TelemetrySettings{
			Enabled:              telemetryEnabled,
			Endpoint:             environmentOrDefault(OTelEndpointEnvironmentVariable, defaultOTelEndpoint),
			Insecure:             telemetryInsecure,
			MetricExportInterval: metricExportInterval,
			ServiceName:          environmentOrDefault(OTelServiceNameEnvironmentVariable, defaultOTelServiceName),
			TraceSampleRatio:     traceSampleRatio,
		},
		Database: DatabaseSettings{
			URL:             os.Getenv(DatabaseURLEnvironmentVariable),
			MigrationURL:    os.Getenv(MigrationDatabaseURLEnvironmentVariable),
			Scopes:          databaseScopes,
			ApplicationRole: environmentOrDefault(DatabaseAppRoleEnvironmentVariable, defaultDatabaseAppRole),
		},
		Application: ApplicationSettings{
			GoogleFolderID:        os.Getenv(GoogleFolderIDEnvironmentVariable),
			GoogleOAuthClientFile: os.Getenv(GoogleOAuthClientFileEnvironmentVariable),
			GoogleOAuthTokenFile:  os.Getenv(GoogleOAuthTokenFileEnvironmentVariable),
			Directory: GoogleDirectorySettings{
				Enabled:         googleDirectoryEnabled,
				OAuthClientFile: os.Getenv(GoogleDirectoryClientFileEnvironmentVariable),
				OAuthTokenFile:  os.Getenv(GoogleDirectoryTokenFileEnvironmentVariable),
				EmailDomains:    titleSetEnvironment(GoogleDirectoryDomainsEnvironmentVariable),
				Freshness:       googleDirectoryFreshness,
				RetryAfter:      googleDirectoryRetryAfter,
				MaxAttempts:     googleDirectoryMaxAttempts,
			},
			TranscriptTitles: titleSetEnvironment(TranscriptTitlesEnvironmentVariable),
			NotesTitles:      titleSetEnvironment(NotesTitlesEnvironmentVariable),
			Model: ModelSettings{
				DataMode:        modelpolicy.DataMode(os.Getenv(DataModeEnvironmentVariable)),
				Provider:        modelpolicy.Provider(os.Getenv(ModelProviderEnvironmentVariable)),
				ModelID:         os.Getenv(ModelIDEnvironmentVariable),
				MaxOutputTokens: modelMaxOutputTokens,
				MaxAttempts:     modelMaxAttempts,
				AWSProfile:      os.Getenv(AWSProfileEnvironmentVariable),
				AWSRegion:       os.Getenv(AWSRegionEnvironmentVariable),
				OpenAIAPIKey:    os.Getenv(OpenAIAPIKeyEnvironmentVariable),
				AnthropicAPIKey: os.Getenv(AnthropicAPIKeyEnvironmentVariable),
			},
			LegacyModelEnvironment:  configuredUnsupportedModelEnvironment(),
			IngestionLeaseDuration:  ingestionLeaseDuration,
			IngestionAttemptTimeout: ingestionAttemptTimeout,
			ExtractionPromptVersion: environmentOrDefault(ExtractionPromptVersionEnvironmentVariable, defaultExtractionPromptVersion),
			ManagerConfidence: ManagerConfidenceSettings{
				PromptVersion:    environmentOrDefault(AnalysisPromptVersionEnvironmentVariable, defaultAnalysisPromptVersion),
				EmployeeEntityID: os.Getenv(EmployeeEntityIDEnvironmentVariable),
				ManagerEntityID:  os.Getenv(ManagerEntityIDEnvironmentVariable),
			},
		},
	}, nil
}

// Validate applies command-specific database and application validation before
// any connection or provider dependency can be constructed.
func (settings Settings) Validate(command Command) error {
	if err := settings.Database.validate(command, settings.Application.Directory.Enabled); err != nil {
		return err
	}
	return settings.Application.Validate(command)
}

func (settings DatabaseSettings) validate(command Command, directoryEnabled bool) error {
	scopes := settings.Scopes
	if len(scopes) == 0 {
		scopes = []DatabaseScope{DatabaseScopeCore}
	}
	if err := validateDatabaseScopes(scopes); err != nil {
		return err
	}
	if directoryEnabled && !containsDatabaseScope(scopes, DatabaseScopeDirectory) {
		return fmt.Errorf(
			"%s must include %q when %s is enabled",
			DatabaseScopesEnvironmentVariable,
			DatabaseScopeDirectory,
			GoogleDirectoryEnabledEnvironmentVariable,
		)
	}
	switch command {
	case CommandDoctor, CommandSync, CommandEntities, CommandReview, CommandAnalyze, CommandDBStatus:
		return validateExactRequired(command, DatabaseURLEnvironmentVariable, settings.URL)
	case CommandDBMigrate, CommandDBReset:
		if command == CommandDBReset {
			if err := validateExactRequired(
				command,
				DatabaseURLEnvironmentVariable,
				settings.URL,
			); err != nil {
				return err
			}
		}
		if err := validateExactRequired(
			command,
			MigrationDatabaseURLEnvironmentVariable,
			settings.MigrationURL,
		); err != nil {
			return err
		}
		if !validDatabaseIdentifier(settings.ApplicationRole) {
			return fmt.Errorf("%s must be a safe PostgreSQL identifier for %s", DatabaseAppRoleEnvironmentVariable, command)
		}
	}
	return nil
}

func validDatabaseIdentifier(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			character == '_' ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func databaseScopesEnvironment() ([]DatabaseScope, error) {
	raw := environmentOrDefault(DatabaseScopesEnvironmentVariable, defaultDatabaseScopes)
	parts := strings.Split(raw, ",")
	scopes := make([]DatabaseScope, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, fmt.Errorf("%s must contain nonblank comma-separated scopes", DatabaseScopesEnvironmentVariable)
		}
		scopes = append(scopes, DatabaseScope(value))
	}
	if err := validateDatabaseScopes(scopes); err != nil {
		return nil, err
	}
	return scopes, nil
}

func validateDatabaseScopes(scopes []DatabaseScope) error {
	seen := make(map[DatabaseScope]struct{}, len(scopes))
	coreCount := 0
	for _, scope := range scopes {
		switch scope {
		case DatabaseScopeCore:
			coreCount++
		case DatabaseScopeDirectory:
		default:
			return fmt.Errorf("%s contains unknown scope %q", DatabaseScopesEnvironmentVariable, scope)
		}
		if _, duplicate := seen[scope]; duplicate {
			return fmt.Errorf("%s contains duplicate scope %q", DatabaseScopesEnvironmentVariable, scope)
		}
		seen[scope] = struct{}{}
	}
	if coreCount != 1 {
		return fmt.Errorf("%s must contain %q exactly once", DatabaseScopesEnvironmentVariable, DatabaseScopeCore)
	}
	return nil
}

func containsDatabaseScope(scopes []DatabaseScope, wanted DatabaseScope) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func configuredUnsupportedModelEnvironment() []string {
	configured := make([]string, 0, len(unsupportedModelEnvironmentNames))
	for _, name := range unsupportedModelEnvironmentNames {
		if value, present := os.LookupEnv(name); present && value != "" {
			configured = append(configured, name)
		}
	}
	return configured
}

func positiveIntegerEnvironment(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func optionalPositiveIntegerEnvironment(name string) (int, error) {
	if os.Getenv(name) == "" {
		return 0, nil
	}
	return positiveIntegerEnvironment(name, 0)
}

func titleSetEnvironment(name string) []string {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	titles := make([]string, 0, len(parts))
	for _, part := range parts {
		if title := strings.TrimSpace(part); title != "" {
			titles = append(titles, title)
		}
	}
	return titles
}

func booleanEnvironment(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return parsed, nil
}

func durationEnvironment(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}

func unitIntervalEnvironment(name string, fallback float64) (float64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > 1 {
		return 0, fmt.Errorf("%s must be a finite number between 0 and 1", name)
	}
	return parsed, nil
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
