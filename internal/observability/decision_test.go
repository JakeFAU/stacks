package observability

import (
	"context"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestDecisionRecorderAddsEventWithoutChildSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer(instrumentationName).Start(context.Background(), "query")
	recorder, err := NewDecisionRecorder(metricnoop.NewMeterProvider().Meter(instrumentationName))
	if err != nil {
		t.Fatalf("NewDecisionRecorder() error = %v", err)
	}
	confidence := 0.8

	if err := recorder.Record(ctx, DecisionObservation{
		Name:       "candidate_filter",
		Outcome:    "retained",
		Duration:   25 * time.Millisecond,
		InputSize:  12,
		OutputSize: 3,
		Confidence: &confidence,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	FinishSpan(span, nil)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if len(spans[0].Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(spans[0].Events))
	}
	if spans[0].Events[0].Name != decisionEventName {
		t.Errorf("event name = %q, want %q", spans[0].Events[0].Name, decisionEventName)
	}
}

func TestDecisionRecorderRecordsDistributions(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder, err := NewDecisionRecorder(provider.Meter(instrumentationName))
	if err != nil {
		t.Fatalf("NewDecisionRecorder() error = %v", err)
	}
	confidence := 0.6
	if err := recorder.Record(context.Background(), DecisionObservation{
		Name:       "entity_resolution",
		Outcome:    "matched",
		Duration:   40 * time.Millisecond,
		InputSize:  8,
		OutputSize: 1,
		Confidence: &confidence,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	wantNames := map[string]bool{
		"stacks.decision.duration":    false,
		"stacks.decision.input.size":  false,
		"stacks.decision.output.size": false,
		"stacks.decision.confidence":  false,
	}
	for _, scope := range collected.ScopeMetrics {
		for _, measured := range scope.Metrics {
			if _, wanted := wantNames[measured.Name]; !wanted {
				continue
			}
			switch histogram := measured.Data.(type) {
			case metricdata.Histogram[int64]:
				if len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 {
					t.Errorf("%s histogram count is not 1", measured.Name)
				}
			case metricdata.Histogram[float64]:
				if len(histogram.DataPoints) != 1 || histogram.DataPoints[0].Count != 1 {
					t.Errorf("%s histogram count is not 1", measured.Name)
				}
			default:
				t.Errorf("%s data type = %T, want histogram", measured.Name, measured.Data)
			}
			wantNames[measured.Name] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("metric %q not collected", name)
		}
	}
}

func TestDecisionRecorderRejectsInvalidObservations(t *testing.T) {
	recorder, err := NewDecisionRecorder(metricnoop.NewMeterProvider().Meter(instrumentationName))
	if err != nil {
		t.Fatalf("NewDecisionRecorder() error = %v", err)
	}
	invalidConfidence := 1.1

	tests := []DecisionObservation{
		{Outcome: "selected"},
		{Name: "ranking"},
		{Name: "ranking", Outcome: "selected", Duration: -time.Second},
		{Name: "ranking", Outcome: "selected", InputSize: -1},
		{Name: "ranking", Outcome: "selected", OutputSize: -1},
		{Name: "ranking", Outcome: "selected", Confidence: &invalidConfidence},
	}
	for index, observation := range tests {
		if err := recorder.Record(context.Background(), observation); err == nil {
			t.Errorf("Record() test %d error = nil, want validation error", index)
		}
	}
}
