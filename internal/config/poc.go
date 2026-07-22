package config

import (
	"fmt"
	"strings"
)

const (
	defaultBedrockMaxAttempts      = 5
	defaultExtractionPromptVersion = "extract-v1"
	defaultAnalysisPromptVersion   = "analyze-v1"

	DatabaseURLEnvironmentVariable             = "STACKS_DATABASE_URL"
	GoogleFolderIDEnvironmentVariable          = "STACKS_GOOGLE_FOLDER_ID"
	GoogleOAuthClientFileEnvironmentVariable   = "STACKS_GOOGLE_OAUTH_CLIENT_FILE"
	GoogleOAuthTokenFileEnvironmentVariable    = "STACKS_GOOGLE_OAUTH_TOKEN_FILE"
	TranscriptTitlesEnvironmentVariable        = "STACKS_TRANSCRIPT_TITLES"
	NotesTitlesEnvironmentVariable             = "STACKS_NOTES_TITLES"
	AWSProfileEnvironmentVariable              = "STACKS_AWS_PROFILE"
	AWSRegionEnvironmentVariable               = "STACKS_AWS_REGION"
	BedrockModelIDEnvironmentVariable          = "STACKS_BEDROCK_MODEL_ID"
	BedrockMaxTokensEnvironmentVariable        = "STACKS_BEDROCK_MAX_TOKENS"
	BedrockMaxAttemptsEnvironmentVariable      = "STACKS_BEDROCK_MAX_ATTEMPTS"
	ExtractionPromptVersionEnvironmentVariable = "STACKS_EXTRACTION_PROMPT_VERSION"
	AnalysisPromptVersionEnvironmentVariable   = "STACKS_ANALYSIS_PROMPT_VERSION"
	EmployeeEntityIDEnvironmentVariable        = "STACKS_EMPLOYEE_ENTITY_ID"
	ManagerEntityIDEnvironmentVariable         = "STACKS_MANAGER_ENTITY_ID"
)

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
	AWSProfile              string
	AWSRegion               string
	BedrockModelID          string
	BedrockMaxTokens        int
	BedrockMaxAttempts      int
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
			AWSProfileEnvironmentVariable,
			AWSRegionEnvironmentVariable,
			BedrockModelIDEnvironmentVariable,
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
		AWSRegionEnvironmentVariable,
		BedrockModelIDEnvironmentVariable,
	); err != nil {
		return err
	}
	return settings.validateCorpusTitles()
}

func (settings PoCSettings) validateCorpusAndModel(command Command) error {
	if err := settings.validateRequired(command,
		DatabaseURLEnvironmentVariable,
		GoogleFolderIDEnvironmentVariable,
		GoogleOAuthClientFileEnvironmentVariable,
		GoogleOAuthTokenFileEnvironmentVariable,
		AWSProfileEnvironmentVariable,
		AWSRegionEnvironmentVariable,
		BedrockModelIDEnvironmentVariable,
	); err != nil {
		return err
	}
	if err := settings.validateCorpusTitles(); err != nil {
		return err
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
	if err := validateDistinctTabTitles(settings.TranscriptTitles, settings.NotesTitles); err != nil {
		return err
	}
	return nil
}

func (settings PoCSettings) validateModelSettings(command Command) error {
	if settings.BedrockMaxTokens <= 0 {
		return fmt.Errorf("%s must be a positive integer", BedrockMaxTokensEnvironmentVariable)
	}
	if settings.BedrockMaxAttempts <= 0 {
		return fmt.Errorf("%s must be a positive integer", BedrockMaxAttemptsEnvironmentVariable)
	}
	return settings.validateRequired(command,
		ExtractionPromptVersionEnvironmentVariable,
		AnalysisPromptVersionEnvironmentVariable,
	)
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
	case AWSProfileEnvironmentVariable:
		return settings.AWSProfile
	case AWSRegionEnvironmentVariable:
		return settings.AWSRegion
	case BedrockModelIDEnvironmentVariable:
		return settings.BedrockModelID
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
