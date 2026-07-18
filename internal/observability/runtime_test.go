package observability

import (
	"context"
	"testing"

	"stacks/internal/config"
)

func TestDisabledRuntimeDoesNotRequireCollector(t *testing.T) {
	runtime, err := New(context.Background(), config.Settings{LogLevel: "error"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}
