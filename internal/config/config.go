package config

import (
	"fmt"
	"strings"
	"time"
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

func validateDatabaseScopes(scopes []DatabaseScope) error {
	seen := make(map[DatabaseScope]struct{}, len(scopes))
	coreCount := 0
	for _, scope := range scopes {
		switch scope {
		case DatabaseScopeCore:
			coreCount++
		case DatabaseScopeDirectory:
		default:
			return fmt.Errorf(
				"%s may contain only %q and %q",
				DatabaseScopesEnvironmentVariable,
				DatabaseScopeCore,
				DatabaseScopeDirectory,
			)
		}
		if _, duplicate := seen[scope]; duplicate {
			return fmt.Errorf("%s must not contain duplicate scopes", DatabaseScopesEnvironmentVariable)
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
