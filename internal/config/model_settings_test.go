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

	if settings.Application.Model.DataMode != modelpolicy.DataModePersonal {
		t.Error("Load() data mode did not retain the explicit personal selection")
	}
	if settings.Application.Model.Provider != modelpolicy.ProviderOpenAI {
		t.Error("Load() provider did not retain the explicit OpenAI selection")
	}
	if settings.Application.Model.ModelID != "test-model" || settings.Application.Model.MaxOutputTokens != 1000 || settings.Application.Model.MaxAttempts != defaultModelMaxAttempts {
		t.Error("Load() did not retain explicit model settings and the default maximum attempts")
	}
	if settings.Application.Model.OpenAIAPIKey != "synthetic-openai-key" {
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

func TestApplicationSettingsValidateKeepsNonModelCommandsLazy(t *testing.T) {
	settings := ApplicationSettings{}
	if err := settings.Validate(CommandServe); err != nil {
		t.Fatalf("Validate(serve) error = %v, want no model requirement", err)
	}

	settings.GoogleOAuthClientFile = "/tmp/client.json"
	settings.GoogleOAuthTokenFile = "/tmp/token.json"
	if err := settings.Validate(CommandAuth); err != nil {
		t.Fatalf("Validate(auth) error = %v, want no model requirement", err)
	}

	settings = ApplicationSettings{}
	for _, command := range []Command{CommandEntities, CommandReview} {
		if err := settings.Validate(command); err != nil {
			t.Fatalf("Validate(%s) error = %v, want no model requirement", command, err)
		}
	}
}

func TestApplicationSettingsValidateModelCommandsRequireExplicitModelSelection(t *testing.T) {
	for _, command := range []Command{CommandDoctor, CommandSync} {
		t.Run(string(command), func(t *testing.T) {
			settings := validModelCommandSettings()
			settings.Model = ModelSettings{}

			if err := settings.Validate(command); err == nil {
				t.Fatalf("Validate(%s) error = nil, want explicit model selection requirement", command)
			}
		})
	}
}

func TestApplicationSettingsValidateRejectsInvalidModelPolicyBeforeProviderBoundary(t *testing.T) {
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

func TestApplicationSettingsValidateRequiresSelectedProviderCredential(t *testing.T) {
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

func TestApplicationSettingsValidateRejectsPaddedDirectProviderSettingsWithoutValues(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*ModelSettings)
		wantName  string
		private   string
	}{
		{
			name: "OpenAI API key",
			configure: func(model *ModelSettings) {
				model.Provider = modelpolicy.ProviderOpenAI
				model.AWSRegion = ""
				model.OpenAIAPIKey = " private-openai-key "
			},
			wantName: OpenAIAPIKeyEnvironmentVariable,
			private:  "private-openai-key",
		},
		{
			name: "Anthropic API key",
			configure: func(model *ModelSettings) {
				model.Provider = modelpolicy.ProviderAnthropic
				model.AWSRegion = ""
				model.AnthropicAPIKey = " private-anthropic-key "
			},
			wantName: AnthropicAPIKeyEnvironmentVariable,
			private:  "private-anthropic-key",
		},
		{
			name: "OpenAI model ID",
			configure: func(model *ModelSettings) {
				model.Provider = modelpolicy.ProviderOpenAI
				model.AWSRegion = ""
				model.OpenAIAPIKey = "synthetic-openai-key"
				model.ModelID = " padded-openai-model "
			},
			wantName: ModelIDEnvironmentVariable,
			private:  "padded-openai-model",
		},
		{
			name: "Anthropic model ID",
			configure: func(model *ModelSettings) {
				model.Provider = modelpolicy.ProviderAnthropic
				model.AWSRegion = ""
				model.AnthropicAPIKey = "synthetic-anthropic-key"
				model.ModelID = " padded-anthropic-model "
			},
			wantName: ModelIDEnvironmentVariable,
			private:  "padded-anthropic-model",
		},
	}

	for _, command := range []Command{CommandSync} {
		for _, testCase := range tests {
			t.Run(string(command)+"/"+testCase.name, func(t *testing.T) {
				settings := validModelCommandSettings()
				testCase.configure(&settings.Model)

				err := settings.Validate(command)
				if err == nil || !strings.Contains(err.Error(), testCase.wantName) {
					t.Fatalf("Validate(%s) error = %v, want bounded %s rejection", command, err, testCase.wantName)
				}
				if strings.Contains(err.Error(), testCase.private) {
					t.Fatalf("Validate(%s) error exposed configured value: %v", command, err)
				}
			})
		}
	}
}

func TestApplicationSettingsValidateAcceptsExactDirectProviderSettings(t *testing.T) {
	for _, provider := range []modelpolicy.Provider{modelpolicy.ProviderOpenAI, modelpolicy.ProviderAnthropic} {
		t.Run(string(provider), func(t *testing.T) {
			settings := validModelCommandSettings()
			settings.Model.Provider = provider
			settings.Model.AWSRegion = ""
			settings.Model.ModelID = "exact-model-id"
			if provider == modelpolicy.ProviderOpenAI {
				settings.Model.OpenAIAPIKey = "exact-openai-key"
			} else {
				settings.Model.AnthropicAPIKey = "exact-anthropic-key"
			}
			if err := settings.Validate(CommandSync); err != nil {
				t.Fatalf("Validate(sync) error = %v, want exact direct-provider settings accepted", err)
			}
		})
	}
}

func TestApplicationSettingsValidateDirectProviderIgnoresAmbientAWSRegion(t *testing.T) {
	settings := validModelCommandSettings()
	settings.Model.Provider = modelpolicy.ProviderOpenAI
	settings.Model.OpenAIAPIKey = "synthetic-openai-key"

	if err := settings.Validate(CommandSync); err != nil {
		t.Fatalf("Validate(sync) error = %v, want AWS region ignored for direct-provider invocation", err)
	}
}

func TestApplicationSettingsRejectsUnsupportedModelEnvironmentNamesWithoutValues(t *testing.T) {
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
	legacyEnvironment := settings.Application.LegacyModelEnvironment
	settings.Application = validModelCommandSettings()
	settings.Application.LegacyModelEnvironment = legacyEnvironment

	err = settings.Application.Validate(CommandSync)
	if err == nil || !strings.Contains(err.Error(), BedrockModelIDEnvironmentVariable) || !strings.Contains(err.Error(), OpenAIBaseURLEnvironmentVariable) {
		t.Fatalf("Validate(sync) error = %v, want unsupported environment names", err)
	}
	if strings.Contains(err.Error(), legacyValue) || strings.Contains(err.Error(), ambientValue) {
		t.Fatalf("Validate(sync) error exposed unsupported environment value: %v", err)
	}
}

func TestApplicationSettingsValidateBoundsModelTokensAndAttempts(t *testing.T) {
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

func validModelCommandSettings() ApplicationSettings {
	return validApplicationSettings()
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
