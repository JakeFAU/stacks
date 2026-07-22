package config

import (
	"strings"
	"testing"

	"stacks/internal/modelpolicy"
)

func TestLoadReadsExplicitModelSettings(t *testing.T) {
	clearModelEnvironment(t)
	t.Setenv(DataModeEnvironmentVariable, string(modelpolicy.DataModePersonal))
	t.Setenv(ModelProviderEnvironmentVariable, string(modelpolicy.ProviderOpenAI))
	t.Setenv(ModelIDEnvironmentVariable, "test-model")
	t.Setenv(ModelMaxTokensEnvironmentVariable, "1000")
	t.Setenv(OpenAIAPIKeyEnvironmentVariable, "synthetic-openai-key")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if settings.PoC.Model.DataMode != modelpolicy.DataModePersonal {
		t.Error("Load() data mode did not retain the explicit personal selection")
	}
	if settings.PoC.Model.Provider != modelpolicy.ProviderOpenAI {
		t.Error("Load() provider did not retain the explicit OpenAI selection")
	}
	if settings.PoC.Model.ModelID != "test-model" || settings.PoC.Model.MaxOutputTokens != 1000 || settings.PoC.Model.MaxAttempts != defaultModelMaxAttempts {
		t.Error("Load() did not retain explicit model settings and the default maximum attempts")
	}
	if settings.PoC.Model.OpenAIAPIKey != "synthetic-openai-key" {
		t.Error("Load() did not retain the configured OpenAI API key in memory")
	}
}

func TestLoadDoesNotExposeProviderCredentialInErrors(t *testing.T) {
	clearModelEnvironment(t)
	const credential = "synthetic-provider-credential"
	t.Setenv(OpenAIAPIKeyEnvironmentVariable, credential)
	t.Setenv(ModelMaxTokensEnvironmentVariable, "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want invalid model token error")
	}
	if strings.Contains(err.Error(), credential) {
		t.Fatalf("Load() error exposed provider credential: %v", err)
	}
}

func TestPoCSettingsValidateKeepsNonModelCommandsLazy(t *testing.T) {
	settings := PoCSettings{}
	if err := settings.Validate(CommandServe); err != nil {
		t.Fatalf("Validate(serve) error = %v, want no model requirement", err)
	}

	settings.GoogleOAuthClientFile = "/tmp/client.json"
	settings.GoogleOAuthTokenFile = "/tmp/token.json"
	if err := settings.Validate(CommandAuth); err != nil {
		t.Fatalf("Validate(auth) error = %v, want no model requirement", err)
	}

	settings = PoCSettings{DatabaseURL: "postgres://synthetic"}
	for _, command := range []Command{CommandEntities, CommandReview} {
		if err := settings.Validate(command); err != nil {
			t.Fatalf("Validate(%s) error = %v, want no model requirement", command, err)
		}
	}
}

func TestPoCSettingsValidateModelCommandsRequireExplicitModelSelection(t *testing.T) {
	for _, command := range []Command{CommandDoctor, CommandSync, CommandAnalyze} {
		t.Run(string(command), func(t *testing.T) {
			settings := validModelCommandSettings()
			settings.Model = ModelSettings{}

			if err := settings.Validate(command); err == nil {
				t.Fatalf("Validate(%s) error = nil, want explicit model selection requirement", command)
			}
		})
	}
}

func TestPoCSettingsValidateRejectsInvalidModelPolicyBeforeProviderBoundary(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*ModelSettings)
	}{
		{"restricted OpenAI", func(model *ModelSettings) {
			model.DataMode = modelpolicy.DataModeRestricted
			model.Provider = modelpolicy.ProviderOpenAI
			model.AWSRegion = ""
			model.OpenAIAPIKey = "synthetic-openai-key"
		}},
		{"restricted Anthropic", func(model *ModelSettings) {
			model.DataMode = modelpolicy.DataModeRestricted
			model.Provider = modelpolicy.ProviderAnthropic
			model.AWSRegion = ""
			model.AnthropicAPIKey = "synthetic-anthropic-key"
		}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			settings := validModelCommandSettings()
			testCase.configure(&settings.Model)

			if err := settings.Validate(CommandSync); err == nil {
				t.Fatal("Validate(sync) error = nil, want restricted direct provider rejection")
			}
		})
	}
}

func TestPoCSettingsValidateRequiresSelectedProviderCredential(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*ModelSettings)
		wantName  string
	}{
		{"Bedrock", func(model *ModelSettings) { model.AWSRegion = "" }, AWSRegionEnvironmentVariable},
		{"OpenAI", func(model *ModelSettings) {
			model.Provider = modelpolicy.ProviderOpenAI
			model.AWSRegion = ""
			model.OpenAIAPIKey = ""
		}, OpenAIAPIKeyEnvironmentVariable},
		{"Anthropic", func(model *ModelSettings) {
			model.Provider = modelpolicy.ProviderAnthropic
			model.AWSRegion = ""
			model.AnthropicAPIKey = ""
		}, AnthropicAPIKeyEnvironmentVariable},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			settings := validModelCommandSettings()
			testCase.configure(&settings.Model)

			err := settings.Validate(CommandSync)
			if err == nil || !strings.Contains(err.Error(), testCase.wantName) {
				t.Fatalf("Validate(sync) error = %v, want %s requirement", err, testCase.wantName)
			}
		})
	}
}

func TestPoCSettingsValidateDirectProviderIgnoresAmbientAWSRegion(t *testing.T) {
	settings := validModelCommandSettings()
	settings.Model.Provider = modelpolicy.ProviderOpenAI
	settings.Model.OpenAIAPIKey = "synthetic-openai-key"

	if err := settings.Validate(CommandSync); err != nil {
		t.Fatalf("Validate(sync) error = %v, want AWS region ignored for direct-provider invocation", err)
	}
}

func TestPoCSettingsRejectsUnsupportedModelEnvironmentNamesWithoutValues(t *testing.T) {
	settings := validModelCommandSettings()
	settings.LegacyModelEnvironment = []string{BedrockModelIDEnvironmentVariable, OpenAIBaseURLEnvironmentVariable}

	err := settings.Validate(CommandSync)
	if err == nil || !strings.Contains(err.Error(), BedrockModelIDEnvironmentVariable) || !strings.Contains(err.Error(), OpenAIBaseURLEnvironmentVariable) {
		t.Fatalf("Validate(sync) error = %v, want unsupported environment names", err)
	}
}

func TestLoadRejectsUnsupportedModelEnvironmentNamesWithoutValues(t *testing.T) {
	clearModelEnvironment(t)
	const legacyValue = "synthetic-legacy-model-value"
	const ambientValue = "synthetic-ambient-routing-value"
	t.Setenv(BedrockModelIDEnvironmentVariable, legacyValue)
	t.Setenv(OpenAIBaseURLEnvironmentVariable, ambientValue)

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	legacyEnvironment := settings.PoC.LegacyModelEnvironment
	settings.PoC = validModelCommandSettings()
	settings.PoC.LegacyModelEnvironment = legacyEnvironment

	err = settings.PoC.Validate(CommandSync)
	if err == nil || !strings.Contains(err.Error(), BedrockModelIDEnvironmentVariable) || !strings.Contains(err.Error(), OpenAIBaseURLEnvironmentVariable) {
		t.Fatalf("Validate(sync) error = %v, want unsupported environment names", err)
	}
	if strings.Contains(err.Error(), legacyValue) || strings.Contains(err.Error(), ambientValue) {
		t.Fatalf("Validate(sync) error exposed unsupported environment value: %v", err)
	}
}

func TestPoCSettingsValidateBoundsModelTokensAndAttempts(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		configure func(*ModelSettings)
	}{
		{"zero tokens", func(model *ModelSettings) { model.MaxOutputTokens = 0 }},
		{"zero attempts", func(model *ModelSettings) { model.MaxAttempts = 0 }},
		{"too many attempts", func(model *ModelSettings) { model.MaxAttempts = defaultModelMaxAttempts + 1 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings := validModelCommandSettings()
			testCase.configure(&settings.Model)
			if err := settings.Validate(CommandSync); err == nil {
				t.Fatal("Validate(sync) error = nil, want model bound rejection")
			}
		})
	}
}

func validModelCommandSettings() PoCSettings {
	return validPoCSettings()
}

func clearModelEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		DataModeEnvironmentVariable,
		ModelProviderEnvironmentVariable,
		ModelIDEnvironmentVariable,
		ModelMaxTokensEnvironmentVariable,
		ModelMaxAttemptsEnvironmentVariable,
		OpenAIAPIKeyEnvironmentVariable,
		AnthropicAPIKeyEnvironmentVariable,
		BedrockModelIDEnvironmentVariable,
		BedrockMaxTokensEnvironmentVariable,
		BedrockMaxAttemptsEnvironmentVariable,
		OpenAIBaseURLEnvironmentVariable,
		OpenAIOrganizationIDEnvironmentVariable,
		OpenAIProjectIDEnvironmentVariable,
		AnthropicBaseURLEnvironmentVariable,
		AnthropicAuthTokenEnvironmentVariable,
		AnthropicProfileEnvironmentVariable,
	} {
		t.Setenv(name, "")
	}
}
