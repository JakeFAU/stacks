package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const contentTypeJSON = "application/json"

type healthResponse struct {
	Status string `json:"status"`
}

// NewHandler returns the instrumented public HTTP API.
func NewHandler(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)

	return otelhttp.NewHandler(
		explicitSpanStatus(mux),
		"http.request",
		otelhttp.WithTracerProvider(tracerProvider),
		otelhttp.WithMeterProvider(meterProvider),
	)
}

func health(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", contentTypeJSON)
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(healthResponse{Status: "ok"})
}

// explicitSpanStatus codifies Stacks' policy that successful spans say OK.
// otelhttp leaves successful server spans unset by default.
func explicitSpanStatus(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		metrics := httpsnoop.CaptureMetrics(next, response, request)

		span := trace.SpanFromContext(request.Context())
		if metrics.Code >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(metrics.Code))
			return
		}
		span.SetStatus(codes.Ok, "")
	})
}
