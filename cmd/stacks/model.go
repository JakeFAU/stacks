package main

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"

	"stacks/internal/anthropic"
	"stacks/internal/bedrock"
	"stacks/internal/config"
	"stacks/internal/doctor"
	"stacks/internal/extract"
	"stacks/internal/modelpolicy"
	"stacks/internal/modeltelemetry"
	"stacks/internal/openai"
	"stacks/internal/queryplan"
)

type queryPlannerModelFactory func(
	context.Context,
	config.ModelSettings,
	modeltelemetry.Recorder,
	trace.Tracer,
) (queryplan.Model, error)

func newModel(settings config.ModelSettings, recorder modeltelemetry.Recorder, tracer trace.Tracer) (extract.Model, error) {
	return newModelWithContext(context.Background(), settings, recorder, tracer)
}

func newModelWithContext(ctx context.Context, settings config.ModelSettings, recorder modeltelemetry.Recorder, tracer trace.Tracer) (extract.Model, error) {
	switch settings.Provider {
	case modelpolicy.ProviderBedrock:
		configuration, err := loadAWSConfiguration(ctx, settings.AWSProfile, settings.AWSRegion)
		if err != nil {
			return nil, err
		}
		return bedrock.NewFromConfig(configuration, bedrock.Options{
			ModelID: settings.ModelID, DataMode: settings.DataMode, MaxTokens: settings.MaxOutputTokens,
			MaxAttempts: settings.MaxAttempts, Recorder: recorder, Tracer: tracer,
		})
	case modelpolicy.ProviderOpenAI:
		return openai.New(settings.OpenAIAPIKey, openai.Options{
			ModelID: settings.ModelID, DataMode: settings.DataMode, MaxOutputTokens: settings.MaxOutputTokens,
			MaxAttempts: settings.MaxAttempts, Recorder: recorder, Tracer: tracer,
		})
	case modelpolicy.ProviderAnthropic:
		return anthropic.New(settings.AnthropicAPIKey, anthropic.Options{
			ModelID: settings.ModelID, DataMode: settings.DataMode, MaxOutputTokens: settings.MaxOutputTokens,
			MaxAttempts: settings.MaxAttempts, Recorder: recorder, Tracer: tracer,
		})
	default:
		return nil, fmt.Errorf("unsupported model provider")
	}
}

func newQueryPlannerModelWithContext(
	ctx context.Context,
	settings config.ModelSettings,
	recorder modeltelemetry.Recorder,
	tracer trace.Tracer,
) (queryplan.Model, error) {
	switch settings.Provider {
	case modelpolicy.ProviderBedrock:
		configuration, err := loadAWSConfiguration(ctx, settings.AWSProfile, settings.AWSRegion)
		if err != nil {
			return nil, err
		}
		return bedrock.NewFromConfig(configuration, bedrock.Options{
			ModelID: settings.ModelID, DataMode: settings.DataMode, MaxTokens: settings.MaxOutputTokens,
			MaxAttempts: settings.MaxAttempts, Recorder: recorder, Tracer: tracer,
		})
	case modelpolicy.ProviderOpenAI:
		return openai.New(settings.OpenAIAPIKey, openai.Options{
			ModelID: settings.ModelID, DataMode: settings.DataMode, MaxOutputTokens: settings.MaxOutputTokens,
			MaxAttempts: settings.MaxAttempts, Recorder: recorder, Tracer: tracer,
		})
	case modelpolicy.ProviderAnthropic:
		return anthropic.New(settings.AnthropicAPIKey, anthropic.Options{
			ModelID: settings.ModelID, DataMode: settings.DataMode, MaxOutputTokens: settings.MaxOutputTokens,
			MaxAttempts: settings.MaxAttempts, Recorder: recorder, Tracer: tracer,
		})
	default:
		return nil, fmt.Errorf("unsupported model provider")
	}
}

func newDoctorProviderProbe(settings config.ModelSettings) (doctor.ModelProbe, doctor.DisclosureProbe, error) {
	switch settings.Provider {
	case modelpolicy.ProviderBedrock:
		probe := doctor.NewAWSProbe(settings.AWSProfile, settings.AWSRegion, settings.ModelID)
		return probe, probe, nil
	case modelpolicy.ProviderOpenAI:
		return doctor.NewOpenAIProbe(settings.OpenAIAPIKey, settings.ModelID), nil, nil
	case modelpolicy.ProviderAnthropic:
		return doctor.NewAnthropicProbe(settings.AnthropicAPIKey, settings.ModelID), nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported model provider")
	}
}
