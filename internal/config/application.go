package config

import (
	"fmt"
	"strings"
	"time"

	"stacks/internal/entity"
	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
)

const (
	defaultModelMaxAttempts           = 5
	defaultExtractionPromptVersion    = extract.ExtractionPromptVersion
	defaultIngestionLeaseDuration     = 5 * time.Minute
	defaultIngestionAttemptTimeout    = 4 * time.Minute
	maximumIngestionLeaseDuration     = time.Hour
	minimumLeaseCleanupMargin         = 5 * time.Second
	defaultGoogleDirectoryFreshness   = 24 * time.Hour
	defaultGoogleDirectoryRetryAfter  = 15 * time.Minute
	defaultGoogleDirectoryMaxAttempts = 3
	defaultQueryMaxEntities           = 16
	defaultQueryMaxPredicates         = 32
	defaultQueryMaxChronology         = 1000
	minimumQueryLimit                 = 1
	maximumQueryEntities              = 64
	maximumQueryPredicates            = 256
	maximumQueryChronology            = 10000

	DatabaseURLEnvironmentVariable                = "STACKS_DATABASE_URL"
	GoogleFolderIDEnvironmentVariable             = "STACKS_GOOGLE_FOLDER_ID"
	GoogleOAuthClientFileEnvironmentVariable      = "STACKS_GOOGLE_OAUTH_CLIENT_FILE"
	GoogleOAuthTokenFileEnvironmentVariable       = "STACKS_GOOGLE_OAUTH_TOKEN_FILE"
	TranscriptTitlesEnvironmentVariable           = "STACKS_TRANSCRIPT_TITLES"
	NotesTitlesEnvironmentVariable                = "STACKS_NOTES_TITLES"
	AWSProfileEnvironmentVariable                 = "STACKS_AWS_PROFILE"
	AWSRegionEnvironmentVariable                  = "STACKS_AWS_REGION"
	DataModeEnvironmentVariable                   = "STACKS_DATA_MODE"
	ModelProviderEnvironmentVariable              = "STACKS_MODEL_PROVIDER"
	ModelIDEnvironmentVariable                    = "STACKS_MODEL_ID"
	ModelMaxTokensEnvironmentVariable             = "STACKS_MODEL_MAX_OUTPUT_TOKENS"
	ModelMaxAttemptsEnvironmentVariable           = "STACKS_MODEL_MAX_ATTEMPTS"
	OpenAIAPIKeyEnvironmentVariable               = "OPENAI_API_KEY"
	AnthropicAPIKeyEnvironmentVariable            = "ANTHROPIC_API_KEY"
	OpenAIBaseURLEnvironmentVariable              = "OPENAI_BASE_URL"
	OpenAIOrganizationIDEnvironmentVariable       = "OPENAI_ORG_ID"
	OpenAIProjectIDEnvironmentVariable            = "OPENAI_PROJECT_ID"
	AnthropicBaseURLEnvironmentVariable           = "ANTHROPIC_BASE_URL"
	AnthropicAuthTokenEnvironmentVariable         = "ANTHROPIC_AUTH_TOKEN"
	AnthropicProfileEnvironmentVariable           = "ANTHROPIC_PROFILE"
	BedrockModelIDEnvironmentVariable             = "STACKS_BEDROCK_MODEL_ID"
	BedrockMaxTokensEnvironmentVariable           = "STACKS_BEDROCK_MAX_TOKENS"
	BedrockMaxAttemptsEnvironmentVariable         = "STACKS_BEDROCK_MAX_ATTEMPTS"
	IngestionLeaseDurationEnvironmentVariable     = "STACKS_INGEST_LEASE_DURATION"
	IngestionAttemptTimeoutEnvironmentVariable    = "STACKS_INGEST_ATTEMPT_TIMEOUT"
	ExtractionPromptVersionEnvironmentVariable    = "STACKS_EXTRACTION_PROMPT_VERSION"
	GoogleDirectoryEnabledEnvironmentVariable     = "STACKS_GOOGLE_DIRECTORY_ENABLED"
	GoogleDirectoryClientFileEnvironmentVariable  = "STACKS_GOOGLE_DIRECTORY_OAUTH_CLIENT_FILE"
	GoogleDirectoryTokenFileEnvironmentVariable   = "STACKS_GOOGLE_DIRECTORY_OAUTH_TOKEN_FILE"
	GoogleDirectoryDomainsEnvironmentVariable     = "STACKS_GOOGLE_DIRECTORY_EMAIL_DOMAINS"
	GoogleDirectoryFreshnessEnvironmentVariable   = "STACKS_GOOGLE_DIRECTORY_FRESHNESS"
	GoogleDirectoryRetryAfterEnvironmentVariable  = "STACKS_GOOGLE_DIRECTORY_RETRY_AFTER"
	GoogleDirectoryMaxAttemptsEnvironmentVariable = "STACKS_GOOGLE_DIRECTORY_MAX_ATTEMPTS"
	QueryMaxEntitiesEnvironmentVariable           = "STACKS_QUERY_MAX_ENTITIES"
	QueryMaxPredicatesEnvironmentVariable         = "STACKS_QUERY_MAX_PREDICATES"
	QueryMaxChronologyEnvironmentVariable         = "STACKS_QUERY_MAX_CHRONOLOGY"
)

var unsupportedModelEnvironmentNames = []string{
	BedrockModelIDEnvironmentVariable,
	BedrockMaxTokensEnvironmentVariable,
	BedrockMaxAttemptsEnvironmentVariable,
	OpenAIBaseURLEnvironmentVariable,
	OpenAIOrganizationIDEnvironmentVariable,
	OpenAIProjectIDEnvironmentVariable,
	AnthropicBaseURLEnvironmentVariable,
	AnthropicAuthTokenEnvironmentVariable,
	AnthropicProfileEnvironmentVariable,
}

// Command identifies a top-level Stacks command for configuration validation.
type Command string

const (
	CommandServe     Command = "serve"
	CommandAuth      Command = "auth"
	CommandDoctor    Command = "doctor"
	CommandSync      Command = "sync"
	CommandEntities  Command = "entities"
	CommandReview    Command = "review"
	CommandQuery     Command = "query"
	CommandDBMigrate Command = "db-migrate"
	CommandDBStatus  Command = "db-status"
	CommandDBReset   Command = "db-reset"
)

// ModelSettings holds the explicitly selected model invocation boundary and
// credentials. Credential values remain in memory and are never formatted in
// validation errors.
type ModelSettings struct {
	Provider        modelpolicy.Provider
	DataMode        modelpolicy.DataMode
	ModelID         string
	MaxOutputTokens int
	MaxAttempts     int
	AWSProfile      string
	AWSRegion       string
	OpenAIAPIKey    string
	AnthropicAPIKey string
}

// GoogleDirectorySettings controls optional, separately authorized directory
// identity enrichment. It remains disabled unless explicitly enabled.
type GoogleDirectorySettings struct {
	Enabled         bool
	OAuthClientFile string
	OAuthTokenFile  string
	EmailDomains    []string
	Freshness       time.Duration
	RetryAfter      time.Duration
	MaxAttempts     int
}

// GoogleAuthTarget identifies one independently scoped Google authorization.
type GoogleAuthTarget string

const (
	GoogleAuthDrive     GoogleAuthTarget = "google"
	GoogleAuthDirectory GoogleAuthTarget = "google-directory"
)

// ApplicationSettings holds command-specific application settings. Values are
// loaded without command validation so serving health traffic remains possible
// before the application is fully configured.
type ApplicationSettings struct {
	GoogleFolderID          string
	GoogleOAuthClientFile   string
	GoogleOAuthTokenFile    string
	Directory               GoogleDirectorySettings
	TranscriptTitles        []string
	NotesTitles             []string
	Model                   ModelSettings
	LegacyModelEnvironment  []string
	IngestionLeaseDuration  time.Duration
	IngestionAttemptTimeout time.Duration
	ExtractionPromptVersion string
}

// Validate verifies only the settings required by command. Serve intentionally
// requires no application configuration, preserving the no-argument server.
func (settings ApplicationSettings) Validate(command Command) error {
	switch command {
	case CommandServe, CommandDBMigrate, CommandDBStatus, CommandDBReset, "":
		return nil
	case CommandAuth:
		return nil
	case CommandDoctor:
		return settings.validateDoctor(command)
	case CommandSync:
		return settings.validateCorpusAndModel(command)
	case CommandEntities:
		return nil
	case CommandReview:
		return settings.validateGoogleDirectory(command)
	default:
		return nil
	}
}

func (settings ApplicationSettings) validateDoctor(command Command) error {
	if err := settings.validateRequired(command,
		GoogleFolderIDEnvironmentVariable,
		GoogleOAuthClientFileEnvironmentVariable,
		GoogleOAuthTokenFileEnvironmentVariable,
	); err != nil {
		return err
	}
	if err := settings.validateCorpusTitles(); err != nil {
		return err
	}
	if err := settings.validateModelSettings(command); err != nil {
		return err
	}
	return settings.validateGoogleDirectory(command)
}

func (settings ApplicationSettings) validateCorpusAndModel(command Command) error {
	if err := settings.validateRequired(command,
		GoogleFolderIDEnvironmentVariable,
		GoogleOAuthClientFileEnvironmentVariable,
		GoogleOAuthTokenFileEnvironmentVariable,
	); err != nil {
		return err
	}
	if err := settings.validateCorpusTitles(); err != nil {
		return err
	}
	if settings.IngestionLeaseDuration <= 0 || settings.IngestionLeaseDuration > maximumIngestionLeaseDuration {
		return fmt.Errorf("%s must be a positive duration no greater than %s", IngestionLeaseDurationEnvironmentVariable, maximumIngestionLeaseDuration)
	}
	if settings.IngestionAttemptTimeout <= 0 ||
		settings.IngestionAttemptTimeout > settings.IngestionLeaseDuration-minimumLeaseCleanupMargin {
		return fmt.Errorf("%s must leave at least %s before %s expires",
			IngestionAttemptTimeoutEnvironmentVariable, minimumLeaseCleanupMargin, IngestionLeaseDurationEnvironmentVariable)
	}
	if err := settings.validateModelSettings(command); err != nil {
		return err
	}
	return settings.validateGoogleDirectory(command)
}

// ValidateGoogleAuth validates only the credentials for the selected explicit
// authorization target. Directory paths are required even while enrichment is
// disabled so they can be authorized before enabling it.
func (settings ApplicationSettings) ValidateGoogleAuth(target GoogleAuthTarget) error {
	switch target {
	case GoogleAuthDrive:
		return settings.validateRequired(CommandAuth,
			GoogleOAuthClientFileEnvironmentVariable,
			GoogleOAuthTokenFileEnvironmentVariable,
		)
	case GoogleAuthDirectory:
		if err := validateExactRequired(CommandAuth, GoogleDirectoryClientFileEnvironmentVariable, settings.Directory.OAuthClientFile); err != nil {
			return err
		}
		return validateExactRequired(CommandAuth, GoogleDirectoryTokenFileEnvironmentVariable, settings.Directory.OAuthTokenFile)
	default:
		return fmt.Errorf("unsupported Google authorization target %q", target)
	}
}

func (settings ApplicationSettings) validateGoogleDirectory(command Command) error {
	if !settings.Directory.Enabled {
		return nil
	}
	if err := validateExactRequired(command, GoogleDirectoryClientFileEnvironmentVariable, settings.Directory.OAuthClientFile); err != nil {
		return err
	}
	if err := validateExactRequired(command, GoogleDirectoryTokenFileEnvironmentVariable, settings.Directory.OAuthTokenFile); err != nil {
		return err
	}
	if _, err := entity.NewDirectoryPolicy(settings.Directory.EmailDomains); err != nil {
		return fmt.Errorf("%s is invalid", GoogleDirectoryDomainsEnvironmentVariable)
	}
	if settings.Directory.Freshness <= 0 {
		return fmt.Errorf("%s must be a positive duration", GoogleDirectoryFreshnessEnvironmentVariable)
	}
	if settings.Directory.RetryAfter <= 0 {
		return fmt.Errorf("%s must be a positive duration", GoogleDirectoryRetryAfterEnvironmentVariable)
	}
	if settings.Directory.MaxAttempts < 1 || settings.Directory.MaxAttempts > defaultGoogleDirectoryMaxAttempts {
		return fmt.Errorf("%s must be between 1 and %d", GoogleDirectoryMaxAttemptsEnvironmentVariable, defaultGoogleDirectoryMaxAttempts)
	}
	return nil
}

func (settings ApplicationSettings) validateCorpusTitles() error {
	if len(normalizedTitleSet(settings.TranscriptTitles)) == 0 {
		return fmt.Errorf("%s must include at least one title", TranscriptTitlesEnvironmentVariable)
	}
	if len(normalizedTitleSet(settings.NotesTitles)) == 0 {
		return fmt.Errorf("%s must include at least one title", NotesTitlesEnvironmentVariable)
	}
	return validateDistinctTabTitles(settings.TranscriptTitles, settings.NotesTitles)
}

func (settings ApplicationSettings) validateModelSettings(command Command) error {
	if err := settings.validateUnsupportedModelEnvironment(command); err != nil {
		return err
	}
	if err := settings.validateRequired(command,
		DataModeEnvironmentVariable,
		ModelProviderEnvironmentVariable,
		ModelIDEnvironmentVariable,
	); err != nil {
		return err
	}
	if (settings.Model.Provider == modelpolicy.ProviderOpenAI || settings.Model.Provider == modelpolicy.ProviderAnthropic) &&
		settings.Model.ModelID != strings.TrimSpace(settings.Model.ModelID) {
		return fmt.Errorf("%s must not contain surrounding whitespace for %s", ModelIDEnvironmentVariable, command)
	}
	if err := settings.Model.validateCredentials(command); err != nil {
		return err
	}
	if err := (modelpolicy.Invocation{
		Provider: settings.Model.Provider,
		DataMode: settings.Model.DataMode,
		Region:   settings.Model.invocationRegion(),
	}).Validate(); err != nil {
		return fmt.Errorf("model policy: %w", err)
	}
	if command == CommandDoctor {
		return nil
	}
	if settings.Model.MaxOutputTokens <= 0 {
		return fmt.Errorf("%s must be a positive integer", ModelMaxTokensEnvironmentVariable)
	}
	if settings.Model.MaxAttempts <= 0 || settings.Model.MaxAttempts > defaultModelMaxAttempts {
		return fmt.Errorf("%s must be a positive integer no greater than %d", ModelMaxAttemptsEnvironmentVariable, defaultModelMaxAttempts)
	}
	if err := settings.validateRequired(command,
		ExtractionPromptVersionEnvironmentVariable,
	); err != nil {
		return err
	}
	if settings.ExtractionPromptVersion != extract.ExtractionPromptVersion {
		return fmt.Errorf("%s must be %q; update it and run stacks sync to create current derivations",
			ExtractionPromptVersionEnvironmentVariable, extract.ExtractionPromptVersion)
	}
	return nil
}

func (settings ApplicationSettings) validateUnsupportedModelEnvironment(command Command) error {
	unsupported := make([]string, 0, len(settings.LegacyModelEnvironment))
	for _, name := range unsupportedModelEnvironmentNames {
		for _, configured := range settings.LegacyModelEnvironment {
			if configured == name {
				unsupported = append(unsupported, name)
				break
			}
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("%s are unsupported for %s", strings.Join(unsupported, ", "), command)
}

func (settings ModelSettings) invocationRegion() string {
	if settings.Provider == modelpolicy.ProviderBedrock {
		return settings.AWSRegion
	}
	return ""
}

func (settings ModelSettings) validateCredentials(command Command) error {
	switch settings.Provider {
	case modelpolicy.ProviderBedrock:
		if strings.TrimSpace(settings.AWSRegion) == "" {
			return fmt.Errorf("%s is required for %s", AWSRegionEnvironmentVariable, command)
		}
	case modelpolicy.ProviderOpenAI:
		credential := strings.TrimSpace(settings.OpenAIAPIKey)
		if credential == "" {
			return fmt.Errorf("%s is required for %s", OpenAIAPIKeyEnvironmentVariable, command)
		}
		if settings.OpenAIAPIKey != credential {
			return fmt.Errorf("%s must not contain surrounding whitespace for %s", OpenAIAPIKeyEnvironmentVariable, command)
		}
	case modelpolicy.ProviderAnthropic:
		credential := strings.TrimSpace(settings.AnthropicAPIKey)
		if credential == "" {
			return fmt.Errorf("%s is required for %s", AnthropicAPIKeyEnvironmentVariable, command)
		}
		if settings.AnthropicAPIKey != credential {
			return fmt.Errorf("%s must not contain surrounding whitespace for %s", AnthropicAPIKeyEnvironmentVariable, command)
		}
	}
	return nil
}

func (settings ApplicationSettings) validateRequired(command Command, names ...string) error {
	for _, name := range names {
		if strings.TrimSpace(settings.valueForEnvironment(name)) == "" {
			return fmt.Errorf("%s is required for %s", name, command)
		}
	}
	return nil
}

func validateExactRequired(command Command, name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required for %s", name, command)
	}
	if value != trimmed {
		return fmt.Errorf("%s must not contain surrounding whitespace for %s", name, command)
	}
	return nil
}

func (settings ApplicationSettings) valueForEnvironment(name string) string {
	switch name {
	case GoogleFolderIDEnvironmentVariable:
		return settings.GoogleFolderID
	case GoogleOAuthClientFileEnvironmentVariable:
		return settings.GoogleOAuthClientFile
	case GoogleOAuthTokenFileEnvironmentVariable:
		return settings.GoogleOAuthTokenFile
	case GoogleDirectoryClientFileEnvironmentVariable:
		return settings.Directory.OAuthClientFile
	case GoogleDirectoryTokenFileEnvironmentVariable:
		return settings.Directory.OAuthTokenFile
	case DataModeEnvironmentVariable:
		return string(settings.Model.DataMode)
	case ModelProviderEnvironmentVariable:
		return string(settings.Model.Provider)
	case ModelIDEnvironmentVariable:
		return settings.Model.ModelID
	case ExtractionPromptVersionEnvironmentVariable:
		return settings.ExtractionPromptVersion
	default:
		return ""
	}
}

func validateDistinctTabTitles(transcriptTitles, notesTitles []string) error {
	transcriptSet := normalizedTitleSet(transcriptTitles)
	for title := range normalizedTitleSet(notesTitles) {
		if _, exists := transcriptSet[title]; exists {
			return fmt.Errorf("%s and %s must not overlap after title normalization", TranscriptTitlesEnvironmentVariable, NotesTitlesEnvironmentVariable)
		}
	}
	return nil
}

func normalizedTitleSet(titles []string) map[string]struct{} {
	set := make(map[string]struct{}, len(titles))
	for _, title := range titles {
		normalized := strings.ToLower(strings.Join(strings.Fields(title), " "))
		if normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}
