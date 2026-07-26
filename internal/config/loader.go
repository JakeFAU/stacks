package config

import (
	"bytes"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"stacks/internal/modelpolicy"
)

const (
	configKeyHTTPHost                  = "http.host"
	configKeyHTTPPort                  = "http.port"
	configKeyHTTPReadHeaderTimeout     = "http.read_header_timeout_seconds"
	configKeyLogLevel                  = "log.level"
	configKeyTelemetryEnabled          = "telemetry.enabled"
	configKeyTelemetryEndpoint         = "telemetry.endpoint"
	configKeyTelemetryInsecure         = "telemetry.insecure"
	configKeyTelemetryMetricInterval   = "telemetry.metric_export_interval"
	configKeyTelemetryServiceName      = "telemetry.service_name"
	configKeyTelemetryTraceSampleRatio = "telemetry.trace_sample_ratio"
	configKeyDatabaseScopes            = "database.scopes"
	configKeyDatabaseApplicationRole   = "database.application_role"
	configKeyGoogleFolderID            = "google.folder_id"
	configKeyGoogleOAuthClientFile     = "google.oauth_client_file"
	configKeyGoogleOAuthTokenFile      = "google.oauth_token_file"
	configKeyGoogleTranscriptTitles    = "google.transcript_titles"
	configKeyGoogleNotesTitles         = "google.notes_titles"
	configKeyDirectoryEnabled          = "directory.enabled"
	configKeyDirectoryOAuthClientFile  = "directory.oauth_client_file"
	configKeyDirectoryOAuthTokenFile   = "directory.oauth_token_file"
	configKeyDirectoryEmailDomains     = "directory.email_domains"
	configKeyDirectoryFreshness        = "directory.freshness"
	configKeyDirectoryRetryAfter       = "directory.retry_after"
	configKeyDirectoryMaxAttempts      = "directory.max_attempts"
	configKeyModelDataMode             = "model.data_mode"
	configKeyModelProvider             = "model.provider"
	configKeyModelID                   = "model.id"
	configKeyModelMaxOutputTokens      = "model.max_output_tokens"
	configKeyModelMaxAttempts          = "model.max_attempts"
	configKeyModelAWSProfile           = "model.aws_profile"
	configKeyModelAWSRegion            = "model.aws_region"
	configKeyIngestionLeaseDuration    = "ingestion.lease_duration"
	configKeyIngestionAttemptTimeout   = "ingestion.attempt_timeout"
	configKeyExtractionPromptVersion   = "extraction.prompt_version"
	configKeyAnalysisPromptVersion     = "analysis.prompt_version"
)

type environmentBinding struct {
	key  string
	name string
}

var configurationEnvironmentBindings = []environmentBinding{
	{configKeyHTTPHost, HTTPHostEnvironmentVariable},
	{configKeyHTTPPort, HTTPPortEnvironmentVariable},
	{configKeyHTTPReadHeaderTimeout, ReadHeaderTimeoutEnvironmentVariable},
	{configKeyLogLevel, LogLevelEnvironmentVariable},
	{configKeyTelemetryEnabled, OTelEnabledEnvironmentVariable},
	{configKeyTelemetryEndpoint, OTelEndpointEnvironmentVariable},
	{configKeyTelemetryInsecure, OTelInsecureEnvironmentVariable},
	{configKeyTelemetryMetricInterval, OTelMetricIntervalEnvironmentVariable},
	{configKeyTelemetryServiceName, OTelServiceNameEnvironmentVariable},
	{configKeyTelemetryTraceSampleRatio, OTelTraceSampleRatioEnvironmentVariable},
	{configKeyDatabaseScopes, DatabaseScopesEnvironmentVariable},
	{configKeyDatabaseApplicationRole, DatabaseAppRoleEnvironmentVariable},
	{configKeyGoogleFolderID, GoogleFolderIDEnvironmentVariable},
	{configKeyGoogleOAuthClientFile, GoogleOAuthClientFileEnvironmentVariable},
	{configKeyGoogleOAuthTokenFile, GoogleOAuthTokenFileEnvironmentVariable},
	{configKeyGoogleTranscriptTitles, TranscriptTitlesEnvironmentVariable},
	{configKeyGoogleNotesTitles, NotesTitlesEnvironmentVariable},
	{configKeyDirectoryEnabled, GoogleDirectoryEnabledEnvironmentVariable},
	{configKeyDirectoryOAuthClientFile, GoogleDirectoryClientFileEnvironmentVariable},
	{configKeyDirectoryOAuthTokenFile, GoogleDirectoryTokenFileEnvironmentVariable},
	{configKeyDirectoryEmailDomains, GoogleDirectoryDomainsEnvironmentVariable},
	{configKeyDirectoryFreshness, GoogleDirectoryFreshnessEnvironmentVariable},
	{configKeyDirectoryRetryAfter, GoogleDirectoryRetryAfterEnvironmentVariable},
	{configKeyDirectoryMaxAttempts, GoogleDirectoryMaxAttemptsEnvironmentVariable},
	{configKeyModelDataMode, DataModeEnvironmentVariable},
	{configKeyModelProvider, ModelProviderEnvironmentVariable},
	{configKeyModelID, ModelIDEnvironmentVariable},
	{configKeyModelMaxOutputTokens, ModelMaxTokensEnvironmentVariable},
	{configKeyModelMaxAttempts, ModelMaxAttemptsEnvironmentVariable},
	{configKeyModelAWSProfile, AWSProfileEnvironmentVariable},
	{configKeyModelAWSRegion, AWSRegionEnvironmentVariable},
	{configKeyIngestionLeaseDuration, IngestionLeaseDurationEnvironmentVariable},
	{configKeyIngestionAttemptTimeout, IngestionAttemptTimeoutEnvironmentVariable},
	{configKeyExtractionPromptVersion, ExtractionPromptVersionEnvironmentVariable},
	{configKeyAnalysisPromptVersion, AnalysisPromptVersionEnvironmentVariable},
}

// Load returns settings assembled from defaults and approved environment values.
func Load() (Settings, error) {
	return LoadWithOptions(LoadOptions{})
}

// LoadOptions identifies one already-validated optional configuration file.
type LoadOptions struct {
	ConfigFile *string
}

// LoadWithOptions returns typed settings from defaults, one configuration file,
// and exact non-secret environment bindings, in that precedence order.
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

func setDefaults(values *viper.Viper) {
	values.SetDefault(configKeyHTTPHost, defaultHTTPHost)
	values.SetDefault(configKeyHTTPPort, defaultHTTPPort)
	values.SetDefault(configKeyHTTPReadHeaderTimeout, defaultReadHeaderTimeoutSeconds)
	values.SetDefault(configKeyLogLevel, defaultLogLevel)
	values.SetDefault(configKeyTelemetryEnabled, false)
	values.SetDefault(configKeyTelemetryEndpoint, defaultOTelEndpoint)
	values.SetDefault(configKeyTelemetryInsecure, true)
	values.SetDefault(configKeyTelemetryMetricInterval, defaultOTelMetricExportInterval)
	values.SetDefault(configKeyTelemetryServiceName, defaultOTelServiceName)
	values.SetDefault(configKeyTelemetryTraceSampleRatio, defaultOTelTraceSampleRatio)
	values.SetDefault(configKeyDatabaseScopes, []string{defaultDatabaseScopes})
	values.SetDefault(configKeyDatabaseApplicationRole, defaultDatabaseAppRole)
	values.SetDefault(configKeyDirectoryEnabled, false)
	values.SetDefault(configKeyDirectoryFreshness, defaultGoogleDirectoryFreshness)
	values.SetDefault(configKeyDirectoryRetryAfter, defaultGoogleDirectoryRetryAfter)
	values.SetDefault(configKeyDirectoryMaxAttempts, defaultGoogleDirectoryMaxAttempts)
	values.SetDefault(configKeyModelMaxAttempts, defaultModelMaxAttempts)
	values.SetDefault(configKeyIngestionLeaseDuration, defaultIngestionLeaseDuration)
	values.SetDefault(configKeyIngestionAttemptTimeout, defaultIngestionAttemptTimeout)
	values.SetDefault(configKeyExtractionPromptVersion, defaultExtractionPromptVersion)
	values.SetDefault(configKeyAnalysisPromptVersion, defaultAnalysisPromptVersion)
}

func bindEnvironment(values *viper.Viper) error {
	for _, binding := range configurationEnvironmentBindings {
		if err := values.BindEnv(binding.key, binding.name); err != nil {
			return fmt.Errorf("bind %s: %w", binding.name, err)
		}
	}
	return nil
}

func settingsFrom(values *viper.Viper) (Settings, error) {
	host, err := defaultedStringValue(values, configKeyHTTPHost, HTTPHostEnvironmentVariable, defaultHTTPHost)
	if err != nil {
		return Settings{}, err
	}
	port, err := positiveIntegerValue(values, configKeyHTTPPort, HTTPPortEnvironmentVariable, false)
	if err != nil {
		return Settings{}, err
	}
	if port > 65535 {
		return Settings{}, fmt.Errorf("%s must be at most 65535", HTTPPortEnvironmentVariable)
	}
	readHeaderTimeoutSeconds, err := positiveIntegerValue(values, configKeyHTTPReadHeaderTimeout, ReadHeaderTimeoutEnvironmentVariable, false)
	if err != nil {
		return Settings{}, err
	}
	logLevel, err := defaultedStringValue(values, configKeyLogLevel, LogLevelEnvironmentVariable, defaultLogLevel)
	if err != nil {
		return Settings{}, err
	}
	logLevel = strings.ToLower(logLevel)
	if !validLogLevel(logLevel) {
		return Settings{}, fmt.Errorf("%s must be one of debug, info, warn, or error", LogLevelEnvironmentVariable)
	}
	telemetryEnabled, err := booleanValue(values, configKeyTelemetryEnabled, OTelEnabledEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	telemetryInsecure, err := booleanValue(values, configKeyTelemetryInsecure, OTelInsecureEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	metricExportInterval, err := durationValue(values, configKeyTelemetryMetricInterval, OTelMetricIntervalEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	traceSampleRatio, err := unitIntervalValue(values, configKeyTelemetryTraceSampleRatio, OTelTraceSampleRatioEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	modelMaxOutputTokens, err := positiveIntegerValue(values, configKeyModelMaxOutputTokens, ModelMaxTokensEnvironmentVariable, true)
	if err != nil {
		return Settings{}, err
	}
	modelMaxAttempts, err := positiveIntegerValue(values, configKeyModelMaxAttempts, ModelMaxAttemptsEnvironmentVariable, false)
	if err != nil {
		return Settings{}, err
	}
	ingestionLeaseDuration, err := durationValue(values, configKeyIngestionLeaseDuration, IngestionLeaseDurationEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	if ingestionLeaseDuration > maximumIngestionLeaseDuration {
		return Settings{}, fmt.Errorf("%s must be no greater than %s", IngestionLeaseDurationEnvironmentVariable, maximumIngestionLeaseDuration)
	}
	ingestionAttemptTimeout, err := durationValue(values, configKeyIngestionAttemptTimeout, IngestionAttemptTimeoutEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	if ingestionAttemptTimeout > ingestionLeaseDuration-minimumLeaseCleanupMargin {
		return Settings{}, fmt.Errorf("%s must leave at least %s before %s expires", IngestionAttemptTimeoutEnvironmentVariable, minimumLeaseCleanupMargin, IngestionLeaseDurationEnvironmentVariable)
	}
	googleDirectoryEnabled, err := booleanValue(values, configKeyDirectoryEnabled, GoogleDirectoryEnabledEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	googleDirectoryFreshness, err := durationValue(values, configKeyDirectoryFreshness, GoogleDirectoryFreshnessEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	googleDirectoryRetryAfter, err := durationValue(values, configKeyDirectoryRetryAfter, GoogleDirectoryRetryAfterEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	googleDirectoryMaxAttempts, err := positiveIntegerValue(values, configKeyDirectoryMaxAttempts, GoogleDirectoryMaxAttemptsEnvironmentVariable, false)
	if err != nil {
		return Settings{}, err
	}
	if googleDirectoryMaxAttempts > defaultGoogleDirectoryMaxAttempts {
		return Settings{}, fmt.Errorf("%s must be between 1 and %d", GoogleDirectoryMaxAttemptsEnvironmentVariable, defaultGoogleDirectoryMaxAttempts)
	}
	databaseScopes, err := databaseScopeValues(values)
	if err != nil {
		return Settings{}, err
	}
	endpoint, err := defaultedStringValue(values, configKeyTelemetryEndpoint, OTelEndpointEnvironmentVariable, defaultOTelEndpoint)
	if err != nil {
		return Settings{}, err
	}
	serviceName, err := defaultedStringValue(values, configKeyTelemetryServiceName, OTelServiceNameEnvironmentVariable, defaultOTelServiceName)
	if err != nil {
		return Settings{}, err
	}
	applicationRole, err := defaultedStringValue(values, configKeyDatabaseApplicationRole, DatabaseAppRoleEnvironmentVariable, defaultDatabaseAppRole)
	if err != nil {
		return Settings{}, err
	}
	googleFolderID, err := stringValue(values, configKeyGoogleFolderID, GoogleFolderIDEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	googleOAuthClientFile, err := stringValue(values, configKeyGoogleOAuthClientFile, GoogleOAuthClientFileEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	googleOAuthTokenFile, err := stringValue(values, configKeyGoogleOAuthTokenFile, GoogleOAuthTokenFileEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	directoryOAuthClientFile, err := stringValue(values, configKeyDirectoryOAuthClientFile, GoogleDirectoryClientFileEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	directoryOAuthTokenFile, err := stringValue(values, configKeyDirectoryOAuthTokenFile, GoogleDirectoryTokenFileEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	directoryDomains, err := stringListValue(values, configKeyDirectoryEmailDomains, GoogleDirectoryDomainsEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	transcriptTitles, err := stringListValue(values, configKeyGoogleTranscriptTitles, TranscriptTitlesEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	notesTitles, err := stringListValue(values, configKeyGoogleNotesTitles, NotesTitlesEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	dataMode, err := stringValue(values, configKeyModelDataMode, DataModeEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	provider, err := stringValue(values, configKeyModelProvider, ModelProviderEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	modelID, err := stringValue(values, configKeyModelID, ModelIDEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	awsProfile, err := stringValue(values, configKeyModelAWSProfile, AWSProfileEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	awsRegion, err := stringValue(values, configKeyModelAWSRegion, AWSRegionEnvironmentVariable)
	if err != nil {
		return Settings{}, err
	}
	extractionPromptVersion, err := defaultedStringValue(values, configKeyExtractionPromptVersion, ExtractionPromptVersionEnvironmentVariable, defaultExtractionPromptVersion)
	if err != nil {
		return Settings{}, err
	}
	analysisPromptVersion, err := defaultedStringValue(values, configKeyAnalysisPromptVersion, AnalysisPromptVersionEnvironmentVariable, defaultAnalysisPromptVersion)
	if err != nil {
		return Settings{}, err
	}

	return Settings{
		HTTPAddress:       net.JoinHostPort(host, strconv.Itoa(port)),
		ReadHeaderTimeout: time.Duration(readHeaderTimeoutSeconds) * time.Second,
		LogLevel:          logLevel,
		Telemetry: TelemetrySettings{
			Enabled: telemetryEnabled, Endpoint: endpoint, Insecure: telemetryInsecure,
			MetricExportInterval: metricExportInterval, ServiceName: serviceName, TraceSampleRatio: traceSampleRatio,
		},
		Database: DatabaseSettings{
			URL: os.Getenv(DatabaseURLEnvironmentVariable), MigrationURL: os.Getenv(MigrationDatabaseURLEnvironmentVariable),
			Scopes: databaseScopes, ApplicationRole: applicationRole,
		},
		Application: ApplicationSettings{
			GoogleFolderID: googleFolderID, GoogleOAuthClientFile: googleOAuthClientFile, GoogleOAuthTokenFile: googleOAuthTokenFile,
			Directory: GoogleDirectorySettings{
				Enabled: googleDirectoryEnabled, OAuthClientFile: directoryOAuthClientFile, OAuthTokenFile: directoryOAuthTokenFile,
				EmailDomains: directoryDomains, Freshness: googleDirectoryFreshness, RetryAfter: googleDirectoryRetryAfter, MaxAttempts: googleDirectoryMaxAttempts,
			},
			TranscriptTitles: transcriptTitles, NotesTitles: notesTitles,
			Model: ModelSettings{
				DataMode: modelpolicy.DataMode(dataMode), Provider: modelpolicy.Provider(provider), ModelID: modelID,
				MaxOutputTokens: modelMaxOutputTokens, MaxAttempts: modelMaxAttempts, AWSProfile: awsProfile, AWSRegion: awsRegion,
				OpenAIAPIKey: os.Getenv(OpenAIAPIKeyEnvironmentVariable), AnthropicAPIKey: os.Getenv(AnthropicAPIKeyEnvironmentVariable),
			},
			LegacyModelEnvironment: configuredUnsupportedModelEnvironment(),
			IngestionLeaseDuration: ingestionLeaseDuration, IngestionAttemptTimeout: ingestionAttemptTimeout,
			ExtractionPromptVersion: extractionPromptVersion,
			ManagerConfidence: ManagerConfidenceSettings{
				PromptVersion: analysisPromptVersion, EmployeeEntityID: os.Getenv(EmployeeEntityIDEnvironmentVariable), ManagerEntityID: os.Getenv(ManagerEntityIDEnvironmentVariable),
			},
		},
	}, nil
}

func defaultedStringValue(values *viper.Viper, key, environmentName, fallback string) (string, error) {
	value, err := stringValue(values, key, environmentName)
	if err != nil || value != "" {
		return value, err
	}
	return fallback, nil
}

func stringValue(values *viper.Viper, key, environmentName string) (string, error) {
	value := values.Get(key)
	if value == nil {
		return "", nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", environmentName)
	}
	return stringValue, nil
}

func booleanValue(values *viper.Viper, key, environmentName string) (bool, error) {
	switch value := values.Get(key).(type) {
	case bool:
		return value, nil
	case string:
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			return parsed, nil
		}
	}
	return false, fmt.Errorf("%s must be a boolean", environmentName)
}

func positiveIntegerValue(values *viper.Viper, key, environmentName string, optional bool) (int, error) {
	value := values.Get(key)
	if optional && (value == nil || value == "") {
		return 0, nil
	}
	integer, ok := integer(value)
	if !ok || integer <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", environmentName)
	}
	return integer, nil
}

func integer(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case int8:
		return int(value), true
	case int16:
		return int(value), true
	case int32:
		return int(value), true
	case int64:
		converted := int(value)
		return converted, int64(converted) == value
	case uint:
		converted := int(value)
		return converted, converted >= 0 && uint(converted) == value
	case uint8:
		return int(value), true
	case uint16:
		return int(value), true
	case uint32:
		converted := int(value)
		return converted, converted >= 0 && uint32(converted) == value
	case uint64:
		converted := int(value)
		return converted, converted >= 0 && uint64(converted) == value
	case float64:
		converted := int(value)
		return converted, float64(converted) == value
	case string:
		converted, err := strconv.Atoi(value)
		return converted, err == nil
	default:
		return 0, false
	}
}

func durationValue(values *viper.Viper, key, environmentName string) (time.Duration, error) {
	switch value := values.Get(key).(type) {
	case time.Duration:
		if value > 0 {
			return value, nil
		}
	case string:
		parsed, err := time.ParseDuration(value)
		if err == nil && parsed > 0 {
			return parsed, nil
		}
	}
	return 0, fmt.Errorf("%s must be a positive duration", environmentName)
}

func unitIntervalValue(values *viper.Viper, key, environmentName string) (float64, error) {
	value := values.Get(key)
	var parsed float64
	switch value := value.(type) {
	case float64:
		parsed = value
	case float32:
		parsed = float64(value)
	case int:
		parsed = float64(value)
	case string:
		var err error
		parsed, err = strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a finite number between 0 and 1", environmentName)
		}
	default:
		return 0, fmt.Errorf("%s must be a finite number between 0 and 1", environmentName)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > 1 {
		return 0, fmt.Errorf("%s must be a finite number between 0 and 1", environmentName)
	}
	return parsed, nil
}

func stringListValue(values *viper.Viper, key, environmentName string) ([]string, error) {
	switch value := values.Get(key).(type) {
	case nil:
		return nil, nil
	case string:
		if value == "" {
			return nil, nil
		}
		parts := strings.Split(value, ",")
		list := make([]string, 0, len(parts))
		for _, part := range parts {
			if item := strings.TrimSpace(part); item != "" {
				list = append(list, item)
			}
		}
		return list, nil
	case []string:
		return append([]string(nil), value...), nil
	case []any:
		list := make([]string, len(value))
		for index, item := range value {
			stringItem, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be an array of strings", environmentName)
			}
			list[index] = stringItem
		}
		return list, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", environmentName)
	}
}

func databaseScopeValues(values *viper.Viper) ([]DatabaseScope, error) {
	rawScopes, err := stringListValue(values, configKeyDatabaseScopes, DatabaseScopesEnvironmentVariable)
	if err != nil {
		return nil, err
	}
	if len(rawScopes) == 0 {
		return nil, fmt.Errorf("%s must contain nonblank comma-separated scopes", DatabaseScopesEnvironmentVariable)
	}
	scopes := make([]DatabaseScope, len(rawScopes))
	for index, scope := range rawScopes {
		if scope == "" {
			return nil, fmt.Errorf("%s must contain nonblank comma-separated scopes", DatabaseScopesEnvironmentVariable)
		}
		scopes[index] = DatabaseScope(scope)
	}
	if err := validateDatabaseScopes(scopes); err != nil {
		return nil, err
	}
	return scopes, nil
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

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
