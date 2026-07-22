package config

import (
	"fmt"
	"strings"
	"time"

	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
)

const (
	defaultModelMaxAttempts        = 5
	defaultExtractionPromptVersion = extract.ExtractionPromptVersion
	defaultAnalysisPromptVersion   = extract.AnalysisPromptVersion
	defaultIngestionLeaseDuration  = 5 * time.Minute
	defaultIngestionAttemptTimeout = 4 * time.Minute
	maximumIngestionLeaseDuration  = time.Hour
	minimumLeaseCleanupMargin      = 5 * time.Second

	DatabaseURLEnvironmentVariable             = "STACKS_DATABASE_URL"
	GoogleFolderIDEnvironmentVariable          = "STACKS_GOOGLE_FOLDER_ID"
	GoogleOAuthClientFileEnvironmentVariable   = "STACKS_GOOGLE_OAUTH_CLIENT_FILE"
	GoogleOAuthTokenFileEnvironmentVariable    = "STACKS_GOOGLE_OAUTH_TOKEN_FILE"
	TranscriptTitlesEnvironmentVariable        = "STACKS_TRANSCRIPT_TITLES"
	NotesTitlesEnvironmentVariable             = "STACKS_NOTES_TITLES"
	AWSProfileEnvironmentVariable              = "STACKS_AWS_PROFILE"
	AWSRegionEnvironmentVariable               = "STACKS_AWS_REGION"
	DataModeEnvironmentVariable                = "STACKS_DATA_MODE"
	ModelProviderEnvironmentVariable           = "STACKS_MODEL_PROVIDER"
	ModelIDEnvironmentVariable                 = "STACKS_MODEL_ID"
	ModelMaxTokensEnvironmentVariable          = "STACKS_MODEL_MAX_OUTPUT_TOKENS"
	ModelMaxAttemptsEnvironmentVariable        = "STACKS_MODEL_MAX_ATTEMPTS"
	OpenAIAPIKeyEnvironmentVariable            = "OPENAI_API_KEY"
	AnthropicAPIKeyEnvironmentVariable         = "ANTHROPIC_API_KEY"
	OpenAIBaseURLEnvironmentVariable           = "OPENAI_BASE_URL"
	OpenAIOrganizationIDEnvironmentVariable    = "OPENAI_ORG_ID"
	OpenAIProjectIDEnvironmentVariable         = "OPENAI_PROJECT_ID"
	AnthropicBaseURLEnvironmentVariable        = "ANTHROPIC_BASE_URL"
	AnthropicAuthTokenEnvironmentVariable      = "ANTHROPIC_AUTH_TOKEN"
	AnthropicProfileEnvironmentVariable        = "ANTHROPIC_PROFILE"
	BedrockModelIDEnvironmentVariable          = "STACKS_BEDROCK_MODEL_ID"
	BedrockMaxTokensEnvironmentVariable        = "STACKS_BEDROCK_MAX_TOKENS"
	BedrockMaxAttemptsEnvironmentVariable      = "STACKS_BEDROCK_MAX_ATTEMPTS"
	IngestionLeaseDurationEnvironmentVariable  = "STACKS_INGEST_LEASE_DURATION"
	IngestionAttemptTimeoutEnvironmentVariable = "STACKS_INGEST_ATTEMPT_TIMEOUT"
	ExtractionPromptVersionEnvironmentVariable = "STACKS_EXTRACTION_PROMPT_VERSION"
	AnalysisPromptVersionEnvironmentVariable   = "STACKS_ANALYSIS_PROMPT_VERSION"
	EmployeeEntityIDEnvironmentVariable        = "STACKS_EMPLOYEE_ENTITY_ID"
	ManagerEntityIDEnvironmentVariable         = "STACKS_MANAGER_ENTITY_ID"
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
	CommandServe    Command = "serve"
	CommandAuth     Command = "auth"
	CommandDoctor   Command = "doctor"
	CommandSync     Command = "sync"
	CommandEntities Command = "entities"
	CommandReview   Command = "review"
	CommandAnalyze  Command = "analyze"
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

// PoCSettings holds the command-specific settings for the manager-confidence
// proof of concept. Values are loaded without command validation so serving
// health traffic remains possible before the PoC is configured.
type PoCSettings struct {
	DatabaseURL             string
	GoogleFolderID          string
	GoogleOAuthClientFile   string
	GoogleOAuthTokenFile    string
	TranscriptTitles        []string
	NotesTitles             []string
	Model                   ModelSettings
	LegacyModelEnvironment  []string
	IngestionLeaseDuration  time.Duration
	IngestionAttemptTimeout time.Duration
	ExtractionPromptVersion string
	AnalysisPromptVersion   string
	EmployeeEntityID        string
	ManagerEntityID         string
}

// Validate verifies only the settings required by command. Serve intentionally
// requires no PoC configuration, preserving the existing no-argument server.
func (settings PoCSettings) Validate(command Command) error {
	switch command {
	case CommandServe, "":
		return nil
	case CommandAuth:
		return settings.validateRequired(command,
			GoogleOAuthClientFileEnvironmentVariable,
			GoogleOAuthTokenFileEnvironmentVariable,
		)
	case CommandDoctor:
		return settings.validateDoctor(command)
	case CommandSync:
		return settings.validateCorpusAndModel(command)
	case CommandEntities, CommandReview:
		return settings.validateRequired(command, DatabaseURLEnvironmentVariable)
	case CommandAnalyze:
		if err := settings.validateRequired(command,
			DatabaseURLEnvironmentVariable,
			EmployeeEntityIDEnvironmentVariable,
			ManagerEntityIDEnvironmentVariable,
		); err != nil {
			return err
		}
		return settings.validateModelSettings(command)
	default:
		return nil
	}
}

func (settings PoCSettings) validateDoctor(command Command) error {
	if err := settings.validateRequired(command,
		DatabaseURLEnvironmentVariable,
		GoogleFolderIDEnvironmentVariable,
		GoogleOAuthClientFileEnvironmentVariable,
		GoogleOAuthTokenFileEnvironmentVariable,
	); err != nil {
		return err
	}
	if err := settings.validateCorpusTitles(); err != nil {
		return err
	}
	return settings.validateModelSettings(command)
}

func (settings PoCSettings) validateCorpusAndModel(command Command) error {
	if err := settings.validateRequired(command,
		DatabaseURLEnvironmentVariable,
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
	return settings.validateModelSettings(command)
}

func (settings PoCSettings) validateCorpusTitles() error {
	if len(normalizedTitleSet(settings.TranscriptTitles)) == 0 {
		return fmt.Errorf("%s must include at least one title", TranscriptTitlesEnvironmentVariable)
	}
	if len(normalizedTitleSet(settings.NotesTitles)) == 0 {
		return fmt.Errorf("%s must include at least one title", NotesTitlesEnvironmentVariable)
	}
	return validateDistinctTabTitles(settings.TranscriptTitles, settings.NotesTitles)
}

func (settings PoCSettings) validateModelSettings(command Command) error {
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
		AnalysisPromptVersionEnvironmentVariable,
	); err != nil {
		return err
	}
	if settings.ExtractionPromptVersion != extract.ExtractionPromptVersion {
		return fmt.Errorf("%s must be %q; update it and run stacks sync to create current derivations",
			ExtractionPromptVersionEnvironmentVariable, extract.ExtractionPromptVersion)
	}
	if settings.AnalysisPromptVersion != extract.AnalysisPromptVersion {
		return fmt.Errorf("%s must be %q; update it and run stacks sync before analysis",
			AnalysisPromptVersionEnvironmentVariable, extract.AnalysisPromptVersion)
	}
	return nil
}

func (settings PoCSettings) validateUnsupportedModelEnvironment(command Command) error {
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
		if strings.TrimSpace(settings.OpenAIAPIKey) == "" {
			return fmt.Errorf("%s is required for %s", OpenAIAPIKeyEnvironmentVariable, command)
		}
	case modelpolicy.ProviderAnthropic:
		if strings.TrimSpace(settings.AnthropicAPIKey) == "" {
			return fmt.Errorf("%s is required for %s", AnthropicAPIKeyEnvironmentVariable, command)
		}
	}
	return nil
}

func (settings PoCSettings) validateRequired(command Command, names ...string) error {
	for _, name := range names {
		if strings.TrimSpace(settings.valueForEnvironment(name)) == "" {
			return fmt.Errorf("%s is required for %s", name, command)
		}
	}
	return nil
}

func (settings PoCSettings) valueForEnvironment(name string) string {
	switch name {
	case DatabaseURLEnvironmentVariable:
		return settings.DatabaseURL
	case GoogleFolderIDEnvironmentVariable:
		return settings.GoogleFolderID
	case GoogleOAuthClientFileEnvironmentVariable:
		return settings.GoogleOAuthClientFile
	case GoogleOAuthTokenFileEnvironmentVariable:
		return settings.GoogleOAuthTokenFile
	case DataModeEnvironmentVariable:
		return string(settings.Model.DataMode)
	case ModelProviderEnvironmentVariable:
		return string(settings.Model.Provider)
	case ModelIDEnvironmentVariable:
		return settings.Model.ModelID
	case ExtractionPromptVersionEnvironmentVariable:
		return settings.ExtractionPromptVersion
	case AnalysisPromptVersionEnvironmentVariable:
		return settings.AnalysisPromptVersion
	case EmployeeEntityIDEnvironmentVariable:
		return settings.EmployeeEntityID
	case ManagerEntityIDEnvironmentVariable:
		return settings.ManagerEntityID
	default:
		return ""
	}
}

func validateDistinctTabTitles(transcriptTitles, notesTitles []string) error {
	transcriptSet := normalizedTitleSet(transcriptTitles)
	for title := range normalizedTitleSet(notesTitles) {
		if _, exists := transcriptSet[title]; exists {
			return fmt.Errorf("%s and %s must not overlap at normalized title %q", TranscriptTitlesEnvironmentVariable, NotesTitlesEnvironmentVariable, title)
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
