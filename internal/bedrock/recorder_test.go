package bedrock

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/observability"
)

func TestMetricsInvocationRecorderRecordsBoundedEventAndDistributions(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := tracerProvider.Tracer("stacks").Start(context.Background(), "caller")
	recorder, err := NewMetricsInvocationRecorder(meterProvider.Meter("stacks"))
	if err != nil {
		t.Fatalf("NewMetricsInvocationRecorder() error = %v", err)
	}

	recorder.Record(ctx, InvocationObservation{
		ModelID: testModelID, PromptVersion: testPromptVersion, Outcome: OutcomeSuccess,
		InputTokens: 11, OutputTokens: 7, Attempts: 2,
		WallLatency: 75 * time.Millisecond, ProviderLatency: 47 * time.Millisecond,
	})
	observability.FinishSpan(span, nil)

	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Events) != 1 || spans[0].Events[0].Name != invocationEventName {
		t.Fatalf("recorded spans/events = %#v, want one bounded invocation event", spans)
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	want := map[string]bool{
		"stacks.bedrock.invocation.wall.duration":     false,
		"stacks.bedrock.invocation.provider.duration": false,
		"stacks.bedrock.invocation.input.tokens":      false,
		"stacks.bedrock.invocation.output.tokens":     false,
		"stacks.bedrock.invocation.attempts":          false,
	}
	for _, scope := range collected.ScopeMetrics {
		for _, measured := range scope.Metrics {
			if _, ok := want[measured.Name]; ok {
				want[measured.Name] = true
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("metric %q was not recorded", name)
		}
	}
}
