package main

import (
	"context"
	"io"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"stacks/internal/config"
	"stacks/internal/observability"
)

func TestPoCCommandProviderRegistersSyncAndAnalyzeWithoutConstructingLiveDependencies(t *testing.T) {
	recorder, err := observability.NewDecisionRecorder(noop.NewMeterProvider().Meter("synthetic"))
	if err != nil {
		t.Fatalf("create decision recorder: %v", err)
	}
	commands, err := pocCommandProvider(
		context.Background(), config.Settings{}, io.Discard, io.Discard,
		tracenoop.NewTracerProvider().Tracer("synthetic"), recorder,
	)
	if err != nil {
		t.Fatalf("pocCommandProvider() error = %v", err)
	}
	if commands[string(config.CommandSync)] == nil {
		t.Fatal("sync command is not registered")
	}
	if commands[string(config.CommandAnalyze)] == nil {
		t.Fatal("analyze command is not registered")
	}
}
