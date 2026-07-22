package bedrock

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const invocationEventName = "stacks.bedrock.invocation"

// MetricsInvocationRecorder records only bounded invocation metadata as one
// event on the owning span and a small set of distributions.
type MetricsInvocationRecorder struct {
	wallDuration     metric.Float64Histogram
	providerDuration metric.Float64Histogram
	inputTokens      metric.Int64Histogram
	outputTokens     metric.Int64Histogram
	attempts         metric.Int64Histogram
}

// NewMetricsInvocationRecorder constructs reusable Bedrock instruments.
func NewMetricsInvocationRecorder(meter metric.Meter) (*MetricsInvocationRecorder, error) {
	wallDuration, err := meter.Float64Histogram(
		"stacks.bedrock.invocation.wall.duration",
		metric.WithDescription("Wall-clock Bedrock invocation time including retries"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Bedrock wall duration histogram: %w", err)
	}
	providerDuration, err := meter.Float64Histogram(
		"stacks.bedrock.invocation.provider.duration",
		metric.WithDescription("Provider-reported Bedrock model latency"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Bedrock provider duration histogram: %w", err)
	}
	inputTokens, err := meter.Int64Histogram(
		"stacks.bedrock.invocation.input.tokens",
		metric.WithDescription("Bedrock input token count"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Bedrock input token histogram: %w", err)
	}
	outputTokens, err := meter.Int64Histogram(
		"stacks.bedrock.invocation.output.tokens",
		metric.WithDescription("Bedrock output token count"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Bedrock output token histogram: %w", err)
	}
	attempts, err := meter.Int64Histogram(
		"stacks.bedrock.invocation.attempts",
		metric.WithDescription("Bedrock invocation attempt count"),
		metric.WithUnit("{attempt}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Bedrock attempts histogram: %w", err)
	}
	return &MetricsInvocationRecorder{
		wallDuration: wallDuration, providerDuration: providerDuration,
		inputTokens: inputTokens, outputTokens: outputTokens, attempts: attempts,
	}, nil
}

// Record emits no prompt, input, output, private entity, or provider error
// text. Invalid observations are dropped at this non-failing telemetry edge.
func (recorder *MetricsInvocationRecorder) Record(ctx context.Context, observation InvocationObservation) {
	modelID := strings.TrimSpace(observation.ModelID)
	promptVersion := strings.TrimSpace(observation.PromptVersion)
	if promptVersion == "" {
		promptVersion = "invalid"
	}
	if recorder == nil || modelID == "" || !validInvocationOutcome(observation.Outcome) ||
		observation.InputTokens < 0 || observation.OutputTokens < 0 || observation.TotalTokens < 0 ||
		observation.Attempts < 0 || observation.WallLatency < 0 || observation.ProviderLatency < 0 {
		return
	}
	attributes := []attribute.KeyValue{
		attribute.String("stacks.bedrock.model_id", modelID),
		attribute.String("stacks.bedrock.prompt_version", promptVersion),
		attribute.String("stacks.bedrock.outcome", observation.Outcome),
	}
	options := metric.WithAttributes(attributes...)
	recorder.wallDuration.Record(ctx, observation.WallLatency.Seconds(), options)
	recorder.providerDuration.Record(ctx, observation.ProviderLatency.Seconds(), options)
	recorder.inputTokens.Record(ctx, observation.InputTokens, options)
	recorder.outputTokens.Record(ctx, observation.OutputTokens, options)
	recorder.attempts.Record(ctx, int64(observation.Attempts), options)

	trace.SpanFromContext(ctx).AddEvent(invocationEventName, trace.WithAttributes(append(attributes,
		attribute.Int64("stacks.bedrock.input_tokens", observation.InputTokens),
		attribute.Int64("stacks.bedrock.output_tokens", observation.OutputTokens),
		attribute.Int64("stacks.bedrock.total_tokens", observation.TotalTokens),
		attribute.Int("stacks.bedrock.attempts", observation.Attempts),
		attribute.Float64("stacks.bedrock.wall_seconds", observation.WallLatency.Seconds()),
		attribute.Float64("stacks.bedrock.provider_seconds", observation.ProviderLatency.Seconds()),
	)...))
}

func validInvocationOutcome(outcome string) bool {
	switch outcome {
	case OutcomeSuccess, OutcomeThrottled, OutcomeTimeout, OutcomeUnavailable, OutcomeInternal, OutcomeAuthentication,
		OutcomeAccessDenied, OutcomeNotFound, OutcomeInvalidRequest, OutcomeInvalidOutput,
		OutcomeCanceled, OutcomeProviderError:
		return true
	default:
		return false
	}
}

var _ InvocationRecorder = (*MetricsInvocationRecorder)(nil)
