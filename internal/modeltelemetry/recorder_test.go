package modeltelemetry

import (
	"context"
	"reflect"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"stacks/internal/modelpolicy"
	"stacks/internal/observability"
)

func TestObservationExposesOnlyBoundedMetadata(t *testing.T) {
	want := []string{
		"Provider", "DataMode", "ModelID", "PromptVersion", "Outcome",
		"InputTokens", "OutputTokens", "TotalTokens", "WallLatency", "ProviderLatency", "Attempts",
	}
	observationType := reflect.TypeOf(Observation{})
	got := make([]string, observationType.NumField())
	for index := range got {
		got[index] = observationType.Field(index).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Observation fields = %v, want only bounded metadata %v", got, want)
	}
}

func TestMetricsRecorderRecordsOnlyBoundedEventAndDistributions(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := tracerProvider.Tracer("stacks").Start(context.Background(), "caller")
	recorder, err := NewMetricsRecorder(meterProvider.Meter("stacks"))
	if err != nil {
		t.Fatalf("NewMetricsRecorder() error = %v", err)
	}

	recorder.Record(ctx, validObservation())
	observability.FinishSpan(span, nil)

	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Events) != 1 || spans[0].Events[0].Name != "stacks.model.invocation" {
		t.Fatalf("recorded spans/events = %#v, want one bounded model invocation event", spans)
	}
	assertAttributes(t, spans[0].Events[0].Attributes, map[string]attribute.Value{
		"stacks.model.provider":         attribute.StringValue("bedrock"),
		"stacks.model.data_mode":        attribute.StringValue("personal"),
		"stacks.model.model_id":         attribute.StringValue("configured-model"),
		"stacks.model.prompt_version":   attribute.StringValue("extract-v2"),
		"stacks.model.outcome":          attribute.StringValue("success"),
		"stacks.model.input_tokens":     attribute.Int64Value(11),
		"stacks.model.output_tokens":    attribute.Int64Value(7),
		"stacks.model.total_tokens":     attribute.Int64Value(18),
		"stacks.model.attempts":         attribute.IntValue(2),
		"stacks.model.wall_seconds":     attribute.Float64Value(0.075),
		"stacks.model.provider_seconds": attribute.Float64Value(0.047),
	})

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	wantMetrics := map[string]bool{
		"stacks.model.invocation.wall.duration":     false,
		"stacks.model.invocation.provider.duration": false,
		"stacks.model.invocation.input.tokens":      false,
		"stacks.model.invocation.output.tokens":     false,
		"stacks.model.invocation.total.tokens":      false,
		"stacks.model.invocation.attempts":          false,
	}
	for _, scope := range collected.ScopeMetrics {
		for _, measured := range scope.Metrics {
			if _, ok := wantMetrics[measured.Name]; !ok {
				t.Errorf("unexpected metric %q", measured.Name)
				continue
			}
			wantMetrics[measured.Name] = true
			assertMetricAttributes(t, measured)
		}
	}
	for name, found := range wantMetrics {
		if !found {
			t.Errorf("metric %q was not recorded", name)
		}
	}
}

func TestMetricsRecorderDropsInvalidObservations(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := tracerProvider.Tracer("stacks").Start(context.Background(), "caller")
	recorder, err := NewMetricsRecorder(meterProvider.Meter("stacks"))
	if err != nil {
		t.Fatalf("NewMetricsRecorder() error = %v", err)
	}

	invalidObservations := []Observation{
		{Provider: "unknown", DataMode: modelpolicy.DataModePersonal, ModelID: "configured-model", PromptVersion: "extract-v2", Outcome: "success"},
		{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModeLegacy, ModelID: "configured-model", PromptVersion: "extract-v2", Outcome: "success"},
		{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal, PromptVersion: "extract-v2", Outcome: "success"},
		{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal, ModelID: "configured-model", Outcome: "success"},
		{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal, ModelID: "configured-model", PromptVersion: "extract-v2", Outcome: "unknown"},
		{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal, ModelID: "configured-model", PromptVersion: "extract-v2", Outcome: "success", InputTokens: -1},
		{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal, ModelID: "configured-model", PromptVersion: "extract-v2", Outcome: "success", WallLatency: -time.Millisecond},
		{Provider: modelpolicy.ProviderBedrock, DataMode: modelpolicy.DataModePersonal, ModelID: "configured-model", PromptVersion: "extract-v2", Outcome: "success", Attempts: -1},
	}
	for _, observation := range invalidObservations {
		recorder.Record(ctx, observation)
	}
	observability.FinishSpan(span, nil)

	if spans := exporter.GetSpans(); len(spans) != 1 || len(spans[0].Events) != 0 {
		t.Fatalf("recorded spans/events = %#v, want no telemetry for invalid observations", spans)
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, measured := range scope.Metrics {
			switch data := measured.Data.(type) {
			case metricdata.Histogram[float64]:
				if len(data.DataPoints) != 0 {
					t.Fatalf("metric %q points = %d, want none", measured.Name, len(data.DataPoints))
				}
			case metricdata.Histogram[int64]:
				if len(data.DataPoints) != 0 {
					t.Fatalf("metric %q points = %d, want none", measured.Name, len(data.DataPoints))
				}
			}
		}
	}
}

func TestMetricsRecorderRecordsQuestionByteCountWithoutAttributes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder, err := NewMetricsRecorder(meterProvider.Meter("stacks"))
	if err != nil {
		t.Fatalf("NewMetricsRecorder() error = %v", err)
	}

	recorder.RecordQuestionBytes(context.Background(), 37)
	recorder.RecordQuestionBytes(context.Background(), -1)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, measured := range scope.Metrics {
			if measured.Name != "stacks.query.planner.question.bytes" {
				continue
			}
			data, ok := measured.Data.(metricdata.Histogram[int64])
			if !ok || len(data.DataPoints) != 1 {
				t.Fatalf("question byte metric = %#v, want one int64 histogram datapoint", measured.Data)
			}
			point := data.DataPoints[0]
			if point.Sum != 37 || point.Count != 1 {
				t.Fatalf("question byte datapoint = %#v, want one value of 37", point)
			}
			if len(point.Attributes.ToSlice()) != 0 {
				t.Fatalf("question byte attributes = %#v, want none", point.Attributes.ToSlice())
			}
			return
		}
	}
	t.Fatal("question byte metric was not recorded")
}

func validObservation() Observation {
	return Observation{
		Provider:        modelpolicy.ProviderBedrock,
		DataMode:        modelpolicy.DataModePersonal,
		ModelID:         "configured-model",
		PromptVersion:   "extract-v2",
		Outcome:         "success",
		InputTokens:     11,
		OutputTokens:    7,
		TotalTokens:     18,
		WallLatency:     75 * time.Millisecond,
		ProviderLatency: 47 * time.Millisecond,
		Attempts:        2,
	}
}

func assertAttributes(t *testing.T, got []attribute.KeyValue, want map[string]attribute.Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("attribute count = %d, want %d: %#v", len(got), len(want), got)
	}
	seen := make(map[string]bool, len(got))
	for _, keyValue := range got {
		value, ok := want[string(keyValue.Key)]
		if !ok || value != keyValue.Value {
			t.Errorf("attribute %q = %v, want %v", keyValue.Key, keyValue.Value, value)
		}
		seen[string(keyValue.Key)] = true
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("attribute %q is missing", key)
		}
	}
}

func assertMetricAttributes(t *testing.T, measured metricdata.Metrics) {
	t.Helper()
	want := map[string]attribute.Value{
		"stacks.model.provider":       attribute.StringValue("bedrock"),
		"stacks.model.data_mode":      attribute.StringValue("personal"),
		"stacks.model.model_id":       attribute.StringValue("configured-model"),
		"stacks.model.prompt_version": attribute.StringValue("extract-v2"),
		"stacks.model.outcome":        attribute.StringValue("success"),
	}
	switch data := measured.Data.(type) {
	case metricdata.Histogram[float64]:
		if len(data.DataPoints) != 1 {
			t.Fatalf("metric %q points = %d, want 1", measured.Name, len(data.DataPoints))
		}
		assertAttributes(t, data.DataPoints[0].Attributes.ToSlice(), want)
	case metricdata.Histogram[int64]:
		if len(data.DataPoints) != 1 {
			t.Fatalf("metric %q points = %d, want 1", measured.Name, len(data.DataPoints))
		}
		assertAttributes(t, data.DataPoints[0].Attributes.ToSlice(), want)
	default:
		t.Fatalf("metric %q type = %T, want histogram", measured.Name, measured.Data)
	}
}
