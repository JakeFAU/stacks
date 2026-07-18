package observability

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const decisionEventName = "stacks.decision"

// DecisionObservation describes one completed operational decision. Name and
// outcome must be bounded, low-cardinality values; never use document text,
// prompts, IDs, or other private/user-controlled values.
type DecisionObservation struct {
	Name       string
	Outcome    string
	Duration   time.Duration
	InputSize  int64
	OutputSize int64
	Confidence *float64
}

// DecisionRecorder observes a decision as an event on the current span and as
// distributions. It deliberately does not create a child span.
type DecisionRecorder struct {
	duration   metric.Float64Histogram
	inputSize  metric.Int64Histogram
	outputSize metric.Int64Histogram
	confidence metric.Float64Histogram
}

// NewDecisionRecorder constructs decision instruments once for reuse.
func NewDecisionRecorder(meter metric.Meter) (*DecisionRecorder, error) {
	duration, err := meter.Float64Histogram(
		"stacks.decision.duration",
		metric.WithDescription("Elapsed decision time"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("create decision duration histogram: %w", err)
	}
	inputSize, err := meter.Int64Histogram(
		"stacks.decision.input.size",
		metric.WithDescription("Number of candidates considered by a decision"),
		metric.WithUnit("{item}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create decision input size histogram: %w", err)
	}
	outputSize, err := meter.Int64Histogram(
		"stacks.decision.output.size",
		metric.WithDescription("Number of candidates retained by a decision"),
		metric.WithUnit("{item}"),
	)
	if err != nil {
		return nil, fmt.Errorf("create decision output size histogram: %w", err)
	}
	confidence, err := meter.Float64Histogram(
		"stacks.decision.confidence",
		metric.WithDescription("Confidence reported by decisions that produce one"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return nil, fmt.Errorf("create decision confidence histogram: %w", err)
	}

	return &DecisionRecorder{
		duration:   duration,
		inputSize:  inputSize,
		outputSize: outputSize,
		confidence: confidence,
	}, nil
}

// Record validates and records an observation.
func (r *DecisionRecorder) Record(ctx context.Context, observation DecisionObservation) error {
	if err := observation.validate(); err != nil {
		return err
	}

	attributes := []attribute.KeyValue{
		attribute.String("stacks.decision.name", observation.Name),
		attribute.String("stacks.decision.outcome", observation.Outcome),
	}
	metricOptions := metric.WithAttributes(attributes...)
	r.duration.Record(ctx, observation.Duration.Seconds(), metricOptions)
	r.inputSize.Record(ctx, observation.InputSize, metricOptions)
	r.outputSize.Record(ctx, observation.OutputSize, metricOptions)

	eventAttributes := append(attributes,
		attribute.Int64("stacks.decision.input.size", observation.InputSize),
		attribute.Int64("stacks.decision.output.size", observation.OutputSize),
		attribute.Float64("stacks.decision.duration_seconds", observation.Duration.Seconds()),
	)
	if observation.Confidence != nil {
		r.confidence.Record(ctx, *observation.Confidence, metricOptions)
		eventAttributes = append(eventAttributes, attribute.Float64("stacks.decision.confidence", *observation.Confidence))
	}
	trace.SpanFromContext(ctx).AddEvent(
		decisionEventName,
		trace.WithAttributes(eventAttributes...),
	)

	return nil
}

func (o DecisionObservation) validate() error {
	if o.Name == "" {
		return fmt.Errorf("decision name must not be empty")
	}
	if o.Outcome == "" {
		return fmt.Errorf("decision outcome must not be empty")
	}
	if o.Duration < 0 {
		return fmt.Errorf("decision duration must not be negative")
	}
	if o.InputSize < 0 {
		return fmt.Errorf("decision input size must not be negative")
	}
	if o.OutputSize < 0 {
		return fmt.Errorf("decision output size must not be negative")
	}
	if o.Confidence != nil && (math.IsNaN(*o.Confidence) || math.IsInf(*o.Confidence, 0) || *o.Confidence < 0 || *o.Confidence > 1) {
		return fmt.Errorf("decision confidence must be a finite number between 0 and 1")
	}
	return nil
}
