// Package modeltelemetry records bounded operational metadata for model
// invocations. It deliberately excludes private requests, responses, and
// provider errors.
package modeltelemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"stacks/internal/modelpolicy"
)

const (
	invocationEventName = "stacks.model.invocation"

	OutcomeSuccess        = "success"
	OutcomeThrottled      = "throttled"
	OutcomeTimeout        = "timeout"
	OutcomeUnavailable    = "unavailable"
	OutcomeInternal       = "internal_error"
	OutcomeAuthentication = "authentication_error"
	OutcomeAccessDenied   = "access_denied"
	OutcomeNotFound       = "not_found"
	OutcomeInvalidRequest = "invalid_request"
	OutcomeInvalidOutput  = "invalid_output"
	OutcomeCanceled       = "canceled"
	OutcomeProviderError  = "provider_error"
)

// Observation is the bounded telemetry emitted for one completed model
// invocation. It deliberately excludes request and response text, provider
// errors, credentials, and source-derived metadata.
type Observation struct {
	Provider        modelpolicy.Provider
	DataMode        modelpolicy.DataMode
	ModelID         string
	PromptVersion   string
	Outcome         string
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	WallLatency     time.Duration
	ProviderLatency time.Duration
	Attempts        int
}

// Recorder receives privacy-safe model invocation telemetry.
type Recorder interface {
	Record(context.Context, Observation)
}

// MetricsRecorder records a bounded event on the owning span and a small set
// of distributions. It never creates a child span.
type MetricsRecorder struct {
	wallDuration     metric.Float64Histogram
	providerDuration metric.Float64Histogram
	inputTokens      metric.Int64Histogram
	outputTokens     metric.Int64Histogram
	totalTokens      metric.Int64Histogram
	attempts         metric.Int64Histogram
}

// NewMetricsRecorder constructs reusable model invocation instruments.
func NewMetricsRecorder(meter metric.Meter) (*MetricsRecorder, error) {
	wallDuration, err := meter.Float64Histogram(
		"stacks.model.invocation.wall.duration",
		metric.WithDescription("Wall-clock model invocation time including retries"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create model wall duration histogram: %w", err)
	}
	providerDuration, err := meter.Float64Histogram(
		"stacks.model.invocation.provider.duration",
		metric.WithDescription("Provider-reported model latency"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create model provider duration histogram: %w", err)
	}
	inputTokens, err := meter.Int64Histogram(
		"stacks.model.invocation.input.tokens",
		metric.WithDescription("Model input token count"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create model input token histogram: %w", err)
	}
	outputTokens, err := meter.Int64Histogram(
		"stacks.model.invocation.output.tokens",
		metric.WithDescription("Model output token count"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create model output token histogram: %w", err)
	}
	totalTokens, err := meter.Int64Histogram(
		"stacks.model.invocation.total.tokens",
		metric.WithDescription("Model total token count"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create model total token histogram: %w", err)
	}
	attempts, err := meter.Int64Histogram(
		"stacks.model.invocation.attempts",
		metric.WithDescription("Model invocation attempt count"),
		metric.WithUnit("{attempt}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create model attempts histogram: %w", err)
	}
	return &MetricsRecorder{
		wallDuration: wallDuration, providerDuration: providerDuration,
		inputTokens: inputTokens, outputTokens: outputTokens, totalTokens: totalTokens, attempts: attempts,
	}, nil
}

// Record emits only bounded model metadata. Invalid observations are dropped
// because telemetry must not interrupt the owning operation.
func (recorder *MetricsRecorder) Record(ctx context.Context, observation Observation) {
	if recorder == nil || !observation.valid() {
		return
	}
	attributes := []attribute.KeyValue{
		attribute.String("stacks.model.provider", string(observation.Provider)),
		attribute.String("stacks.model.data_mode", string(observation.DataMode)),
		attribute.String("stacks.model.model_id", strings.TrimSpace(observation.ModelID)),
		attribute.String("stacks.model.prompt_version", strings.TrimSpace(observation.PromptVersion)),
		attribute.String("stacks.model.outcome", observation.Outcome),
	}
	options := metric.WithAttributes(attributes...)
	recorder.wallDuration.Record(ctx, observation.WallLatency.Seconds(), options)
	recorder.providerDuration.Record(ctx, observation.ProviderLatency.Seconds(), options)
	recorder.inputTokens.Record(ctx, observation.InputTokens, options)
	recorder.outputTokens.Record(ctx, observation.OutputTokens, options)
	recorder.totalTokens.Record(ctx, observation.TotalTokens, options)
	recorder.attempts.Record(ctx, int64(observation.Attempts), options)

	trace.SpanFromContext(ctx).AddEvent(invocationEventName, trace.WithAttributes(append(attributes,
		attribute.Int64("stacks.model.input_tokens", observation.InputTokens),
		attribute.Int64("stacks.model.output_tokens", observation.OutputTokens),
		attribute.Int64("stacks.model.total_tokens", observation.TotalTokens),
		attribute.Int("stacks.model.attempts", observation.Attempts),
		attribute.Float64("stacks.model.wall_seconds", observation.WallLatency.Seconds()),
		attribute.Float64("stacks.model.provider_seconds", observation.ProviderLatency.Seconds()),
	)...))
}

func (observation Observation) valid() bool {
	return observation.Provider.Valid() && observation.DataMode.ValidForNewRun() &&
		strings.TrimSpace(observation.ModelID) != "" && strings.TrimSpace(observation.PromptVersion) != "" &&
		validOutcome(observation.Outcome) && observation.InputTokens >= 0 && observation.OutputTokens >= 0 &&
		observation.TotalTokens >= 0 && observation.WallLatency >= 0 && observation.ProviderLatency >= 0 &&
		observation.Attempts >= 0
}

func validOutcome(outcome string) bool {
	switch outcome {
	case OutcomeSuccess, OutcomeThrottled, OutcomeTimeout, OutcomeUnavailable, OutcomeInternal, OutcomeAuthentication,
		OutcomeAccessDenied, OutcomeNotFound, OutcomeInvalidRequest, OutcomeInvalidOutput, OutcomeCanceled, OutcomeProviderError:
		return true
	default:
		return false
	}
}

var _ Recorder = (*MetricsRecorder)(nil)
