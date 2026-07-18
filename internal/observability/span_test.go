package observability

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestFinishSpanSetsExplicitStatus(t *testing.T) {
	tests := []struct {
		name           string
		operationError error
		wantStatus     codes.Code
		wantEvents     int
	}{
		{name: "success", wantStatus: codes.Ok},
		{name: "error", operationError: errors.New("failed"), wantStatus: codes.Error, wantEvents: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
			_, span := provider.Tracer(instrumentationName).Start(context.Background(), "operation")

			FinishSpan(span, test.operationError)

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("span count = %d, want 1", len(spans))
			}
			if spans[0].Status.Code != test.wantStatus {
				t.Errorf("status = %v, want %v", spans[0].Status.Code, test.wantStatus)
			}
			if len(spans[0].Events) != test.wantEvents {
				t.Errorf("event count = %d, want %d", len(spans[0].Events), test.wantEvents)
			}
		})
	}
}
