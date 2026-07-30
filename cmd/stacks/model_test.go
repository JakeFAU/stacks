package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/anthropic"
	"stacks/internal/bedrock"
	"stacks/internal/config"
	"stacks/internal/doctor"
	"stacks/internal/modelpolicy"
	"stacks/internal/openai"
)

func TestNewModelSelectsOnlyConfiguredProvider(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-secret-key")
	tracer := tracenoop.NewTracerProvider().Tracer("synthetic")
	tests := []struct {
		name     string
		settings config.ModelSettings
		want     any
	}{
		{
			name: "bedrock",
			settings: config.ModelSettings{
				Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, AWSRegion: "us-east-1",
			},
			want: (*bedrock.Client)(nil),
		},
		{
			name: "openai",
			settings: config.ModelSettings{
				Provider: modelpolicy.ProviderOpenAI, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, OpenAIAPIKey: "synthetic-openai-key",
			},
			want: (*openai.Client)(nil),
		},
		{
			name: "anthropic",
			settings: config.ModelSettings{
				Provider: modelpolicy.ProviderAnthropic, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, AnthropicAPIKey: "synthetic-anthropic-key",
			},
			want: (*anthropic.Client)(nil),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			model, err := newModel(testCase.settings, nil, tracer)
			if err != nil {
				t.Fatalf("newModel() error = %v", err)
			}
			if got, want := typeName(model), typeName(testCase.want); got != want {
				t.Fatalf("newModel() type = %s, want %s", got, want)
			}
		})
	}
}

func TestNewQueryPlannerModelSelectsOnlyConfiguredProvider(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-secret-key")
	tracer := tracenoop.NewTracerProvider().Tracer("synthetic")
	tests := []struct {
		name     string
		settings config.ModelSettings
		want     any
	}{
		{
			name: "bedrock",
			settings: config.ModelSettings{
				Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, AWSRegion: "us-east-1",
			},
			want: (*bedrock.Client)(nil),
		},
		{
			name: "openai",
			settings: config.ModelSettings{
				Provider: modelpolicy.ProviderOpenAI, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, OpenAIAPIKey: "synthetic-openai-key",
			},
			want: (*openai.Client)(nil),
		},
		{
			name: "anthropic",
			settings: config.ModelSettings{
				Provider: modelpolicy.ProviderAnthropic, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, AnthropicAPIKey: "synthetic-anthropic-key",
			},
			want: (*anthropic.Client)(nil),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			model, err := newQueryPlannerModelWithContext(context.Background(), testCase.settings, nil, tracer)
			if err != nil {
				t.Fatalf("newQueryPlannerModelWithContext() error = %v", err)
			}
			if got, want := typeName(model), typeName(testCase.want); got != want {
				t.Fatalf("planner model type = %s, want %s", got, want)
			}
		})
	}
}

func TestNewQueryPlannerModelRejectsUnsupportedProviderWithoutFallback(t *testing.T) {
	settings := config.ModelSettings{Provider: "unsupported", OpenAIAPIKey: "private-openai-key"}
	_, err := newQueryPlannerModelWithContext(context.Background(), settings, nil, tracenoop.NewTracerProvider().Tracer("synthetic"))
	if err == nil || !strings.Contains(err.Error(), "unsupported model provider") {
		t.Fatalf("newQueryPlannerModelWithContext() error = %v, want unsupported provider rejection", err)
	}
	if strings.Contains(err.Error(), settings.OpenAIAPIKey) {
		t.Fatalf("planner model error exposed credential: %v", err)
	}
}

func TestNewDoctorProviderProbeSelectsConfiguredReadOnlyProbe(t *testing.T) {
	tests := []struct {
		provider       modelpolicy.Provider
		settings       config.ModelSettings
		wantModel      any
		wantDisclosure bool
	}{
		{
			provider:  modelpolicy.ProviderBedrock,
			settings:  config.ModelSettings{Provider: modelpolicy.ProviderBedrock, AWSRegion: "us-east-1", ModelID: "synthetic-model"},
			wantModel: (*doctor.AWSProbe)(nil), wantDisclosure: true,
		},
		{
			provider:  modelpolicy.ProviderOpenAI,
			settings:  config.ModelSettings{Provider: modelpolicy.ProviderOpenAI, OpenAIAPIKey: "synthetic-key", ModelID: "synthetic-model"},
			wantModel: (*doctor.OpenAIProbe)(nil),
		},
		{
			provider:  modelpolicy.ProviderAnthropic,
			settings:  config.ModelSettings{Provider: modelpolicy.ProviderAnthropic, AnthropicAPIKey: "synthetic-key", ModelID: "synthetic-model"},
			wantModel: (*doctor.AnthropicProbe)(nil),
		},
	}
	for _, testCase := range tests {
		t.Run(string(testCase.provider), func(t *testing.T) {
			model, disclosure, err := newDoctorProviderProbe(testCase.settings)
			if err != nil {
				t.Fatalf("newDoctorProviderProbe() error = %v", err)
			}
			if got, want := typeName(model), typeName(testCase.wantModel); got != want {
				t.Fatalf("model probe type = %s, want %s", got, want)
			}
			if (disclosure != nil) != testCase.wantDisclosure {
				t.Fatalf("disclosure configured = %t, want %t", disclosure != nil, testCase.wantDisclosure)
			}
		})
	}
}

func TestNewDoctorProviderProbeRejectsUnsupportedProvider(t *testing.T) {
	model, disclosure, err := newDoctorProviderProbe(config.ModelSettings{Provider: "unsupported"})
	if err == nil || model != nil || disclosure != nil {
		t.Fatalf("newDoctorProviderProbe() = (%T, %T, %v), want unsupported rejection", model, disclosure, err)
	}
}

func TestNewModelRejectsUnsupportedProviderWithoutFallback(t *testing.T) {
	settings := config.ModelSettings{
		Provider: "unsupported", DataMode: modelpolicy.DataModePersonal,
		ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1,
		OpenAIAPIKey: "private-openai-key", AnthropicAPIKey: "private-anthropic-key",
	}
	_, err := newModel(settings, nil, tracenoop.NewTracerProvider().Tracer("synthetic"))
	if err == nil || !strings.Contains(err.Error(), "unsupported model provider") {
		t.Fatalf("newModel() error = %v, want unsupported provider rejection", err)
	}
	for _, privateValue := range []string{settings.OpenAIAPIKey, settings.AnthropicAPIKey} {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("newModel() error exposed credential %q: %v", privateValue, err)
		}
	}
}

func TestNewModelRejectsMissingOrMalformedCredentialWithoutSecretValue(t *testing.T) {
	tests := []struct {
		name     string
		settings config.ModelSettings
		secret   string
	}{
		{
			name: "openai missing",
			settings: config.ModelSettings{Provider: modelpolicy.ProviderOpenAI, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1},
		},
		{
			name: "openai malformed",
			settings: config.ModelSettings{Provider: modelpolicy.ProviderOpenAI, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, OpenAIAPIKey: " private-openai-key "},
			secret: "private-openai-key",
		},
		{
			name: "anthropic missing",
			settings: config.ModelSettings{Provider: modelpolicy.ProviderAnthropic, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1},
		},
		{
			name: "anthropic malformed",
			settings: config.ModelSettings{Provider: modelpolicy.ProviderAnthropic, DataMode: modelpolicy.DataModePersonal,
				ModelID: "synthetic-model", MaxOutputTokens: 256, MaxAttempts: 1, AnthropicAPIKey: " private-anthropic-key "},
			secret: "private-anthropic-key",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := newModel(testCase.settings, nil, tracenoop.NewTracerProvider().Tracer("synthetic"))
			if err == nil {
				t.Fatal("newModel() error = nil, want credential rejection")
			}
			if testCase.secret != "" && strings.Contains(err.Error(), testCase.secret) {
				t.Fatalf("newModel() error exposed credential: %v", err)
			}
		})
	}
}

func typeName(value any) string {
	return reflect.TypeOf(value).String()
}
