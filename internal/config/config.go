package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
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
)

// Settings contains validated runtime configuration.
type Settings struct {
	HTTPAddress       string
	ReadHeaderTimeout time.Duration
	LogLevel          string
	Telemetry         TelemetrySettings
	PoC               PoCSettings
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
	bedrockMaxTokens, err := optionalPositiveIntegerEnvironment(BedrockMaxTokensEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	bedrockMaxAttempts, err := positiveIntegerEnvironment(
		BedrockMaxAttemptsEnvironmentVariable,
		defaultBedrockMaxAttempts,
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
		PoC: PoCSettings{
			DatabaseURL:             os.Getenv(DatabaseURLEnvironmentVariable),
			GoogleFolderID:          os.Getenv(GoogleFolderIDEnvironmentVariable),
			GoogleOAuthClientFile:   os.Getenv(GoogleOAuthClientFileEnvironmentVariable),
			GoogleOAuthTokenFile:    os.Getenv(GoogleOAuthTokenFileEnvironmentVariable),
			TranscriptTitles:        titleSetEnvironment(TranscriptTitlesEnvironmentVariable),
			NotesTitles:             titleSetEnvironment(NotesTitlesEnvironmentVariable),
			AWSProfile:              os.Getenv(AWSProfileEnvironmentVariable),
			AWSRegion:               os.Getenv(AWSRegionEnvironmentVariable),
			BedrockModelID:          os.Getenv(BedrockModelIDEnvironmentVariable),
			BedrockMaxTokens:        bedrockMaxTokens,
			BedrockMaxAttempts:      bedrockMaxAttempts,
			IngestionLeaseDuration:  ingestionLeaseDuration,
			IngestionAttemptTimeout: ingestionAttemptTimeout,
			ExtractionPromptVersion: environmentOrDefault(ExtractionPromptVersionEnvironmentVariable, defaultExtractionPromptVersion),
			AnalysisPromptVersion:   environmentOrDefault(AnalysisPromptVersionEnvironmentVariable, defaultAnalysisPromptVersion),
			EmployeeEntityID:        os.Getenv(EmployeeEntityIDEnvironmentVariable),
			ManagerEntityID:         os.Getenv(ManagerEntityIDEnvironmentVariable),
		},
	}, nil
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
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
